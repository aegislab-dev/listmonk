-- name: axm-get-dynamic-list-ids
SELECT id FROM lists WHERE id = ANY($1::INT[]) AND axm_filter IS NOT NULL;

-- name: axm-dynamic-list-counts
SELECT l.id, (SELECT COUNT(*) FROM axm_dynamic_list_members(l.id)) AS count
FROM lists l WHERE l.id = ANY($1::INT[]) AND l.axm_filter IS NOT NULL;

-- name: axm-preview-dynamic-filter
SELECT COUNT(*) FROM axm_filter_members($1::JSONB, $2::INT);

-- name: next-campaign-subscribers-dynamic
WITH campLists AS (
    SELECT l.id AS list_id, l.optin, l.axm_filter FROM lists l
    JOIN campaign_lists cl ON cl.list_id = l.id WHERE cl.campaign_id = $1
),
regular AS (
    SELECT DISTINCT s.id FROM subscriber_lists sl
    JOIN campLists cl ON cl.list_id = sl.list_id AND cl.axm_filter IS NULL
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE sl.list_id = ANY($5::INT[]) AND s.id > $3 AND s.id <= $4
        AND s.status != 'blocklisted'
        AND CASE WHEN $2 = 'optin' THEN sl.status = 'unconfirmed' AND cl.optin = 'double'
            WHEN cl.optin = 'double' THEN sl.status = 'confirmed'
            ELSE sl.status != 'unsubscribed' END
    ORDER BY s.id LIMIT $6
),
dynamic AS (
    SELECT m.subscriber_id AS id FROM campLists cl
    CROSS JOIN LATERAL axm_dynamic_list_members(cl.list_id, $3::INT, $4::INT, $6::INT) m
    WHERE cl.axm_filter IS NOT NULL AND $2 != 'optin' AND cl.list_id = ANY($5::INT[])
),
ids AS (SELECT id FROM regular UNION SELECT id FROM dynamic ORDER BY id LIMIT $6),
subs AS (SELECT s.* FROM subscribers s JOIN ids ON ids.id = s.id ORDER BY s.id),
u AS (
    UPDATE campaigns SET last_subscriber_id = (SELECT MAX(id) FROM subs), updated_at = NOW()
    WHERE (SELECT COUNT(id) FROM subs) > 0 AND id = $1
)
SELECT * FROM subs ORDER BY id;
