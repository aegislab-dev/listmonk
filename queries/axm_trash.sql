-- name: axm-trash-resolve-lists
-- The lists a delete by IDs or by query reaches: the same conditions as delete-lists.
SELECT id AS list_id, name FROM lists
WHERE CASE
    WHEN CARDINALITY($1::INT[]) > 0 THEN id = ANY($1)
    ELSE ($2 = '' OR to_tsvector(name) @@ to_tsquery($2))
END
AND CASE
    WHEN $3 = TRUE THEN TRUE ELSE id = ANY($4::INT[])
END
ORDER BY id;

-- name: axm-trash-busy-lists
-- Lists in $1 that a running, paused or scheduled campaign sends to, with that campaign.
SELECT DISTINCT ON (cl.list_id) cl.list_id, c.id AS campaign_id, c.name AS campaign_name
FROM campaign_lists cl JOIN campaigns c ON c.id = cl.campaign_id
WHERE cl.list_id = ANY($1::INT[]) AND c.status IN ('running', 'paused', 'scheduled')
ORDER BY cl.list_id, c.id;

-- name: axm-trash-save-lists
-- Copies the lists $1 into the trash with the user who deletes them ($2 ID, $3 username,
-- $4 name). Their subscriptions are copied by axm-trash-save-subs. Runs in the transaction
-- that deletes the lists.
INSERT INTO axm_deleted_lists (list_id, uuid, name, list, sub_count, deleted_by, deleted_by_username,
    deleted_by_name, campaign_links, roles, draws, source_draws, draw_campaigns)
SELECT l.id, l.uuid, l.name, to_jsonb(l),
    (SELECT COUNT(*) FROM subscriber_lists sl WHERE sl.list_id = l.id),
    $2, $3, $4,
    COALESCE((SELECT array_agg(cl.id) FROM campaign_lists cl WHERE cl.list_id = l.id), '{}'),
    COALESCE((SELECT jsonb_agg(jsonb_build_object('parent_id', r.parent_id, 'permissions', r.permissions))
        FROM roles r WHERE r.list_id = l.id AND r.parent_id IS NOT NULL), '[]'),
    COALESCE((SELECT array_agg(d.id) FROM axm_list_draws d WHERE d.list_id = l.id), '{}'),
    COALESCE((SELECT array_agg(d.id) FROM axm_list_draws d WHERE d.source_list_id = l.id), '{}'),
    COALESCE((SELECT array_agg(dc.campaign_id) FROM axm_list_draw_campaigns dc WHERE dc.source_list_id = l.id), '{}')
FROM lists l WHERE l.id = ANY($1::INT[])
ON CONFLICT (list_id) DO UPDATE SET uuid = EXCLUDED.uuid, name = EXCLUDED.name, list = EXCLUDED.list,
    sub_count = EXCLUDED.sub_count, deleted_by = EXCLUDED.deleted_by,
    deleted_by_username = EXCLUDED.deleted_by_username, deleted_by_name = EXCLUDED.deleted_by_name,
    deleted_at = NOW(), campaign_links = EXCLUDED.campaign_links, roles = EXCLUDED.roles,
    draws = EXCLUDED.draws, source_draws = EXCLUDED.source_draws, draw_campaigns = EXCLUDED.draw_campaigns
RETURNING list_id, name, sub_count;

-- name: axm-trash-clear-subs
DELETE FROM axm_deleted_list_subs WHERE list_id = ANY($1::INT[]);

-- name: axm-trash-save-subs
INSERT INTO axm_deleted_list_subs (list_id, subscriber_id, status, meta, created_at, updated_at)
SELECT list_id, subscriber_id, status, meta, created_at, updated_at
FROM subscriber_lists WHERE list_id = ANY($1::INT[]);

