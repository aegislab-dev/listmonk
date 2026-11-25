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
    -- validated key and value once; other conditions still use axm_filter_match.
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
    -- Scan attributes before checking the base membership. This avoids fetching the
    -- subscriber's heap row through an index for each member of a large base list.
    matched AS MATERIALIZED (
        SELECT s.id FROM subscribers s CROSS JOIN match_rule r
        WHERE s.status != 'blocklisted' AND s.id > p_after AND s.id <= p_upto
          AND CASE
              WHEN r.match_all THEN true
              WHEN r.eq_key IS NOT NULL AND r.eq_text IS NOT NULL THEN s.attribs->r.eq_key = r.eq_text
              WHEN r.eq_key IS NOT NULL THEN jsonb_typeof(s.attribs->r.eq_key) IN ('string', 'number', 'boolean')
                  AND (s.attribs->r.eq_key) #>> '{}' = r.eq_value
              ELSE axm_filter_match(s.attribs, r.matcher)
          END
    )
    SELECT s.id FROM matched s CROSS JOIN rule r
    WHERE EXISTS (
        SELECT 1 FROM subscriber_lists sl JOIN base_lists l ON l.id = sl.list_id
        WHERE sl.subscriber_id = s.id
          AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
              ELSE sl.status != 'unsubscribed' END
    )
    AND (r.exclude_lists = '[]'::JSONB OR NOT EXISTS (
        SELECT 1 FROM subscriber_lists xl
        WHERE xl.subscriber_id = s.id AND r.exclude_lists @> to_jsonb(xl.list_id)
    ))
    AND NOT EXISTS (
        SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
          AND ex.list_id = p_list_id AND ex.status = 'unsubscribed'
    )
    ORDER BY s.id;
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
