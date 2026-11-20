-- name: axm-reaction-tracking
SELECT c.id, c.name, c.status, c.started_at,
    (SELECT COUNT(*) FROM axm_campaign_sends cs WHERE cs.campaign_id = c.id) AS sends,
    (SELECT COUNT(DISTINCT v.subscriber_id) FROM campaign_views v WHERE v.campaign_id = c.id) AS viewers,
    (SELECT COUNT(DISTINCT k.subscriber_id) FROM link_clicks k WHERE k.campaign_id = c.id) AS clickers,
    (SELECT MAX(cs.sent_at) FROM axm_campaign_sends cs WHERE cs.campaign_id = c.id) AS last_sent_at
FROM campaigns c WHERE c.id = $1;

-- name: axm-reaction-campaign-lists
SELECT l.tags, l.axm_preset FROM campaign_lists cl
JOIN lists l ON l.id = cl.list_id
WHERE cl.campaign_id = $1
ORDER BY cl.id;

-- name: axm-reaction-candidates
-- Prepended to counts, sample and add-members at startup. The CASE assigns each matching
-- recipient at most one exclusion reason. NULL permitted list IDs mean all readable lists.
WITH reaction_candidates AS (
    SELECT s.id, CASE
        WHEN s.status = 'blocklisted' THEN 1
        WHEN s.id IN (
            SELECT sl.subscriber_id FROM campaign_lists cl
            JOIN subscriber_lists sl ON sl.list_id = cl.list_id
            WHERE cl.campaign_id = $1 AND sl.status = 'unsubscribed'
        ) THEN 2
        WHEN NOT EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL AND sl.status <> 'unsubscribed'
        ) THEN 3
        WHEN NOT $5::BOOL AND NOT EXISTS (
            SELECT 1 FROM subscriber_lists sl
            WHERE sl.subscriber_id = s.id AND ($4::INT[] IS NULL OR sl.list_id = ANY($4::INT[]))
        ) THEN 4
        ELSE 0
    END AS reason
    FROM axm_campaign_sends cs
    JOIN subscribers s ON s.id = cs.subscriber_id
    WHERE cs.campaign_id = $1
        AND ($2::TEXT = 'any' OR ($2 = 'yes') = EXISTS (
            SELECT 1 FROM campaign_views v WHERE v.campaign_id = $1 AND v.subscriber_id = cs.subscriber_id
        ))
        AND ($3::TEXT = 'any' OR ($3 = 'yes') = EXISTS (
            SELECT 1 FROM link_clicks k WHERE k.campaign_id = $1 AND k.subscriber_id = cs.subscriber_id
        ))
)

-- name: axm-reaction-counts
-- Group by reason so eligibility is evaluated once per matched recipient.
SELECT COALESCE(SUM(n), 0)::BIGINT AS matched,
    COALESCE(MAX(n) FILTER (WHERE reason = 1), 0) AS blocklisted,
    COALESCE(MAX(n) FILTER (WHERE reason = 2), 0) AS campaign_opt_out,
    COALESCE(MAX(n) FILTER (WHERE reason = 3), 0) AS all_opt_out,
    COALESCE(MAX(n) FILTER (WHERE reason = 4), 0) AS out_of_scope,
    COALESCE(MAX(n) FILTER (WHERE reason = 0), 0) AS eligible
FROM (SELECT reason, COUNT(*) AS n FROM reaction_candidates GROUP BY reason) reasons;

-- name: axm-reaction-sample
SELECT s.name, s.email FROM (
    SELECT id FROM reaction_candidates WHERE reason = 0 ORDER BY id LIMIT 10
) sample JOIN subscribers s ON s.id = sample.id
ORDER BY sample.id;

-- name: axm-reaction-add-members
-- Plan the scan before applying the CASE result filter. Most recipients can qualify,
-- so pushing this test into the join leads to an underestimate and a nested loop.
INSERT INTO subscriber_lists (subscriber_id, list_id, status)
SELECT id, $6::INT, 'confirmed'
FROM (SELECT id, reason FROM reaction_candidates OFFSET 0) classified
WHERE reason = 0;
