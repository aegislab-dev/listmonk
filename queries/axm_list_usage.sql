-- Which campaigns used a list (LST-63). Read-only; the import screen warns about a list that
-- already went out and the list table shows where each one was used.

-- name: axm-get-list-campaigns
-- Campaigns that sent to, are sending to, or are scheduled to send to the lists $1: a draft
-- or a campaign that never sent anything is not a use. At most $2 per list, newest first;
-- total is the list's full count. lists_n is how many lists the campaign targets (sent is
-- the whole campaign's count, not this list's share, when it is more than one).
WITH used AS (
    SELECT cl.list_id, c.id AS campaign_id, c.name, c.status::TEXT AS status, c.sent, c.to_send,
        c.started_at, c.send_at, c.updated_at,
        (SELECT COUNT(*) FROM campaign_lists x WHERE x.campaign_id = c.id)::INT AS lists_n,
        COALESCE(c.started_at, c.send_at, c.created_at) AS used_at
    FROM campaign_lists cl
    JOIN campaigns c ON c.id = cl.campaign_id
    WHERE cl.list_id = ANY($1::INT[])
        AND (c.status IN ('scheduled', 'running')
            OR (c.status IN ('paused', 'finished', 'cancelled') AND c.sent > 0))
), ranked AS (
    SELECT used.*,
        ROW_NUMBER() OVER (PARTITION BY list_id ORDER BY used_at DESC, campaign_id DESC) AS rn,
        (COUNT(*) OVER (PARTITION BY list_id))::INT AS total
    FROM used
)
SELECT list_id, campaign_id, name, status, sent, to_send, started_at, send_at, updated_at, lists_n, total
FROM ranked WHERE rn <= $2::INT
ORDER BY list_id, rn;
