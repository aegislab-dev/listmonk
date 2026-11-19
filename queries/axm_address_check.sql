-- name: axm-address-check-count
-- How many subscribers a campaign to these lists would reach (the same audience the
-- recent-send exclusion counts: blocklisted and unsubscribed are out, dynamic lists are in).
SELECT COUNT(*) FROM axm_campaign_audience($1);

-- name: axm-address-check-audience
SELECT s.id, s.email
FROM axm_campaign_audience($1) a
JOIN subscribers s ON s.id = a.subscriber_id
ORDER BY s.id;
