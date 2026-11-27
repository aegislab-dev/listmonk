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
-- A list contributes at most one row per subscriber: subscriber_lists has a primary key
-- on (subscriber_id, list_id), and dynamic lists select subscribers by primary key.
-- Group the same stream by list and by subscriber so per-list counts, overlap and exclusions
-- do not need separate DISTINCT passes or a join between eligible and excluded members.
WITH c AS (
    SELECT id, type FROM campaigns WHERE id = $1
),
campLists AS (
    SELECT cl.list_id, COALESCE(l.name, cl.list_name) AS name, l.optin,
        COALESCE(l.axm_filter IS NOT NULL, false) AS is_dynamic, l.id IS NOT NULL AS live
    FROM c JOIN campaign_lists cl ON cl.campaign_id = c.id
    LEFT JOIN lists l ON l.id = cl.list_id
),
members AS (
    SELECT cl.list_id, s.id, s.status != 'blocklisted' AND CASE
        WHEN c.type = 'optin' THEN sl.status = 'unconfirmed' AND cl.optin = 'double'
        WHEN cl.optin = 'double' THEN sl.status = 'confirmed'
        ELSE sl.status != 'unsubscribed'
    END AS eligible
    FROM c, campLists cl
    JOIN subscriber_lists sl ON sl.list_id = cl.list_id
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE cl.live AND NOT cl.is_dynamic
    UNION ALL
    SELECT cl.list_id, m.subscriber_id, TRUE FROM c, campLists cl
    CROSS JOIN LATERAL axm_dynamic_list_members(cl.list_id) m
    WHERE cl.live AND cl.is_dynamic AND c.type != 'optin'
),
counts AS (
    SELECT list_id, id, COUNT(*) FILTER (WHERE eligible) AS n
    FROM members
    GROUP BY GROUPING SETS ((list_id), (id))
),
totals AS (
    SELECT COUNT(*) FILTER (WHERE n > 0) AS n,
        COUNT(*) FILTER (WHERE n > 1) AS multi,
        COUNT(*) FILTER (WHERE n = 0) AS excluded,
        COUNT(*) FILTER (WHERE n > 0 AND id > $2) AS after_checkpoint
    FROM counts WHERE id IS NOT NULL
),
recent_rule AS MATERIALIZED (
    SELECT axm_recent_rule_on($1) AS enabled
),
held AS (
    SELECT subscriber_id AS id FROM axm_campaign_recent_holds
    WHERE campaign_id = $1
),
fresh AS (
    SELECT r.subscriber_id AS id
    FROM recent_rule
    CROSS JOIN LATERAL axm_recent_received($1, ARRAY(SELECT id FROM counts WHERE id IS NOT NULL AND n > 0)) r
    WHERE recent_rule.enabled
        AND NOT EXISTS (SELECT 1 FROM held h WHERE h.id = r.subscriber_id)
),
recent_out AS (
    SELECT id, FALSE AS held FROM fresh
    UNION ALL
    SELECT h.id, TRUE AS held FROM held h JOIN counts e ON e.id = h.id WHERE e.n > 0
),
pending_exclusions AS (
    SELECT subscriber_id AS id FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id > $2
    UNION
    SELECT subscriber_id FROM axm_campaign_followup_claims WHERE campaign_id = $1 AND subscriber_id > $2 AND sent
    UNION
    SELECT id FROM recent_out WHERE id > $2
)
SELECT cl.list_id AS id, cl.name, COALESCE(cl.optin::TEXT, '') AS optin, cl.is_dynamic AS dynamic, NOT cl.live AS deleted,
    COALESCE(per.n, 0) AS count,
    totals.n AS total,
    totals.multi,
    (SELECT COUNT(*) FROM recent_out) AS recent,
    (SELECT COUNT(*) FROM recent_out WHERE held) AS recent_held_in_audience,
    (CASE WHEN $3::BOOLEAN THEN (SELECT COUNT(*) FROM fresh WHERE id > $2) ELSE (SELECT COUNT(*) FROM fresh) END) AS recent_fresh,
    CASE WHEN c.type = 'regular' THEN totals.excluded ELSE 0 END AS excluded,
    CASE WHEN $3::BOOLEAN THEN totals.after_checkpoint - (
        -- Count a past delivery or a recent exclusion only once, even if several records match.
        SELECT COUNT(*) FROM pending_exclusions x JOIN counts e ON e.id = x.id WHERE e.n > 0
    ) ELSE totals.n END AS remaining,
    NOW() AS as_of
