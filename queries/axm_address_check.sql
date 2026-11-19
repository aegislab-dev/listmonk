-- name: axm-address-check-count
-- How many subscribers a campaign to these lists would reach (the same audience the
-- recent-send exclusion counts: blocklisted and unsubscribed are out, dynamic lists are in).
SELECT COUNT(*) FROM axm_campaign_audience($1);

-- name: axm-address-check-audience
-- direct: the subscriber has a subscription of their own in one of the ordinary lists, so
-- unsubscribing them there takes them out. Someone who is in only through a dynamic list
-- has none.
SELECT s.id, s.email, EXISTS (
    SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
    WHERE sl.subscriber_id = s.id AND sl.list_id = ANY($1) AND l.axm_filter IS NULL AND sl.status != 'unsubscribed'
) AS direct
FROM axm_campaign_audience($1) a
JOIN subscribers s ON s.id = a.subscriber_id
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
