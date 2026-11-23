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

-- name: axm-lock-campaigns-to-delete
-- LST-55. Step 1 of a guarded delete: the IDs of the campaigns a delete by IDs ($1) or by
-- search ($2) reaches (the conditions of delete-campaigns), locked FOR UPDATE. A transaction
-- that is recording send records for one of them holds a share lock on it (the foreign key),
-- so this waits for it to commit.
SELECT c.id FROM campaigns c
WHERE (
    CASE
        WHEN CARDINALITY($1::INT[]) > 0 THEN c.id = ANY($1)
        ELSE $2 = '' OR TO_TSVECTOR(CONCAT(c.name, ' ', c.subject)) @@ TO_TSQUERY($2) OR CONCAT(c.name, ' ', c.subject) ILIKE $2
    END
)
AND (
    $3 OR EXISTS (
        SELECT 1 FROM campaign_lists WHERE campaign_id = c.id AND list_id = ANY($4::INT[])
    )
)
ORDER BY c.id
FOR UPDATE OF c;

-- name: axm-delete-campaigns-guarded
-- LST-55. Step 2, in the same transaction as axm-lock-campaigns-to-delete and run after it:
-- the statement takes its snapshot after the locks were granted, so it sees send records that
-- were committed while step 1 waited (one statement would read the snapshot from before the
-- wait and delete them). Deletes the campaigns $1 unless one of them is a campaign that is
-- sending or has sent. In that case nothing is deleted. The status, sent and to_send that come
-- back are the values the campaign had when it was deleted (or refused). $2 is the number of
-- months the send records are kept.
-- block: 'sending' (running or paused), 'sent' (it has send records, or sent mail within the
-- retention period and its records may not be written yet), '' (may be deleted).
WITH target AS (
    SELECT c.id, c.name, c.status, c.sent, c.to_send, COALESCE(c.started_at, c.created_at) AS began
    FROM campaigns c
    WHERE c.id = ANY($1::INT[])
    ORDER BY c.id
    FOR UPDATE OF c
),
checked AS (
    SELECT t.id, t.name, t.status::TEXT AS status, t.sent, t.to_send,
        CASE
            WHEN t.status IN ('running', 'paused') THEN 'sending'
            WHEN EXISTS (SELECT 1 FROM axm_campaign_sends s WHERE s.campaign_id = t.id) THEN 'sent'
            WHEN t.sent > 0 AND t.began > NOW() - MAKE_INTERVAL(months => $2::INT) THEN 'sent'
        END AS block
    FROM target t
),
del AS (
    DELETE FROM campaigns
    WHERE id IN (SELECT id FROM checked) AND NOT EXISTS (SELECT 1 FROM checked WHERE block IS NOT NULL)
    RETURNING id
)
SELECT k.id, k.name, k.status, k.sent, k.to_send, COALESCE(k.block, '') AS block, (d.id IS NOT NULL) AS deleted
FROM checked k LEFT JOIN del d ON d.id = k.id
ORDER BY k.id;
