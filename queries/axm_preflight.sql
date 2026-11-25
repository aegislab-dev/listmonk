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
--   * (LST-65) A regular campaign with a recent-send rule leaves out, as the sender does in
--     NextSubscribers, the people axm_recent_received names and the people it kept out before
--     (axm_campaign_recent_holds, while the rule is on). The count is `recent`; the remaining
--     count also leaves them out.

-- name: axm-preflight-audience
-- One row per list of campaign $1 with the people that list alone would reach (a deleted
-- list keeps its row, list_id NULL in campaign_lists, with 0). Every row also carries the
-- same totals for all lists together.
--   total      the people reached, each counted once
--   multi      how many of them are on more than one of the lists
--   excluded   people on the lists of a regular campaign who are not reached (blocklisted,
--              unsubscribed, not yet confirmed on a double opt-in list)
--   recent     (LST-65) of total, how many the campaign's recent-send rule leaves out of the
--              send: the people with a send record in the rule's scope within its days, and
--              the people it left out already. 0 without a rule.
--   remaining  with $3 (resume) true, how many of total are still to be sent after the
--              checkpoint $2 (campaigns.last_subscriber_id). A resumed campaign recounts its
--              upper bound, so there is none here, and a person above the checkpoint who
--              already has a send record is skipped by the sender (internal/manager/pipe.go,
--              axm-get-campaign-sent-ids, and for a follow-up its decisions marked sent), also
--              when the checkpoint is 0. Otherwise total.
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
),
held AS (
    SELECT subscriber_id AS id FROM axm_campaign_recent_holds
    WHERE campaign_id = $1 AND axm_recent_rule_on($1)
),
fresh AS (
    SELECT r.subscriber_id AS id FROM axm_recent_received($1, ARRAY(SELECT id FROM eligible)) r
    WHERE NOT EXISTS (SELECT 1 FROM held h WHERE h.id = r.subscriber_id)
),
recent_out AS (
    SELECT id FROM fresh
    UNION ALL
    SELECT id FROM held WHERE id IN (SELECT id FROM eligible)
)
SELECT cl.list_id AS id, cl.name, cl.optin, cl.is_dynamic AS dynamic, NOT cl.live AS deleted,
    COALESCE(per.n, 0) AS count,
    (SELECT n FROM total) AS total,
    (SELECT multi FROM total) AS multi,
    (SELECT COUNT(*) FROM recent_out) AS recent,
    (CASE WHEN $3::BOOLEAN THEN (SELECT COUNT(*) FROM fresh WHERE id > $2) ELSE 0 END) AS recent_fresh,
    (CASE WHEN (SELECT type FROM c) = 'regular' THEN (
        SELECT COUNT(*) FROM (SELECT DISTINCT id FROM memb) x
        WHERE NOT EXISTS (SELECT 1 FROM eligible e WHERE e.id = x.id)
    ) ELSE 0 END) AS excluded,
    (CASE WHEN $3::BOOLEAN THEN (
        SELECT COUNT(*) FROM eligible e
        WHERE e.id > $2
            AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = e.id)
            -- AXM (LST-47) a follow-up also skips whom its decisions say it was sent to (kept
            -- when the send record expired), as the sender does (axm-followup-sent-ids).
            AND NOT EXISTS (SELECT 1 FROM axm_campaign_followup_claims k WHERE k.campaign_id = $1 AND k.subscriber_id = e.id AND k.sent)
            AND NOT EXISTS (SELECT 1 FROM recent_out ro WHERE ro.id = e.id)
    ) ELSE (SELECT n FROM total) END) AS remaining,
    NOW() AS as_of
FROM campLists cl
LEFT JOIN per ON per.list_id = cl.list_id
ORDER BY cl.name, cl.list_id;

-- name: axm-preflight-sample
-- Up to $2 of the people campaign $1 would reach, the lowest IDs first, by the same rule as
-- axm-preflight-audience (keep the two the same). The pre-send check renders the mail for them.
-- With $4 (resume) true, only those still to be sent: after the checkpoint $3 and without a send
-- record of this campaign, as the remaining count of axm-preflight-audience. (LST-65) Without
-- the people the campaign's recent-send rule leaves out (the same rule as `recent` above).
WITH c AS (
    SELECT id, type FROM campaigns WHERE id = $1
),
campLists AS (
    SELECT cl.list_id, COALESCE(l.optin::TEXT, '') AS optin,
        COALESCE(l.axm_filter IS NOT NULL, false) AS is_dynamic, l.id IS NOT NULL AS live
    FROM c JOIN campaign_lists cl ON cl.campaign_id = c.id
    LEFT JOIN lists l ON l.id = cl.list_id
),
memb AS (
    SELECT s.id, s.status = 'blocklisted' AS blocked, sl.status AS sub_status, cl.optin
    FROM campLists cl
    JOIN subscriber_lists sl ON sl.list_id = cl.list_id
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE cl.live AND NOT cl.is_dynamic
),
eligible AS (
    SELECT m.id FROM c, memb m
    WHERE NOT m.blocked
        AND CASE
            WHEN c.type = 'optin' THEN m.sub_status = 'unconfirmed' AND m.optin = 'double'
            WHEN m.optin = 'double' THEN m.sub_status = 'confirmed'
            ELSE m.sub_status != 'unsubscribed'
        END
    UNION
    SELECT m.subscriber_id FROM c, campLists cl
    CROSS JOIN LATERAL axm_dynamic_list_members(cl.list_id) m
    WHERE cl.live AND cl.is_dynamic AND c.type != 'optin'
)
SELECT s.* FROM subscribers s WHERE s.id IN (SELECT id FROM eligible)
    AND (NOT $4::BOOLEAN OR (s.id > $3
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = s.id)))
    AND NOT EXISTS (SELECT 1 FROM axm_recent_received($1, ARRAY[s.id]))
    AND NOT EXISTS (
        SELECT 1 FROM axm_campaign_recent_holds h
        WHERE h.campaign_id = $1 AND h.subscriber_id = s.id
            AND axm_recent_rule_on($1))
ORDER BY s.id LIMIT $2;
