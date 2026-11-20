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
-- Holds back from the follow-up list the people who got the original campaign and no longer meet
-- its condition, and gives back the people held by an earlier check who meet it again. A person is
-- held by setting the subscription to unsubscribed; nothing is deleted, so the check can be undone
-- by the next one (a schedule that moved, a subscription that came back) and a record of the person
-- is never lost. The held rows are remembered in held (subscriber, status before, updated_at after):
-- a row that changed since, for example an unsubscribe by the person, is no longer ours and is left
-- alone. People added by hand who never got the original, and people who already got the follow-up,
-- are not judged. This text continues the shared candidate CTE put in front of it at startup.
, held AS (
    SELECT (h->>'s')::INT AS sid, h->>'st' AS st, (h->>'u')::TIMESTAMPTZ AS u
    FROM jsonb_array_elements((SELECT held FROM axm_campaign_followups WHERE campaign_id = $7)) h
),
verdict AS (
    SELECT sl.list_id, sl.subscriber_id, sl.status AS cur, hd.st AS held_st, hd.u AS held_u, COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    JOIN axm_campaign_sends cs ON cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id
    LEFT JOIN held hd ON hd.sid = sl.subscriber_id AND sl.status = 'unsubscribed' AND sl.updated_at = hd.u
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[]) AND (sl.status != 'unsubscribed' OR hd.sid IS NOT NULL)
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
)
UPDATE subscriber_lists sl
SET status = (CASE WHEN v.reason <> 0 THEN 'unsubscribed' ELSE v.held_st END)::subscription_status, updated_at = NOW()
FROM verdict v
WHERE sl.list_id = v.list_id AND sl.subscriber_id = v.subscriber_id
    AND ((v.reason <> 0 AND v.held_st IS NULL AND sl.status != 'unsubscribed')
        OR (v.reason = 0 AND v.held_st IS NOT NULL AND sl.status = 'unsubscribed' AND sl.updated_at = v.held_u))
RETURNING sl.subscriber_id AS subscriber_id, sl.status::TEXT AS new_status, v.cur::TEXT AS before_status,
    v.reason AS reason, sl.updated_at AS updated_at;

-- name: axm-followup-pending
-- The same judgement, read only: how many people still on the list would be held back if the check
-- ran now. This text continues the shared candidate CTE, which is put in front of it at startup.
, held AS (
    SELECT (h->>'s')::INT AS sid, (h->>'u')::TIMESTAMPTZ AS u
    FROM jsonb_array_elements((SELECT held FROM axm_campaign_followups WHERE campaign_id = $7)) h
),
verdict AS (
    SELECT COALESCE(rc.reason, -1) AS reason
    FROM subscriber_lists sl
    JOIN axm_campaign_sends cs ON cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id
    LEFT JOIN reaction_candidates rc ON rc.id = sl.subscriber_id
    WHERE sl.list_id = ANY($6::INT[]) AND sl.status != 'unsubscribed'
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
)
SELECT COUNT(*) FILTER (WHERE reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE reason IN (2, 3)) AS opted_out,
    COUNT(*) FILTER (WHERE reason = 1) AS blocked
FROM verdict;

-- name: axm-followup-lock-due
-- Locks the follow-up campaign while it is still due, so that a schedule moved to a later time
-- cannot be overtaken by a check made for the earlier one: no row means do not check.
SELECT id FROM campaigns
WHERE id = $1 AND (status = 'running' OR (status = 'scheduled' AND NOW() >= send_at))
FOR UPDATE;

-- name: axm-followup-hold
-- Remembers who the check holds back: the earlier ones that are still held (their row has not
-- changed since) and the new ones ($3), and sets the counts to those held now.
WITH kept AS (
    SELECT e FROM axm_campaign_followups f, jsonb_array_elements(f.held) e
    WHERE f.campaign_id = $1 AND EXISTS (
        SELECT 1 FROM subscriber_lists sl
        WHERE sl.list_id = ANY($2::INT[]) AND sl.subscriber_id = (e->>'s')::INT
            AND sl.status = 'unsubscribed' AND sl.updated_at = (e->>'u')::TIMESTAMPTZ)
), merged AS (
    SELECT COALESCE(jsonb_agg(x.e), '[]'::JSONB) AS h
    FROM (SELECT e FROM kept UNION ALL SELECT jsonb_array_elements($3::JSONB) AS e) x
)
UPDATE axm_campaign_followups f SET held = m.h, rechecked_at = NOW(), recheck = jsonb_build_object(
    'reacted', (SELECT COUNT(*) FROM jsonb_array_elements(m.h) e WHERE e->>'r' = '-1'),
    'opted_out', (SELECT COUNT(*) FROM jsonb_array_elements(m.h) e WHERE e->>'r' IN ('2', '3')),
    'blocked', (SELECT COUNT(*) FROM jsonb_array_elements(m.h) e WHERE e->>'r' = '1'),
    'runs', COALESCE((f.recheck->>'runs')::INT, 0) + 1)
FROM merged m WHERE f.campaign_id = $1;
