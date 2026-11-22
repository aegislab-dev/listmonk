-- name: axm-followup-existing
SELECT f.campaign_id AS id, c.name, c.status, f.condition,
    (SELECT COUNT(*) FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id WHERE l.uuid = f.list_uuid AND sl.status != 'unsubscribed') AS list_count
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
WHERE f.parent_campaign_id = $1;

-- name: axm-followup-is-followup
SELECT EXISTS (SELECT 1 FROM axm_campaign_followups WHERE campaign_id = $1);

-- name: axm-followup-insert
INSERT INTO axm_campaign_followups (campaign_id, parent_campaign_id, list_uuid, condition, counts, created_by)
VALUES ($1, $2, $3::UUID, $4, $5, $6);

-- name: axm-followup-get
-- One row for the campaign on either side of a link. role is followup when the campaign is
-- the follow-up and parent when it is the original. The follow-up list is found by its UUID.
SELECT CASE WHEN f.campaign_id = $1 THEN 'followup' ELSE 'parent' END AS role,
    f.campaign_id, f.parent_campaign_id, l.id AS list_id, f.condition, f.counts, f.recheck, f.rechecked_at, f.created_at,
    fc.name AS followup_name, fc.status AS followup_status, fc.send_at AS followup_send_at,
    pc.name AS parent_name, pc.status AS parent_status, pc.send_at AS parent_send_at,
    l.name AS list_name,
    (SELECT COUNT(*) FROM subscriber_lists sl WHERE sl.list_id = l.id AND sl.status != 'unsubscribed') AS list_count,
    (SELECT COUNT(*) FROM campaign_lists cl WHERE cl.campaign_id = f.campaign_id) AS campaign_lists,
    EXISTS (SELECT 1 FROM campaign_lists cl WHERE cl.campaign_id = f.campaign_id AND cl.list_id = l.id) AS has_list,
    (SELECT MAX(cs.sent_at) FROM axm_campaign_sends cs WHERE cs.campaign_id = f.parent_campaign_id) AS parent_last_sent_at,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids
FROM axm_campaign_followups f
JOIN campaigns fc ON fc.id = f.campaign_id
JOIN campaigns pc ON pc.id = f.parent_campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE f.campaign_id = $1 OR f.parent_campaign_id = $1;

-- name: axm-followup-due
-- Follow-up campaigns the sender is about to pick up that it does not hold yet: running, or
-- scheduled and due. They are checked again before every start, resume and restart. The list to
-- check is the follow-up list, found by its UUID, so a list that was deleted and restored from
-- the trash is still the one checked, and a list added to the campaign by hand is never touched.
-- A follow-up list that was taken off the campaign is not checked (nobody is held back then).
SELECT f.campaign_id, f.parent_campaign_id, f.condition,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids,
    c.updated_at::TEXT || '/' || COALESCE((SELECT string_agg(x.list_id::TEXT, ',' ORDER BY x.list_id) FROM campaign_lists x WHERE x.campaign_id = c.id), '') AS version
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
    AND NOT (c.id = ANY(COALESCE($1::INT[], '{}')))
ORDER BY f.campaign_id;

-- name: axm-followup-by-ids
-- The same rows for campaigns the sender just found, whatever made them start.
SELECT f.campaign_id, f.parent_campaign_id, f.condition,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids,
    c.updated_at::TEXT || '/' || COALESCE((SELECT string_agg(x.list_id::TEXT, ',' ORDER BY x.list_id) FROM campaign_lists x WHERE x.campaign_id = c.id), '') AS version
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE f.campaign_id = ANY($1::INT[]);