FROM c CROSS JOIN totals CROSS JOIN campLists cl
LEFT JOIN counts per ON per.list_id = cl.list_id
ORDER BY cl.name, cl.list_id;

-- name: axm-preflight-has-recipient
-- For a regular campaign during a scheduled save, only ask if the edited lists
-- include anyone it can still send to. $2 is its checkpoint, $3 its active
-- follow-up list (0 otherwise), and $4 is individual tracking.
-- Use the same eligibility and exclusions as the remaining count above and
-- axm-followup-pending. Duplicates across lists do not change EXISTS.
WITH live_lists AS MATERIALIZED (
    SELECT l.id, l.optin, l.axm_filter IS NOT NULL AS dynamic
    FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
    WHERE cl.campaign_id = $1
),
candidates AS (
    SELECT l.id AS list_id, s.id AS subscriber_id
    FROM live_lists l
    JOIN subscriber_lists sl ON sl.list_id = l.id AND sl.subscriber_id > $2
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE NOT l.dynamic AND s.status != 'blocklisted'
        AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END
    UNION ALL
    SELECT l.id, m.subscriber_id
    FROM (SELECT id FROM live_lists WHERE dynamic) l
    CROSS JOIN LATERAL axm_dynamic_list_members(l.id, $2) m
),
followup_held AS MATERIALIZED (
    SELECT j.subscriber_id FROM axm_followup_judge($1, NULL, $4) j
    WHERE $3::INT > 0 AND j.reason NOT IN (0, 1)
),
pending AS NOT MATERIALIZED (
    SELECT e.subscriber_id FROM candidates e
    WHERE NOT EXISTS (SELECT 1 FROM axm_campaign_sends s WHERE s.campaign_id = $1 AND s.subscriber_id = e.subscriber_id)
      AND NOT EXISTS (SELECT 1 FROM axm_campaign_followup_claims k
          WHERE k.campaign_id = $1 AND k.subscriber_id = e.subscriber_id AND k.sent)
      AND (e.list_id <> $3 OR NOT EXISTS (SELECT 1 FROM followup_held h WHERE h.subscriber_id = e.subscriber_id))
),
recent_candidates AS MATERIALIZED (
    SELECT DISTINCT p.subscriber_id FROM pending p
    WHERE NOT EXISTS (
        SELECT 1 FROM axm_campaign_recent_holds h
        WHERE h.campaign_id = $1 AND h.subscriber_id = p.subscriber_id
    )
)
SELECT EXISTS (SELECT 1 FROM live_lists) AS has_lists,
    CASE WHEN axm_recent_rule_on($1) THEN EXISTS (
        -- Check the saved rule against the edited lists inside the save transaction.
        -- Send the candidate IDs together so the rule scopes its campaigns once.
        SELECT 1 FROM recent_candidates e
        WHERE NOT EXISTS (
            SELECT 1 FROM axm_recent_received($1, ARRAY(SELECT subscriber_id FROM recent_candidates)) r
            WHERE r.subscriber_id = e.subscriber_id
        )
    ) ELSE EXISTS (SELECT 1 FROM pending) END AS has_recipients;

-- name: axm-preflight-sample
-- Up to $2 of the people campaign $1 would reach, the lowest IDs first, by the same rule as
-- axm-preflight-audience (keep the two the same). The pre-send check renders the mail for them.
-- With $4 (resume) true, only those still to be sent: after the checkpoint $3 and without a send
-- record or a sent follow-up decision, as in the remaining count of axm-preflight-audience.
-- Leave out the people excluded by the recent-send rule in both modes.
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
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = s.id)
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_followup_claims k WHERE k.campaign_id = $1 AND k.subscriber_id = s.id AND k.sent)))
    AND NOT EXISTS (SELECT 1 FROM axm_recent_received($1, ARRAY[s.id]))
    AND NOT EXISTS (
        SELECT 1 FROM axm_campaign_recent_holds h
        WHERE h.campaign_id = $1 AND h.subscriber_id = s.id
            AND axm_recent_rule_on($1))
ORDER BY s.id LIMIT $2;
