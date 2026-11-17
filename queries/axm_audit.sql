-- name: axm-insert-audit
-- Adds one audit row. $9 is the NULL-able count, $10 the detail JSON (no subscriber e-mail,
-- name or attribute value goes in it).
INSERT INTO axm_audit_log (user_id, username, user_name, user_type, action, target_type, target_id, target_name, count, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::JSONB);

-- name: axm-query-audit
-- One page of the audit log, newest first. $1/$2 bound the time (NULL = open), $3 is a user,
-- $4 the areas (the part of the action before the dot), $5 the actions, $6 a text that the
-- target name contains (compared without LIKE so % and _ stay plain).
SELECT id, created_at, user_id, username, user_name, user_type, action, target_type, target_id,
    target_name, count, detail, COUNT(*) OVER () AS total
FROM axm_audit_log
WHERE ($1::TIMESTAMPTZ IS NULL OR created_at >= $1)
    AND ($2::TIMESTAMPTZ IS NULL OR created_at < $2)
    AND ($3::INT IS NULL OR user_id = $3)
    AND (CARDINALITY($4::TEXT[]) = 0 OR split_part(action, '.', 1) = ANY($4))
    AND (CARDINALITY($5::TEXT[]) = 0 OR action = ANY($5))
    AND ($6::TEXT = '' OR strpos(lower(target_name), lower($6)) > 0)
ORDER BY created_at DESC, id DESC
OFFSET $7 LIMIT NULLIF($8::INT, 0);

-- name: axm-audit-users
-- The people who appear in the log (for the filter), by their last row.
SELECT DISTINCT ON (user_id) user_id, username, user_name
FROM axm_audit_log WHERE user_id IS NOT NULL
ORDER BY user_id, created_at DESC, id DESC;

-- name: axm-delete-audit
-- Deletes up to $2 audit rows made before $1 and returns how many were deleted.
WITH d AS (
    DELETE FROM axm_audit_log WHERE id = ANY(ARRAY(
        SELECT id FROM axm_audit_log WHERE created_at < $1 LIMIT $2
    )) RETURNING 1
)
SELECT COUNT(*) FROM d;

-- name: axm-resolve-campaigns-to-delete
-- The campaigns a delete by IDs or by query reaches (same conditions as delete-campaigns).
SELECT c.id, c.name FROM campaigns c
WHERE (
    CASE
        WHEN CARDINALITY($1::INT[]) > 0 THEN id = ANY($1)
        ELSE $2 = '' OR TO_TSVECTOR(CONCAT(name, ' ', subject)) @@ TO_TSQUERY($2) OR CONCAT(c.name, ' ', c.subject) ILIKE $2
    END
)
AND (
    $3 OR EXISTS (
        SELECT 1 FROM campaign_lists WHERE campaign_id = c.id AND list_id = ANY($4::INT[])
    )
)
ORDER BY c.id;
