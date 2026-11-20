-- name: axm-recent-exclude-counts
SELECT COUNT(*) AS target, COUNT(*) FILTER (WHERE excluded) AS excluded, NOW() AS as_of
FROM axm_recent_exclusion($1, $2, $3, $4, $5);

-- name: axm-recent-sends-coverage
SELECT MIN(sent_at) FROM axm_campaign_sends;

-- name: axm-get-recent-rule
SELECT TRUE AS enabled, rule.days, rule.scope, sources.ids AS campaign_ids, rule.updated_by, rule.updated_at
FROM axm_campaign_recent_rules rule
CROSS JOIN LATERAL (
    SELECT ARRAY(
        SELECT c.id FROM campaigns c
        WHERE c.id = ANY(rule.campaign_ids) AND c.type = 'regular' AND c.id <> rule.campaign_id
        ORDER BY c.id
    ) AS ids
) sources
WHERE rule.campaign_id = $1 AND (rule.scope <> 'campaigns' OR cardinality(sources.ids) > 0);

-- name: axm-upsert-recent-rule
INSERT INTO axm_campaign_recent_rules (campaign_id, days, scope, campaign_ids, updated_by)
VALUES ($1, $2, $3, $4, NULLIF($5, 0))
ON CONFLICT (campaign_id) DO UPDATE
SET days = EXCLUDED.days, scope = EXCLUDED.scope, campaign_ids = EXCLUDED.campaign_ids,
    updated_by = EXCLUDED.updated_by, updated_at = NOW()
RETURNING TRUE AS enabled, days, scope, campaign_ids, updated_by, updated_at;

-- name: axm-delete-recent-rule
DELETE FROM axm_campaign_recent_rules WHERE campaign_id = $1;

-- name: axm-recent-team-tags
SELECT DISTINCT tag
FROM lists l CROSS JOIN LATERAL unnest(l.tags) AS t(tag)
WHERE l.id = ANY($1) AND tag LIKE '팀:_%'
ORDER BY tag;

-- name: axm-get-recent-campaign
SELECT name, status, type FROM campaigns WHERE id = $1;

-- name: axm-recent-source-campaigns
SELECT id, type FROM campaigns WHERE id = ANY($1::INT[]) ORDER BY id;

-- name: axm-lock-recent-campaign
SELECT status, type FROM campaigns WHERE id = $1 FOR UPDATE;

-- name: axm-recent-campaign-lists
SELECT list_id FROM campaign_lists WHERE campaign_id = $1 AND list_id IS NOT NULL;

-- name: axm-recent-source-lists
SELECT l.id, l.name, l.tags, l.axm_preset
FROM unnest($1::INT[]) WITH ORDINALITY AS wanted(id, position)
JOIN lists l ON l.id = wanted.id
ORDER BY wanted.position;

-- name: axm-recent-add-members
INSERT INTO subscriber_lists (subscriber_id, list_id, status)
SELECT kept.subscriber_id, $6::INT, 'confirmed'
FROM (SELECT subscriber_id, excluded FROM axm_recent_exclusion($1, $2, $3, $4, $5) OFFSET 0) kept
WHERE NOT kept.excluded;
