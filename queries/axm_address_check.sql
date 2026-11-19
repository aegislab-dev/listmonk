-- name: axm-address-check-count
-- How many subscribers a campaign to these lists would reach (the same audience the
-- recent-send exclusion counts: blocklisted and unsubscribed are out, dynamic lists are in).
SELECT COUNT(*) FROM axm_campaign_audience($1);

-- name: axm-address-check-audience
-- direct: the subscriber has a subscription of their own in one of the target lists ($2:
-- the chosen ordinary lists the user may manage), so unsubscribing them there changes
-- something. Someone who is in only through a dynamic list or a read-only list has none.
SELECT s.id, s.email, EXISTS (
    SELECT 1 FROM subscriber_lists sl
    WHERE sl.subscriber_id = s.id AND sl.list_id = ANY($2::INT[]) AND sl.status != 'unsubscribed'
) AS direct
FROM axm_campaign_audience($1) a
JOIN subscribers s ON s.id = a.subscriber_id
ORDER BY s.id;

-- name: axm-address-check-count-optin
-- The audience of an opt-in campaign to these lists, as the sender reads it
-- (next-campaign-subscribers and next-campaign-subscribers-dynamic with type 'optin'): the
-- unconfirmed subscribers of the chosen double opt-in ordinary lists who are not
-- blocklisted. Dynamic lists send no opt-in mail.
SELECT COUNT(DISTINCT sl.subscriber_id)
FROM subscriber_lists sl
JOIN lists l ON l.id = sl.list_id AND l.axm_filter IS NULL AND l.optin = 'double'
JOIN subscribers s ON s.id = sl.subscriber_id
WHERE sl.list_id = ANY($1::INT[]) AND sl.status = 'unconfirmed' AND s.status != 'blocklisted';

-- name: axm-address-check-audience-optin
-- The same audience as axm-address-check-count-optin, with direct as in
-- axm-address-check-audience.
SELECT s.id, s.email, EXISTS (
    SELECT 1 FROM subscriber_lists t
    WHERE t.subscriber_id = s.id AND t.list_id = ANY($2::INT[]) AND t.status != 'unsubscribed'
) AS direct
FROM subscribers s
WHERE s.status != 'blocklisted' AND s.id IN (
    SELECT sl.subscriber_id FROM subscriber_lists sl
    JOIN lists l ON l.id = sl.list_id AND l.axm_filter IS NULL AND l.optin = 'double'
    WHERE sl.list_id = ANY($1::INT[]) AND sl.status = 'unconfirmed'
)
ORDER BY s.id;

-- name: axm-address-check-dynamic
SELECT id, axm_filter FROM lists WHERE id = ANY($1) AND axm_filter IS NOT NULL ORDER BY id;

-- name: axm-address-check-linked
-- The other lists an unsubscribe from the given ordinary lists can reach through draws
-- (axm_propagate_draw_unsub): the source of a drawn list, the lists drawn from a source, the
-- base lists of a dynamic source, and the lists drawn from a dynamic list over a base list.
WITH RECURSIVE reach(id) AS (
    SELECT unnest($1::INT[])
    UNION
    SELECT e.id FROM reach r CROSS JOIN LATERAL (
        SELECT d.source_list_id AS id FROM axm_list_draws d WHERE d.list_id = r.id AND d.source_list_id IS NOT NULL
        UNION ALL
        SELECT d.list_id FROM axm_list_draws d WHERE d.source_list_id = r.id AND d.list_id IS NOT NULL
        UNION ALL
        SELECT l.id FROM lists src JOIN lists l ON l.axm_filter IS NULL
            AND (src.axm_filter->'base'->>'type' = 'all'
                OR COALESCE(src.axm_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id))
        WHERE src.id = r.id AND src.axm_filter IS NOT NULL
        UNION ALL
        SELECT d.list_id FROM axm_list_draws d JOIN lists src ON src.id = d.source_list_id
        WHERE d.list_id IS NOT NULL AND src.axm_filter IS NOT NULL
            AND (src.axm_filter->'base'->>'type' = 'all'
                OR COALESCE(src.axm_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(r.id))
    ) e
)
SELECT l.id, l.name FROM reach JOIN lists l ON l.id = reach.id
WHERE l.axm_filter IS NULL AND NOT (l.id = ANY($1::INT[]))
ORDER BY l.name, l.id;
