-- Pre-send confirmation (LST-48), how many people a campaign would reach and who.
--
-- The eligibility rule below is COPIED from next-campaigns (the to_send count and the
-- dynamic counts) and from the batch queries next-campaign-subscribers and
-- next-campaign-subscribers-dynamic. If one of those changes, change these two the same way.
-- cmd/axm_preflight_db_test.go compares them with what a running campaign really reaches.
--
--   * An optin campaign reaches only 'unconfirmed' subscribers of double opt-in lists
--     (dynamic lists are not a target of an optin campaign).
--   * A regular campaign reaches only 'confirmed' subscribers of a double opt-in list and
--     everyone who is not 'unsubscribed' on any other list.
--   * A dynamic list gives the members of axm_dynamic_list_members (regular campaigns only).
--   * Blocklisted subscribers are always left out.

-- name: axm-preflight-audience
-- One row per list of campaign $1 with the people that list alone would reach (a deleted
-- list keeps its row, list_id NULL in campaign_lists, with 0). Every row also carries the
-- same two totals: the people across all lists, each counted once, and how many of them are
-- still to be sent after the checkpoint $2 (campaigns.last_subscriber_id) when a paused
-- campaign is resumed. A resumed campaign recounts its upper bound, so there is none here,
-- and a person above the checkpoint who already has a send record is skipped by the sender
-- (internal/manager/pipe.go, axm-get-campaign-sent-ids). With $2 = 0 nobody was sent yet.
-- The (list, person) pairs are built once and every count reads them: counting each list
-- and the total with separate queries doubled the time on a 400k list.
WITH c AS (
    SELECT id, type FROM campaigns WHERE id = $1
),
campLists AS (
    SELECT cl.list_id, COALESCE(l.name, cl.list_name) AS name, COALESCE(l.optin::TEXT, '') AS optin,
        COALESCE(l.axm_filter IS NOT NULL, false) AS is_dynamic, l.id IS NOT NULL AS live
    FROM c JOIN campaign_lists cl ON cl.campaign_id = c.id
    LEFT JOIN lists l ON l.id = cl.list_id
),
pairs AS (
    SELECT cl.list_id, s.id FROM c, campLists cl
    JOIN subscriber_lists sl ON sl.list_id = cl.list_id
    JOIN subscribers s ON s.id = sl.subscriber_id AND s.status != 'blocklisted'
    WHERE cl.live AND NOT cl.is_dynamic
        AND CASE
            WHEN c.type = 'optin' THEN sl.status = 'unconfirmed' AND cl.optin = 'double'
            WHEN cl.optin = 'double' THEN sl.status = 'confirmed'
            ELSE sl.status != 'unsubscribed'
        END
    UNION ALL
    SELECT cl.list_id, m.subscriber_id FROM c, campLists cl
    CROSS JOIN LATERAL axm_dynamic_list_members(cl.list_id) m
    WHERE cl.live AND cl.is_dynamic AND c.type != 'optin'
),
per AS (
    SELECT list_id, COUNT(DISTINCT id) AS n FROM pairs GROUP BY list_id
),
eligible AS (
    SELECT DISTINCT id FROM pairs
),
total AS (
    SELECT COUNT(*) AS n FROM eligible
)
SELECT cl.list_id AS id, cl.name, cl.optin, cl.is_dynamic AS dynamic, NOT cl.live AS deleted,
    COALESCE(per.n, 0) AS count,
    (SELECT n FROM total) AS total,
    (CASE WHEN $2 > 0 THEN (
        SELECT COUNT(*) FROM eligible e
        WHERE e.id > $2
            AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = e.id)
    ) ELSE (SELECT n FROM total) END) AS remaining,
    NOW() AS as_of
FROM campLists cl
LEFT JOIN per ON per.list_id = cl.list_id
ORDER BY cl.name, cl.list_id;
