-- name: axm-recent-exclude-counts
-- Count the same set that axm-recent-add-members uses: existing holds and recent recipients
-- who have not already received this campaign.
SELECT COUNT(*) AS target,
    COUNT(*) FILTER (WHERE h.subscriber_id IS NOT NULL OR (e.excluded AND own.subscriber_id IS NULL)) AS excluded,
    NOW() AS as_of
FROM axm_recent_exclusion($1, $2, $3, $4, $5) e
LEFT JOIN axm_campaign_recent_holds h ON h.campaign_id = $5 AND h.subscriber_id = e.subscriber_id
LEFT JOIN axm_campaign_sends own ON own.campaign_id = $5 AND own.subscriber_id = e.subscriber_id;

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
SELECT name, status, type, started_at FROM campaigns WHERE id = $1;

-- name: axm-recent-source-campaigns
SELECT id, type FROM campaigns WHERE id = ANY($1::INT[]) ORDER BY id;

-- name: axm-lock-recent-campaign
SELECT status, type, started_at IS NOT NULL AS started FROM campaigns WHERE id = $1 FOR UPDATE;

-- name: axm-recent-campaign-lists
SELECT list_id FROM campaign_lists WHERE campaign_id = $1 AND list_id IS NOT NULL;

-- name: axm-recent-source-lists
SELECT l.id, l.name, l.tags, l.axm_preset
FROM unnest($1::INT[]) WITH ORDINALITY AS wanted(id, position)
JOIN lists l ON l.id = wanted.id
ORDER BY wanted.position;

-- name: axm-recent-add-members
-- Insert the same retained audience that axm-recent-exclude-counts counts.
INSERT INTO subscriber_lists (subscriber_id, list_id, status)
SELECT e.subscriber_id, $6::INT, 'confirmed'
FROM (SELECT subscriber_id, excluded FROM axm_recent_exclusion($1, $2, $3, $4, $5) OFFSET 0) e
LEFT JOIN axm_campaign_recent_holds h ON h.campaign_id = $5 AND h.subscriber_id = e.subscriber_id
LEFT JOIN axm_campaign_sends own ON own.campaign_id = $5 AND own.subscriber_id = e.subscriber_id
WHERE h.subscriber_id IS NULL AND (NOT e.excluded OR own.subscriber_id IS NOT NULL);

-- name: axm-recent-readable-lists
-- The lists of $2 that user $1 may read now, by the role tables (the same rule as the session's
-- permission map): the super admin role, lists:get_all or lists:manage_all on the user role, or
-- list:get or list:manage for that list on the user's list role.
SELECT l.id
FROM users u
JOIN lists l ON l.id = ANY($2::INT[])
LEFT JOIN roles ur ON ur.id = u.user_role_id
WHERE u.id = $1 AND (
    u.user_role_id = 1
    OR ur.permissions && ARRAY['lists:get_all', 'lists:manage_all']
    OR EXISTS (
        SELECT 1 FROM roles r
        WHERE r.parent_id = u.list_role_id AND r.type = 'list' AND r.list_id = l.id
            AND r.permissions && ARRAY['list:get', 'list:manage']
    )
);

-- name: axm-touch-recent-campaign
-- A rule change is a change of the campaign: a send window opened before it is stale (409).
UPDATE campaigns SET updated_at = NOW() WHERE id = $1;

-- name: axm-recent-hold-batch
-- (LST-65) Who of the batch $2 the recent-send rule of campaign $1 leaves out: the people it leaves
-- out now (and keeps from now on in axm_campaign_recent_holds) and the people it left out before,
-- while the rule is on. Nothing for a campaign without a rule.
WITH on_rule AS (
    SELECT 1 WHERE axm_recent_rule_on($1)
), fresh AS (
    SELECT subscriber_id FROM axm_recent_received($1, $2::INT[])
), keep AS (
    INSERT INTO axm_campaign_recent_holds (campaign_id, subscriber_id)
    SELECT $1, subscriber_id FROM fresh
    ON CONFLICT DO NOTHING
)
SELECT subscriber_id FROM fresh
UNION
SELECT h.subscriber_id FROM axm_campaign_recent_holds h
WHERE h.campaign_id = $1 AND h.subscriber_id = ANY($2::INT[]) AND EXISTS (SELECT 1 FROM on_rule);

-- name: axm-recent-has-rule
SELECT axm_recent_rule_on($1);

-- name: axm-recent-held-count
-- A finished campaign keeps its count even when a subscriber and their holds are deleted.
SELECT COALESCE(
    (SELECT r.finished_held_count FROM axm_campaign_recent_rules r
     JOIN campaigns c ON c.id = r.campaign_id AND c.status = 'finished'
     WHERE r.campaign_id = $1),
    (SELECT COUNT(*) FROM axm_campaign_recent_holds WHERE campaign_id = $1)
);
