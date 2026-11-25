-- name: axm-stats-campaigns
-- Statistics dashboard (LST-64): one row per campaign that started in [$1, $2), read-only.
-- $3 and $4 are the team and source tag prefixes. Unique viewers and clickers are counted as the campaign
-- analytics count them (a view or click without a subscriber counts as one person).
SELECT c.id, c.name, c.subject, c.status::TEXT AS status, c.started_at, c.sent, c.to_send,
    COALESCE((SELECT ARRAY_AGG(cl.list_id ORDER BY cl.list_id) FROM campaign_lists cl
        WHERE cl.campaign_id = c.id AND cl.list_id IS NOT NULL), '{}')::INT[] AS list_ids,
    COALESCE((SELECT ARRAY_AGG(cl.list_name ORDER BY cl.list_id) FROM campaign_lists cl
        WHERE cl.campaign_id = c.id AND cl.list_id IS NOT NULL), '{}')::TEXT[] AS list_names,
    COALESCE((SELECT ARRAY_AGG(COALESCE((SELECT MIN(NULLIF(TRIM(SUBSTRING(t FROM CHAR_LENGTH($4) + 1)), ''))
            FROM UNNEST(l.tags) t WHERE STARTS_WITH(t, $4)), '') ORDER BY cl.list_id)
        FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id WHERE cl.campaign_id = c.id), '{}')::TEXT[] AS list_sources,
    COALESCE((SELECT ARRAY_AGG((TO_JSON(l.created_at) #>> '{}') ORDER BY cl.list_id)
        FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id WHERE cl.campaign_id = c.id), '{}')::TEXT[] AS list_created_at,
    (SELECT COUNT(*) FROM campaign_lists cl WHERE cl.campaign_id = c.id)::INT AS lists_n,
    COALESCE((SELECT ARRAY_AGG(DISTINCT t ORDER BY t) FROM campaign_lists cl
        JOIN lists l ON l.id = cl.list_id CROSS JOIN LATERAL UNNEST(l.tags) AS t
        WHERE cl.campaign_id = c.id AND STARTS_WITH(t, $3)), '{}')::TEXT[] AS team_tags,
    (SELECT COUNT(DISTINCT v.subscriber_id) + (CASE WHEN BOOL_OR(v.subscriber_id IS NULL) THEN 1 ELSE 0 END)
        FROM campaign_views v WHERE v.campaign_id = c.id)::INT AS viewers,
    (SELECT COUNT(DISTINCT k.subscriber_id) + (CASE WHEN BOOL_OR(k.subscriber_id IS NULL) THEN 1 ELSE 0 END)
        FROM link_clicks k WHERE k.campaign_id = c.id)::INT AS clickers,
    (SELECT COUNT(*) FROM bounces b WHERE b.campaign_id = c.id AND b.type = 'hard')::INT AS hard,
    (SELECT COUNT(*) FROM bounces b WHERE b.campaign_id = c.id AND b.type = 'soft')::INT AS soft,
    (SELECT COUNT(*) FROM bounces b WHERE b.campaign_id = c.id AND b.type = 'complaint')::INT AS complaint,
    (SELECT COUNT(*) FROM axm_campaign_sends s WHERE s.campaign_id = c.id)::INT AS ledger_rows,
    (SELECT COUNT(*) FROM axm_campaign_sends s WHERE s.campaign_id = c.id AND EXISTS (
        SELECT 1 FROM axm_campaign_sends p WHERE p.subscriber_id = s.subscriber_id AND p.campaign_id <> s.campaign_id
            AND p.sent_at < s.sent_at AND p.sent_at >= s.sent_at - INTERVAL '30 days'))::INT AS overlap30,
    EXISTS (SELECT 1 FROM axm_tracking_off o WHERE o.started_at <= COALESCE(c.started_at, c.created_at)
        AND (o.ended_at IS NULL OR o.ended_at > COALESCE(c.started_at, c.created_at))) AS tracking_off,
    f.parent_campaign_id AS followup_of,
    CASE WHEN f.campaign_id IS NOT NULL THEN
        JSONB_BUILD_OBJECT('open', f.condition->>'open', 'click', f.condition->>'click')
    END AS followup_condition,
    parent.sent AS followup_parent_sent,
    CASE WHEN c.status = 'finished' AND parent.status = 'finished' THEN
        (SELECT COUNT(DISTINCT v.subscriber_id) + CASE WHEN BOOL_OR(v.subscriber_id IS NULL) THEN 1 ELSE 0 END
         FROM campaign_views v WHERE v.campaign_id IN (c.id, parent.id))::INT
    END AS combined_viewers,
    CASE WHEN c.status = 'finished' AND parent.status = 'finished' THEN
        (SELECT COUNT(DISTINCT k.subscriber_id) + CASE WHEN BOOL_OR(k.subscriber_id IS NULL) THEN 1 ELSE 0 END
         FROM link_clicks k WHERE k.campaign_id IN (c.id, parent.id))::INT
    END AS combined_clickers
FROM campaigns c
LEFT JOIN axm_campaign_followups f ON f.campaign_id = c.id
LEFT JOIN campaigns parent ON parent.id = f.parent_campaign_id
WHERE c.type = 'regular' AND c.messenger = 'email' AND c.status IN ('finished', 'running', 'paused', 'cancelled')
    AND c.started_at >= $1 AND c.started_at < $2 AND (c.sent > 0 OR c.status = 'running')
ORDER BY c.started_at DESC, c.id DESC;

-- name: axm-stats-recipients
-- Scope each send to the company and each campaign team (including no team).
-- Count distinct people per scope and group before replacing small group labels.
WITH campaign_teams AS (
    SELECT c.id, ARRAY(
        SELECT DISTINCT t FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
        CROSS JOIN LATERAL UNNEST(l.tags) AS t
        WHERE cl.campaign_id = c.id AND STARTS_WITH(t, $4)
    ) AS tags
    FROM campaigns c
    WHERE c.type = 'regular' AND c.messenger = 'email' AND c.status = 'finished'
        AND c.started_at >= $1 AND c.started_at < $2 AND c.started_at <= NOW() - INTERVAL '3 days'
        AND NOT EXISTS (SELECT 1 FROM axm_tracking_off o WHERE o.started_at <= c.started_at AND (o.ended_at IS NULL OR o.ended_at > c.started_at))
), sends AS MATERIALIZED (
    SELECT t.team, s.subscriber_id, s.opened_at, s.clicked_at,
        EXISTS (SELECT 1 FROM bounces b WHERE b.campaign_id = s.campaign_id
            AND b.subscriber_id = s.subscriber_id AND b.type IN ('hard', 'soft')) AS bounced,
        CASE
            WHEN $3 = 'age' THEN CASE
                WHEN s.sent_at - u.created_at < INTERVAL '7 days' THEN 'new7'
                WHEN s.sent_at - u.created_at < INTERVAL '30 days' THEN 'new30'
                ELSE 'older' END
            ELSE COALESCE(NULLIF(UPPER(TRIM(u.attribs->>'country')), ''), '(none)')
        END AS key
    FROM campaign_teams c
    JOIN axm_campaign_sends s ON s.campaign_id = c.id
    JOIN subscribers u ON u.id = s.subscriber_id
    CROSS JOIN LATERAL UNNEST(ARRAY[''] || CASE WHEN CARDINALITY(c.tags) = 0 THEN ARRAY[$6::TEXT] ELSE c.tags END) AS t(team)
), public_keys AS (
    SELECT team, key FROM sends GROUP BY team, key HAVING COUNT(DISTINCT subscriber_id) >= $5
)
SELECT s.team, COALESCE(p.key, $7) AS key, COUNT(*)::INT AS sent,
    COUNT(DISTINCT s.subscriber_id)::INT AS recipients, COUNT(s.opened_at)::INT AS opened,
    COUNT(s.clicked_at)::INT AS clicked, (COUNT(*) FILTER (WHERE s.bounced))::INT AS bounced
FROM sends s LEFT JOIN public_keys p ON p.team = s.team AND p.key = s.key
GROUP BY s.team, COALESCE(p.key, $7)
ORDER BY s.team, sent DESC, key;

-- name: axm-stats-teams
-- Every team role with the number of lists that carry its tag.
SELECT r.name, r.axm_team_tag AS tag,
    (SELECT COUNT(*) FROM lists l WHERE r.axm_team_tag = ANY(l.tags))::INT AS lists
FROM roles r
WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
ORDER BY r.name;