-- name: axm-followup-lock-due
-- Locks the follow-up campaign while it is still due and gives its version (the last change of
-- the campaign and its lists), so that a check made for an earlier version (a schedule moved to a
-- later time, lists changed) is dropped. No row means drop the check.
SELECT c.updated_at::TEXT || '/' || COALESCE((SELECT string_agg(x.list_id::TEXT, ',' ORDER BY x.list_id) FROM campaign_lists x WHERE x.campaign_id = c.id), '') AS version FROM campaigns c
WHERE c.id = $1 AND (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
FOR UPDATE OF c;

-- name: axm-followup-unchecked
-- The follow-ups among the given campaigns whose last check was not made for the campaign as it is
-- now: never checked, or changed since (a later time saved, lists changed, paused and resumed).
-- The sender must not get them until they are checked again.
SELECT f.campaign_id FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
WHERE f.campaign_id = ANY($1::INT[])
    AND f.checked_for IS DISTINCT FROM (c.updated_at::TEXT || '/' || COALESCE((SELECT string_agg(x.list_id::TEXT, ',' ORDER BY x.list_id) FROM campaign_lists x WHERE x.campaign_id = c.id), ''))
ORDER BY f.campaign_id;

-- name: axm-followup-unhold
-- A check starts over. Who is held back is decided again each time, so a person held for an
-- earlier time who meets the condition again (a schedule that moved, a subscription that came
-- back) is sent to.
DELETE FROM axm_campaign_followup_holds WHERE campaign_id = $1;

-- name: axm-followup-recheck
-- Holds back from the follow-up campaign the people on its list who got the original campaign and
-- no longer meet the condition, because they reacted to it (-1), opted out of it (2, 3) or were blocked (1).
-- Subscriptions are not changed. The sender skips the people held here (next-campaign-subscribers,
-- next-campaign-subscribers-dynamic). Not judged are people added by hand who never got the original,
-- people who already got the follow-up, people not subscribed to the list, and people the campaign
-- also reaches through another of its lists (a list added by hand stays as it is).
-- Keep the judged part identical to axm-followup-pending. This text continues the shared candidate
-- CTE put in front of it at startup.
, judged AS (
    SELECT sl.subscriber_id, COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[]) AND sl.status != 'unsubscribed'
        AND EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id)
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
        AND NOT EXISTS (
            SELECT 1 FROM campaign_lists cl JOIN lists ol ON ol.id = cl.list_id
            WHERE cl.campaign_id = $7 AND NOT (cl.list_id = ANY($6::INT[])) AND (
                (ol.axm_filter IS NULL AND EXISTS (
                    SELECT 1 FROM subscriber_lists o WHERE o.list_id = ol.id AND o.subscriber_id = sl.subscriber_id
                        AND CASE WHEN ol.optin = 'double' THEN o.status = 'confirmed' ELSE o.status != 'unsubscribed' END))
                OR (ol.axm_filter IS NOT NULL AND EXISTS (
                    SELECT 1 FROM axm_dynamic_list_members(ol.id, sl.subscriber_id - 1, sl.subscriber_id, 1))))
        )
)
INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
SELECT $7, subscriber_id, reason FROM judged WHERE reason <> 0
RETURNING reason;

-- name: axm-followup-pending
-- The same judgement, read only. How many people a check made now would hold back.
-- Keep the judged part identical to axm-followup-recheck. This text continues the shared candidate
-- CTE, which is put in front of it at startup.
, judged AS (
    SELECT sl.subscriber_id, COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[]) AND sl.status != 'unsubscribed'
        AND EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id)
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
        AND NOT EXISTS (
            SELECT 1 FROM campaign_lists cl JOIN lists ol ON ol.id = cl.list_id
            WHERE cl.campaign_id = $7 AND NOT (cl.list_id = ANY($6::INT[])) AND (
                (ol.axm_filter IS NULL AND EXISTS (
                    SELECT 1 FROM subscriber_lists o WHERE o.list_id = ol.id AND o.subscriber_id = sl.subscriber_id
                        AND CASE WHEN ol.optin = 'double' THEN o.status = 'confirmed' ELSE o.status != 'unsubscribed' END))
                OR (ol.axm_filter IS NOT NULL AND EXISTS (
                    SELECT 1 FROM axm_dynamic_list_members(ol.id, sl.subscriber_id - 1, sl.subscriber_id, 1))))
        )
)
SELECT COUNT(*) FILTER (WHERE reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE reason IN (2, 3)) AS opted_out,
    COUNT(*) FILTER (WHERE reason = 1) AS blocked
FROM judged;

-- name: axm-lock-campaign-status
-- The status of a campaign, locked until the end of the transaction (core.UpdateCampaign).
SELECT status FROM campaigns WHERE id = $1 FOR UPDATE;

-- name: axm-followup-record
-- Records the check. The counts are those of the people held back now.
UPDATE axm_campaign_followups f SET rechecked_at = NOW(), checked_for = $2, recheck = jsonb_build_object(
    'reacted', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = -1),
    'opted_out', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason IN (2, 3)),
    'blocked', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 1),
    'runs', COALESCE((f.recheck->>'runs')::INT, 0) + 1)
WHERE f.campaign_id = $1;
