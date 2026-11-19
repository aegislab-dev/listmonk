-- name: axm-draw-lock
-- Serializes draws from one source, so two draws never pick the same people.
SELECT pg_advisory_xact_lock(7231, $1::INT);

-- name: axm-draw-source
-- The source of a draw and what decides whether it may be one.
SELECT l.id, l.name, l.status, l.axm_preset, l.tags, l.axm_filter IS NOT NULL AS dynamic,
    EXISTS (SELECT 1 FROM axm_list_draws d WHERE d.list_id = l.id) AS drawn
FROM lists l WHERE l.id = $1;

-- name: axm-draw-counts
-- The numbers of a draw from $1 with the exclusions $2 (same_source | all) and $3 (days, 0 =
-- all). Preview and draw read the same function, so their numbers agree.
SELECT COUNT(*) AS target,
    COUNT(*) FILTER (WHERE reason = 1) AS already_drawn,
    COUNT(*) FILTER (WHERE reason = 2) AS already_received,
    COUNT(*) FILTER (WHERE reason = 0) AS candidates
FROM axm_draw_candidates($1::INT, $2::TEXT, $3::INT);

-- name: axm-draw-names
-- Names that are $1 or $1 followed by " (n)".
SELECT name FROM lists WHERE name = $1 OR name LIKE $2;

-- name: axm-draw-create-list
INSERT INTO lists (uuid, name, type, optin, status, tags, description, axm_preset)
VALUES ($1, $2, 'private', 'single', 'active', $3, $4, $5) RETURNING id, created_at, updated_at;

-- name: axm-draw-add-members
-- Puts the first $5 candidates of source $1 on the new list $2, in a fixed pseudo-random
-- order that depends only on the person and the source.
INSERT INTO subscriber_lists (subscriber_id, list_id, status)
SELECT c.subscriber_id, $2::INT, 'confirmed' FROM axm_draw_candidates($1::INT, $3::TEXT, $4::INT) c
WHERE c.reason = 0
ORDER BY hashint8(c.subscriber_id::BIGINT * 1000003 + $1::INT), c.subscriber_id
LIMIT $5::INT;

-- name: axm-draw-record
INSERT INTO axm_list_draws (list_id, source_list_id, source_name, list_name, drawn_by, settings, result)
VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7::JSONB) RETURNING id, drawn_at;

-- name: axm-get-list-draws
-- Draw records of source $1 (0 = any) or of the drawn lists $2 (empty = any), newest first.
SELECT d.id, d.list_id, d.source_list_id, d.source_name, COALESCE(l.name, d.list_name) AS list_name,
    d.drawn_at, d.drawn_by, COALESCE(u.name, '') AS drawn_by_name, d.settings, d.result, d.deleted_at,
    CASE WHEN d.list_id IS NULL THEN COALESCE((d.result->>'campaigns')::INT, 0)
        ELSE (SELECT COUNT(*) FROM campaign_lists cl WHERE cl.list_id = d.list_id) END AS campaigns,
    CASE WHEN d.list_id IS NULL THEN COALESCE((d.result->>'campaigns_finished')::INT, 0)
        ELSE (SELECT COUNT(*) FROM campaign_lists cl JOIN campaigns c ON c.id = cl.campaign_id
            WHERE cl.list_id = d.list_id AND c.status = 'finished') END AS campaigns_finished
FROM axm_list_draws d
LEFT JOIN lists l ON l.id = d.list_id
LEFT JOIN users u ON u.id = d.drawn_by
WHERE ($1::INT = 0 OR d.source_list_id = $1::INT)
    AND (CARDINALITY($2::INT[]) = 0 OR d.list_id = ANY($2::INT[]))
ORDER BY d.drawn_at DESC, d.id DESC
LIMIT $3::INT;
