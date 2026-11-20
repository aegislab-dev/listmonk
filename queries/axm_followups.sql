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
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids
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
SELECT f.campaign_id, f.parent_campaign_id, f.condition,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
    AND NOT (c.id = ANY(COALESCE($1::INT[], '{}')));

-- name: axm-followup-by-ids
-- The same rows for campaigns the sender just found, whatever made them start.
SELECT f.campaign_id, f.parent_campaign_id, f.condition,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids
FROM axm_campaign_followups f
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE f.campaign_id = ANY($1::INT[]);

-- name: axm-followup-finish-empty
-- A follow-up with nobody left to send to ends here. The sender does not finish a campaign
-- that has no recipients at all (it stays scheduled), and someone added to the list later
-- would then get the mail without being scheduled again. Only a campaign that is still due is
-- ended: a schedule moved to a later time while the check ran stays as it is. The counts are not
-- touched (a resumed campaign keeps its to_send). Who is left is counted as next-campaigns counts
-- it: confirmed only on a double opt-in list, anyone not unsubscribed on the others, and never a
-- blocklisted subscriber.
UPDATE campaigns SET status = 'finished', started_at = COALESCE(started_at, NOW()), updated_at = NOW()
WHERE id = $1 AND (status = 'running' OR (status = 'scheduled' AND NOW() >= send_at))
    AND NOT EXISTS (SELECT 1 FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
        WHERE cl.campaign_id = $1 AND l.axm_filter IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM campaign_lists cl
        JOIN lists l ON l.id = cl.list_id
        JOIN subscriber_lists sl ON sl.list_id = l.id
        JOIN subscribers s ON s.id = sl.subscriber_id
        WHERE cl.campaign_id = $1 AND s.status != 'blocklisted'
            AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END)
RETURNING id;

-- name: axm-followup-recheck
-- Removes from the follow-up list the people who got the original campaign and no longer meet
-- its condition. A person who is not in the shared candidate set reacted since the list was
-- made, and one with a reason other than 0 is blocked or opted out. People who were added by
-- hand and never got the original, and people who already got the follow-up, are left alone.
-- This text continues the shared candidate CTE, which is put in front of it at startup.
, verdict AS (
    SELECT sl.subscriber_id, COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    JOIN axm_campaign_sends cs ON cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[])
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
),
gone AS (
    DELETE FROM subscriber_lists sl USING verdict v
    WHERE sl.list_id = ANY($6::INT[]) AND sl.subscriber_id = v.subscriber_id AND v.reason <> 0
    RETURNING v.reason
)
SELECT COUNT(*) FILTER (WHERE reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE reason IN (2, 3)) AS opted_out,
    COUNT(*) FILTER (WHERE reason = 1) AS blocked
FROM gone;

-- name: axm-followup-pending
-- The same judgement as the recheck, read only: how many would leave the list if it ran now.
-- This text continues the shared candidate CTE, which is put in front of it at startup.
, verdict AS (
    SELECT sl.subscriber_id, COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    JOIN axm_campaign_sends cs ON cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[])
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
)
SELECT COUNT(*) FILTER (WHERE reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE reason IN (2, 3)) AS opted_out,
    COUNT(*) FILTER (WHERE reason = 1) AS blocked
FROM verdict;

-- name: axm-followup-record
UPDATE axm_campaign_followups SET rechecked_at = NOW(), recheck = jsonb_build_object(
    'reacted', COALESCE((recheck->>'reacted')::INT, 0) + $2,
    'opted_out', COALESCE((recheck->>'opted_out')::INT, 0) + $3,
    'blocked', COALESCE((recheck->>'blocked')::INT, 0) + $4,
    'runs', COALESCE((recheck->>'runs')::INT, 0) + 1)
WHERE campaign_id = $1;