-- name: axm-get-list-trash
-- The trash, newest first. $1 = the user sees every list, $2 = the user otherwise sees only
-- what they deleted themselves.
SELECT list_id, uuid, name, sub_count, deleted_by, deleted_by_username, deleted_by_name, deleted_at,
    list->>'type' AS type, list->>'optin' AS optin, list->>'status' AS status,
    (list->'axm_filter') IS NOT NULL AND list->'axm_filter' != 'null'::JSONB AS dynamic,
    COALESCE(list->'tags', '[]'::JSONB) AS tags
FROM axm_deleted_lists
WHERE $1 = TRUE OR deleted_by = $2
ORDER BY deleted_at DESC, list_id DESC;

-- name: axm-trash-lock
-- One trash row, locked for the restore.
SELECT list_id, uuid, name, list, sub_count, deleted_by, campaign_links, roles, draws, source_draws, draw_campaigns
FROM axm_deleted_lists WHERE list_id = $1 FOR UPDATE;

-- name: axm-trash-restore-list
INSERT INTO lists (id, uuid, name, type, optin, status, tags, description, created_at, updated_at, axm_filter, axm_preset)
SELECT r.id, r.uuid, r.name, r.type, r.optin, r.status, r.tags, COALESCE(r.description, ''), r.created_at, r.updated_at,
    r.axm_filter, COALESCE(r.axm_preset, 'creator')
FROM jsonb_populate_record(NULL::lists, $1::JSONB) r;

-- name: axm-trash-restore-subs
-- The subscriptions the list had, for the subscribers that still exist.
INSERT INTO subscriber_lists (subscriber_id, list_id, meta, status, created_at, updated_at)
SELECT s.subscriber_id, s.list_id, s.meta, s.status, s.created_at, s.updated_at
FROM axm_deleted_list_subs s WHERE s.list_id = $1
ON CONFLICT (subscriber_id, list_id) DO NOTHING;

-- name: axm-trash-restore-campaigns
-- Campaigns that sent to the list get it back, unless they got another row for it meanwhile.
UPDATE campaign_lists SET list_id = $1::INT
WHERE id = ANY($2::INT[]) AND list_id IS NULL
    AND NOT EXISTS (SELECT 1 FROM campaign_lists o WHERE o.campaign_id = campaign_lists.campaign_id AND o.list_id = $1::INT);

-- name: axm-trash-restore-roles
INSERT INTO roles (parent_id, list_id, type, permissions)
SELECT (e->>'parent_id')::INT, $1, 'list', ARRAY(SELECT jsonb_array_elements_text(e->'permissions'))
FROM jsonb_array_elements($2::JSONB) e
WHERE EXISTS (SELECT 1 FROM roles p WHERE p.id = (e->>'parent_id')::INT)
ON CONFLICT (parent_id, list_id) DO NOTHING;

-- name: axm-trash-restore-draws
-- A drawn list gets its draw record back; a source list gets back the draws made from it
-- and the campaigns kept for them.
WITH a AS (
    UPDATE axm_list_draws SET list_id = $1::INT, deleted_at = NULL, result = result - 'campaigns' - 'campaigns_finished'
    WHERE id = ANY($2::INT[]) AND list_id IS NULL
        AND NOT EXISTS (SELECT 1 FROM axm_list_draws o WHERE o.list_id = $1::INT)
    RETURNING 1
), b AS (
    UPDATE axm_list_draws SET source_list_id = $1::INT WHERE id = ANY($3::INT[]) AND source_list_id IS NULL RETURNING 1
), c AS (
    INSERT INTO axm_list_draw_campaigns (source_list_id, campaign_id)
    SELECT $1::INT, c.id FROM campaigns c WHERE c.id = ANY($4::INT[])
    ON CONFLICT DO NOTHING RETURNING 1
)
SELECT (SELECT COUNT(*) FROM a) + (SELECT COUNT(*) FROM b) + (SELECT COUNT(*) FROM c);

-- name: axm-trash-drop
DELETE FROM axm_deleted_lists WHERE list_id = $1;

-- name: axm-trash-purge
-- Deletes the trash rows (and their subscriptions) deleted before $1.
DELETE FROM axm_deleted_lists WHERE deleted_at < $1 RETURNING list_id, name, sub_count;
