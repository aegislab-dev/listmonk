package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_4 adds numeric comparisons to dynamic list conditions and replaces the
// per-row cursor in axm_filter_match with an index loop.
func V6_2_1_axm_4(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM dynamic list conditions. Keep this block identical to V6_2_1_axm_4.
-- Reads a JSON number, or a JSON string that holds one, as NUMERIC. A string of up to 64
-- characters may have blanks around it, a sign, a fraction, and either a two-digit exponent
-- or thousands commas in groups of three. Any other value is NULL, never an error: each cast
-- runs only after its pattern matched, and the patterns admit nothing NUMERIC rejects.
CREATE OR REPLACE FUNCTION axm_to_number(p JSONB)
RETURNS NUMERIC LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE jsonb_typeof(p)
        WHEN 'number' THEN p::NUMERIC
        WHEN 'string' THEN CASE
            WHEN length(p #>> '{}') > 64 THEN NULL
            WHEN btrim(p #>> '{}', E' \t\n\r') ~ '^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]{1,2})?$'
                THEN btrim(p #>> '{}', E' \t\n\r')::NUMERIC
            WHEN btrim(p #>> '{}', E' \t\n\r') ~ '^[+-]?[0-9]{1,3}(,[0-9]{3})+(\.[0-9]*)?$'
                THEN replace(btrim(p #>> '{}', E' \t\n\r'), ',', '')::NUMERIC
        END
    END;
$$;

CREATE OR REPLACE FUNCTION axm_filter_match(p_attribs JSONB, p_filter JSONB)
RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    c JSONB;
    v JSONB;
    w JSONB;
    n NUMERIC;
    a NUMERIC;
    op TEXT;
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
    FOR i IN 0 .. jsonb_array_length(p_filter->'conditions') - 1 LOOP
        c := p_filter->'conditions'->i;
        IF jsonb_typeof(c) IS DISTINCT FROM 'object'
            OR jsonb_typeof(c->'key') IS DISTINCT FROM 'string'
            OR length(c->>'key') NOT BETWEEN 1 AND 64 THEN
            RETURN FALSE;
        END IF;
        v := p_attribs->(c->>'key');
        op := c->>'op';
        IF op IN ('gte', 'gt', 'lte', 'lt', 'between', 'not_number') THEN
            -- Plain variables keep the inlined axm_to_number() from repeating JSON lookups.
            w := c->'value';
            n := axm_to_number(v);
            a := axm_to_number(w);
            hit := CASE op
                WHEN 'gte' THEN n >= a
                WHEN 'gt' THEN n > a
                WHEN 'lte' THEN n <= a
                WHEN 'lt' THEN n < a
                WHEN 'between' THEN n >= a AND n < axm_to_number(c->'value2')
                -- Preview diagnostic, not accepted in saved filters: a value that is not a number.
                ELSE n IS NULL AND NOT (v IS NULL OR v = 'null'::JSONB OR v = '[]'::JSONB
                    OR (jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\n\r') = ''))
            END;
        ELSE
            hit := CASE op
                WHEN 'eq' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean') AND v #>> '{}' = c->>'value'
                WHEN 'contains' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean')
                    AND strpos(lower(v #>> '{}'), lower(c->>'value')) > 0
                WHEN 'empty' THEN v IS NULL OR v = 'null'::JSONB OR v = '[]'::JSONB
                    OR (jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\n\r') = '')
                WHEN 'has' THEN jsonb_typeof(v) = 'array' AND v @> jsonb_build_array(c->>'value')
                ELSE FALSE
            END;
        END IF;
        IF p_filter->>'match' = 'all' THEN result := result AND COALESCE(hit, FALSE);
        ELSE result := result OR COALESCE(hit, FALSE); END IF;
    END LOOP;
    RETURN result;
END;
$$;

-- AXM dynamic list planning hints. Keep this block identical to V6_2_1_axm_4.
ALTER FUNCTION axm_filter_members(JSONB, INT, INT, INT, INT) PARALLEL SAFE;
ALTER FUNCTION axm_dynamic_list_members(INT, INT, INT, INT) PARALLEL SAFE;
`)
	return err
}
