-- name: axm-get-dynamic-list-ids
SELECT id FROM lists WHERE id = ANY($1::INT[]) AND axm_filter IS NOT NULL;

-- name: axm-dynamic-list-counts
SELECT l.id, (SELECT COUNT(*) FROM axm_dynamic_list_members(l.id)) AS count
FROM lists l WHERE l.id = ANY($1::INT[]) AND l.axm_filter IS NOT NULL;

-- name: axm-preview-dynamic-filter
SELECT COUNT(*) FROM axm_filter_members($1::JSONB, $2::INT);

-- name: axm-preview-dynamic-sample
-- The first members by ID, read through the same function as every other member query.
SELECT s.id, s.email, s.name, s.attribs FROM axm_filter_members($1::JSONB, $2::INT, 0, 2147483647, $3::INT) m
JOIN subscribers s ON s.id = m.subscriber_id ORDER BY s.id;

-- name: axm-preview-dynamic-excluded
-- Explains who the conditions match but the list leaves out. It counts, in this order,
-- blocklisted subscribers, those with no valid subscription to a base list (unsubscribed, or
-- unconfirmed on a double opt-in list), and those who unsubscribed from this dynamic list.
-- The three add up to the matching subscribers of the base minus axm_filter_members().
WITH c AS (
    SELECT s.status = 'blocklisted' AS blocked,
        EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL
                AND ($1::JSONB->'base'->>'type' = 'all' OR
                    ($1::JSONB->'base'->>'type' = 'lists' AND
                        COALESCE($1::JSONB->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
                AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                    ELSE sl.status != 'unsubscribed' END
        ) AS valid,
        $2::INT IS NOT NULL AND EXISTS (
            SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
                AND ex.list_id = $2::INT AND ex.status = 'unsubscribed'
        ) AS opted_out
    FROM subscribers s
    WHERE axm_filter_match(s.attribs, $1::JSONB)
        AND EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL
                AND ($1::JSONB->'base'->>'type' = 'all' OR
                    ($1::JSONB->'base'->>'type' = 'lists' AND
                        COALESCE($1::JSONB->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
        )
)
SELECT COUNT(*) FILTER (WHERE blocked) AS blocklisted,
    COUNT(*) FILTER (WHERE NOT blocked AND NOT valid) AS unsubscribed,
    COUNT(*) FILTER (WHERE NOT blocked AND valid AND opted_out) AS opted_out
FROM c;

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
