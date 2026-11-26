package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_18 speeds up unbounded dynamic list membership for campaign counts.
func V6_2_1_axm_18(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM unbounded dynamic list membership (LST-67). Keep this block identical to V6_2_1_axm_18.
-- Keep JIT local to this enumeration: compiling the joins per list costs hundreds of
-- milliseconds on the 400k fixture, and hiding the function body keeps unused branches
-- from triggering JIT for static-only campaign counts.
CREATE OR REPLACE FUNCTION axm_filter_members_unlimited(p_filter JSONB, p_list_id INT, p_after INT, p_upto INT)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE PARALLEL SAFE SET jit = off AS $$
    WITH rule AS MATERIALIZED (
        SELECT p_filter #>> '{base,type}' AS base_type,
            COALESCE(p_filter #> '{base,lists}', '[]'::JSONB) AS base_lists,
            CASE WHEN p_filter->>'version' = '2' THEN COALESCE(p_filter #> '{exclude,lists}', '[]'::JSONB)
                ELSE '[]'::JSONB END AS exclude_lists,
            CASE WHEN p_filter->>'version' = '2' THEN jsonb_set(p_filter, '{version}', '1'::JSONB)
                ELSE p_filter END AS matcher,
            p_filter->>'version' = '2' AND p_filter->'conditions' = '[]'::JSONB AS match_all
    ),
    -- For a single equality, the 'all' and 'any' forms are identical. Resolve the
    -- validated key and value once for the attribute-first path.
    match_rule AS MATERIALIZED (
        SELECT r.*, cond->>'value' AS eq_value, CASE
            -- A numeric JSON value prints as a number; true and false also match
            -- their textual spellings. Other alphabetic values only match strings.
            WHEN cond->>'value' ~ '^[[:alpha:]]' AND cond->>'value' NOT IN ('true', 'false')
            THEN to_jsonb(cond->>'value') END AS eq_text, CASE
            WHEN r.matcher->>'version' = '1' AND r.matcher->>'match' IN ('all', 'any')
              AND r.matcher->'conditions' = jsonb_build_array(cond)
              AND cond->>'op' = 'eq' AND jsonb_typeof(cond->'key') = 'string'
              AND length(cond->>'key') BETWEEN 1 AND 64
            THEN cond->>'key' END AS eq_key
        FROM rule r CROSS JOIN LATERAL (SELECT r.matcher->'conditions'->0 AS cond) c
    ),
    base_lists AS MATERIALIZED (
        SELECT l.id, l.optin FROM lists l CROSS JOIN rule r
        WHERE l.axm_filter IS NULL
          AND (r.base_type = 'all' OR (r.base_type = 'lists' AND r.base_lists @> to_jsonb(l.id)))
    ),
    base_members AS NOT MATERIALIZED (
        SELECT sl.subscriber_id FROM base_lists l
        CROSS JOIN LATERAL (
            SELECT sl.subscriber_id FROM subscriber_lists sl
            WHERE sl.list_id = l.id AND sl.subscriber_id > p_after AND sl.subscriber_id <= p_upto
              AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                  ELSE sl.status != 'unsubscribed' END
            OFFSET 0
        ) sl
    ),
    -- Limit the size probe. Narrow bases evaluate conditions only for their members.
    -- The attribute scan saves index lookups for broad bases with a cheap comparison.
    strategy AS MATERIALIZED (
        SELECT CASE
            WHEN NOT EXISTS (SELECT 1 FROM base_members OFFSET 4095 LIMIT 1) THEN 'scoped'
            WHEN r.eq_key IS NOT NULL THEN 'attributes'
            ELSE 'default'
        END AS path FROM match_rule r
    ),
    scoped AS MATERIALIZED (
        SELECT DISTINCT subscriber_id FROM base_members
        WHERE (SELECT path FROM strategy) = 'scoped'
    ),
    matched AS MATERIALIZED (
        SELECT s.id FROM subscribers s CROSS JOIN match_rule r
        WHERE (SELECT path FROM strategy) = 'attributes'
          AND s.status != 'blocklisted' AND s.id > p_after AND s.id <= p_upto
          AND CASE WHEN r.eq_text IS NOT NULL THEN s.attribs->r.eq_key = r.eq_text
              ELSE jsonb_typeof(s.attribs->r.eq_key) IN ('string', 'number', 'boolean')
                  AND (s.attribs->r.eq_key) #>> '{}' = r.eq_value
          END
    ),
    candidates AS (
        SELECT s.id FROM matched s
        WHERE EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN base_lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id
              AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                  ELSE sl.status != 'unsubscribed' END
        )
        UNION ALL
        SELECT found.id FROM scoped b CROSS JOIN rule r
        CROSS JOIN LATERAL (
            SELECT s.id FROM subscribers s
            WHERE s.id = b.subscriber_id AND s.status != 'blocklisted'
              AND (r.match_all OR axm_filter_match(s.attribs, r.matcher))
            OFFSET 0
        ) found
    )
    SELECT s.id FROM candidates s CROSS JOIN rule r
    WHERE (r.exclude_lists = '[]'::JSONB OR NOT EXISTS (
        SELECT 1 FROM subscriber_lists xl
        WHERE xl.subscriber_id = s.id AND r.exclude_lists @> to_jsonb(xl.list_id)
    ))
    AND NOT EXISTS (
        SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
          AND ex.list_id = p_list_id AND ex.status = 'unsubscribed'
    )
    UNION ALL
    SELECT m.subscriber_id FROM axm_filter_members(p_filter, p_list_id, p_after, p_upto) m
    WHERE (SELECT path FROM strategy) = 'default'
    ORDER BY 1;
$$;

-- Keep the bounded query for batches and samples so their LIMIT can stop the scan early.
CREATE OR REPLACE FUNCTION axm_dynamic_list_members(p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE PARALLEL SAFE AS $$
    SELECT m.subscriber_id FROM lists l
    CROSS JOIN LATERAL axm_filter_members_unlimited(l.axm_filter, l.id, p_after, p_upto) m
    WHERE l.id = p_list_id AND l.axm_filter IS NOT NULL AND p_limit IS NULL
    UNION ALL
    SELECT m.subscriber_id FROM lists l
    CROSS JOIN LATERAL axm_filter_members(l.axm_filter, l.id, p_after, p_upto, p_limit) m
    WHERE l.id = p_list_id AND l.axm_filter IS NOT NULL AND p_limit IS NOT NULL;
$$;
-- End of AXM unbounded dynamic list membership.`)
	return err
}
