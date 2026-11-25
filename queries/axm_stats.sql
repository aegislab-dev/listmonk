-- name: axm-stats-campaigns
-- Statistics dashboard (LST-64): one row per campaign that started in [$1, $2), read-only.
-- $3 is the team tag prefix. Unique viewers and clickers are counted as the campaign
-- analytics count them (a view or click without a subscriber counts as one person).
SELECT c.id, c.name, c.subject, c.status::TEXT AS status, c.started_at, c.sent, c.to_send,
    COALESCE((SELECT ARRAY_AGG(cl.list_id ORDER BY cl.list_id) FROM campaign_lists cl
        WHERE cl.campaign_id = c.id AND cl.list_id IS NOT NULL), '{}')::INT[] AS list_ids,
    COALESCE((SELECT ARRAY_AGG(cl.list_name ORDER BY cl.list_id) FROM campaign_lists cl
        WHERE cl.campaign_id = c.id AND cl.list_id IS NOT NULL), '{}')::TEXT[] AS list_names,
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
    (SELECT f.parent_campaign_id FROM axm_campaign_followups f WHERE f.campaign_id = c.id) AS followup_of
FROM campaigns c
WHERE c.type = 'regular' AND c.messenger = 'email' AND c.status IN ('finished', 'running', 'paused', 'cancelled')
    AND c.started_at >= $1 AND c.started_at < $2 AND (c.sent > 0 OR c.status = 'running')
ORDER BY c.started_at DESC, c.id DESC;

-- name: axm-stats-recipients
-- Per campaign and group (address age, or the country attribute) of the send records of the
-- finished campaigns that started in [$1, $2), tracking on, at least 3 days old.
-- $3 is 'age' or 'country'. Counts only, no addresses.
SELECT s.campaign_id, g.k AS key, COUNT(*)::INT AS sent, COUNT(s.opened_at)::INT AS opened,
    COUNT(s.clicked_at)::INT AS clicked,
    (COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM bounces b WHERE b.campaign_id = s.campaign_id
        AND b.subscriber_id = s.subscriber_id AND b.type IN ('hard', 'soft'))))::INT AS bounced
FROM axm_campaign_sends s
JOIN campaigns c ON c.id = s.campaign_id
JOIN subscribers u ON u.id = s.subscriber_id
CROSS JOIN LATERAL (SELECT CASE
    WHEN $3 = 'age' THEN (CASE WHEN s.sent_at - u.created_at < INTERVAL '7 days' THEN 'new7'
        WHEN s.sent_at - u.created_at < INTERVAL '30 days' THEN 'new30' ELSE 'older' END)
    ELSE COALESCE(NULLIF(UPPER(TRIM(CASE WHEN JSONB_TYPEOF(u.attribs) = 'object' THEN u.attribs ->> 'country' END)), ''), '(none)')
    END AS k) g
WHERE c.type = 'regular' AND c.messenger = 'email' AND c.status = 'finished'
    AND c.started_at >= $1 AND c.started_at < $2 AND c.started_at <= NOW() - INTERVAL '3 days'
    AND NOT EXISTS (SELECT 1 FROM axm_tracking_off o WHERE o.started_at <= c.started_at AND (o.ended_at IS NULL OR o.ended_at > c.started_at))
GROUP BY s.campaign_id, g.k;

-- name: axm-stats-teams
-- Every team role with the number of lists that carry its tag.
SELECT r.name, r.axm_team_tag AS tag,
    (SELECT COUNT(*) FROM lists l WHERE r.axm_team_tag = ANY(l.tags))::INT AS lists
FROM roles r
WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
ORDER BY r.name;
