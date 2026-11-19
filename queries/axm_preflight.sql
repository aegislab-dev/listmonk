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
-- same totals for all lists together.
--   total      the people reached, each counted once
--   multi      how many of them are on more than one of the lists
--   excluded   people on the lists of a regular campaign who are not reached (blocklisted,
--              unsubscribed, not yet confirmed on a double opt-in list)
--   remaining  with $3 (resume) true, how many of total are still to be sent after the
--              checkpoint $2 (campaigns.last_subscriber_id). A resumed campaign recounts its
--              upper bound, so there is none here, and a person above the checkpoint who
--              already has a send record is skipped by the sender (internal/manager/pipe.go,
--              axm-get-campaign-sent-ids), also when the checkpoint is 0. Otherwise total.
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
memb AS (
    SELECT cl.list_id, s.id, s.status = 'blocklisted' AS blocked, sl.status AS sub_status, cl.optin
    FROM campLists cl
    JOIN subscriber_lists sl ON sl.list_id = cl.list_id
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE cl.live AND NOT cl.is_dynamic
),
pairs AS (
    SELECT m.list_id, m.id FROM c, memb m
    WHERE NOT m.blocked
        AND CASE
            WHEN c.type = 'optin' THEN m.sub_status = 'unconfirmed' AND m.optin = 'double'
            WHEN m.optin = 'double' THEN m.sub_status = 'confirmed'
            ELSE m.sub_status != 'unsubscribed'
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
    SELECT id, COUNT(DISTINCT list_id) AS lists FROM pairs GROUP BY id
),
total AS (
    SELECT COUNT(*) AS n, COUNT(*) FILTER (WHERE lists > 1) AS multi FROM eligible
)
SELECT cl.list_id AS id, cl.name, cl.optin, cl.is_dynamic AS dynamic, NOT cl.live AS deleted,
    COALESCE(per.n, 0) AS count,
    (SELECT n FROM total) AS total,
    (SELECT multi FROM total) AS multi,
    (CASE WHEN (SELECT type FROM c) = 'regular' THEN (
        SELECT COUNT(*) FROM (SELECT DISTINCT id FROM memb) x
        WHERE NOT EXISTS (SELECT 1 FROM eligible e WHERE e.id = x.id)
    ) ELSE 0 END) AS excluded,
    (CASE WHEN $3::BOOLEAN THEN (
        SELECT COUNT(*) FROM eligible e
        WHERE e.id > $2
            AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = e.id)
    ) ELSE (SELECT n FROM total) END) AS remaining,
    NOW() AS as_of
FROM campLists cl
LEFT JOIN per ON per.list_id = cl.list_id
ORDER BY cl.name, cl.list_id;
