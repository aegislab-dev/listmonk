package migrations

import (
	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
	"log"
)

// V6_2_1_axm_2 adds dynamic list rules and guards membership writes.
func V6_2_1_axm_2(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`ALTER TABLE lists ADD COLUMN IF NOT EXISTS axm_filter JSONB NULL;
-- AXM dynamic lists. Keep this block identical to V6_2_1_axm_2.
CREATE OR REPLACE FUNCTION axm_filter_match(p_attribs JSONB, p_filter JSONB)
RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    c JSONB;
    v JSONB;
    hit BOOLEAN;
    result BOOLEAN;
BEGIN
    IF p_filter->>'version' IS DISTINCT FROM '1'
       OR p_filter->>'match' IS NULL OR p_filter->>'match' NOT IN ('all', 'any')
       OR jsonb_typeof(p_filter->'conditions') IS DISTINCT FROM 'array' THEN
        RETURN FALSE;
    END IF;
    IF jsonb_array_length(p_filter->'conditions') NOT BETWEEN 1 AND 20 THEN RETURN FALSE; END IF;
    result := p_filter->>'match' = 'all';
    FOR c IN SELECT * FROM jsonb_array_elements(p_filter->'conditions') LOOP
        IF jsonb_typeof(c) IS DISTINCT FROM 'object'
            OR jsonb_typeof(c->'key') IS DISTINCT FROM 'string'
            OR length(c->>'key') NOT BETWEEN 1 AND 64 THEN
            RETURN FALSE;
        END IF;
        v := p_attribs->(c->>'key');
        hit := CASE c->>'op'
            WHEN 'eq' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean') AND v #>> '{}' = c->>'value'
            WHEN 'contains' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean')
                AND strpos(lower(v #>> '{}'), lower(c->>'value')) > 0
            WHEN 'empty' THEN v IS NULL OR v = 'null'::JSONB OR v = '[]'::JSONB
                OR (jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\n\r') = '')
            WHEN 'has' THEN jsonb_typeof(v) = 'array' AND v @> jsonb_build_array(c->>'value')
            ELSE FALSE
        END;
        IF p_filter->>'match' = 'all' THEN result := result AND COALESCE(hit, FALSE);
        ELSE result := result OR COALESCE(hit, FALSE); END IF;
    END LOOP;
    RETURN result;
END;
$$;

CREATE OR REPLACE FUNCTION axm_filter_members(p_filter JSONB, p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT s.id FROM subscribers s
    WHERE s.status != 'blocklisted' AND s.id > p_after AND s.id <= p_upto
        AND axm_filter_match(s.attribs, p_filter)
        AND EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL
                AND (p_filter->'base'->>'type' = 'all' OR
                    (p_filter->'base'->>'type' = 'lists' AND
                        COALESCE(p_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
                AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                    ELSE sl.status != 'unsubscribed' END
        )
        AND (p_list_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
                AND ex.list_id = p_list_id AND ex.status = 'unsubscribed'
        ))
    ORDER BY s.id LIMIT p_limit;
$$;

CREATE OR REPLACE FUNCTION axm_dynamic_list_members(p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT m.subscriber_id FROM lists l
    CROSS JOIN LATERAL axm_filter_members(l.axm_filter, l.id, p_after, p_upto, p_limit) m
    WHERE l.id = p_list_id AND l.axm_filter IS NOT NULL;
$$;

CREATE OR REPLACE FUNCTION axm_guard_dynamic_subscription()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status != 'unsubscribed' AND EXISTS (
        SELECT 1 FROM lists WHERE id = NEW.list_id AND axm_filter IS NOT NULL
    ) THEN RETURN NULL; END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_guard_dynamic_subscription ON subscriber_lists;
CREATE TRIGGER axm_guard_dynamic_subscription BEFORE INSERT OR UPDATE ON subscriber_lists
    FOR EACH ROW EXECUTE FUNCTION axm_guard_dynamic_subscription();
`)
	return err
}
