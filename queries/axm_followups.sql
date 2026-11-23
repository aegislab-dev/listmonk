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
SELECT f.campaign_id, f.parent_campaign_id, f.condition, c.name, pc.name AS parent_name,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids,
    f.version
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
JOIN campaigns pc ON pc.id = f.parent_campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
    AND NOT (c.id = ANY(COALESCE($1::INT[], '{}')))
ORDER BY f.campaign_id;

-- name: axm-followup-by-ids
-- The same rows for campaigns the sender just found, whatever made them start.
SELECT f.campaign_id, f.parent_campaign_id, f.condition, c.name, pc.name AS parent_name,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids,
    f.version
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
JOIN campaigns pc ON pc.id = f.parent_campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE f.campaign_id = ANY($1::INT[]);

-- name: axm-followup-lock-due
-- Locks the follow-up campaign while it is still due. No row means drop the check.
SELECT c.id FROM campaigns c
WHERE c.id = $1 AND (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
FOR UPDATE OF c;

-- name: axm-followup-version
-- The follow-up's version, read in its own statement once the campaign is locked: a statement
-- that waited for the lock still reads the other tables as they were when it began, and would
-- miss a change committed while it waited.
SELECT version FROM axm_campaign_followups WHERE campaign_id = $1;

-- name: axm-followup-unchecked
-- The follow-ups among the given campaigns whose last check was not made for the campaign as it is
-- now: never checked, or changed since (a later time saved, lists changed, paused and resumed).
-- The sender must not get them until they are checked again.
SELECT f.campaign_id FROM axm_campaign_followups f
WHERE f.campaign_id = ANY($1::INT[]) AND f.checked_version IS DISTINCT FROM f.version
ORDER BY f.campaign_id;

-- name: axm-followup-stale
-- TRUE when the campaign is a follow-up whose last check was not made for its version now. The
-- sender reads no recipients for it then (a pause and resume while the pipe was being set up).
SELECT COALESCE((SELECT f.checked_version IS DISTINCT FROM f.version FROM axm_campaign_followups f WHERE f.campaign_id = $1), FALSE);

-- name: axm-followup-unhold
-- A check starts over. Who is held back is decided again each time, so a person held for an
-- earlier time who meets the condition again (a schedule that moved, a subscription that came
-- back) is sent to.
DELETE FROM axm_campaign_followup_holds WHERE campaign_id = $1;

-- name: axm-followup-recheck
-- Holds back from follow-up campaign $1 the people the check's judgement says to
-- (axm_followup_judge, with $2 = opens and clicks recorded per subscriber now). Subscriptions are
-- not changed. The sender skips the people held here (next-campaign-subscribers,
-- next-campaign-subscribers-dynamic).
INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
SELECT $1::INT, j.subscriber_id, j.reason FROM axm_followup_judge($1, NULL, $2) j WHERE j.reason <> 0
-- A reaction recorded meanwhile may have held the person already (axm_followup_reaction).
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING
RETURNING reason;

-- name: axm-followup-pending
-- The same judgement, read only. How many people a check made now would hold back, and how many
-- of them the campaign would otherwise reach (the pre-send confirmation leaves them out), all and
-- after its checkpoint.
SELECT COUNT(*) FILTER (WHERE j.reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE j.reason = 2) AS opted_out,
    COUNT(*) FILTER (WHERE j.reason = 1) AS blocked,
    COUNT(*) FILTER (WHERE j.reason = 4) AS unknown,
    COUNT(*) FILTER (WHERE j.reason NOT IN (0, 1)
        AND CASE WHEN j.optin = 'double' THEN j.sub_status = 'confirmed' ELSE j.sub_status != 'unsubscribed' END) AS reachable,
    COUNT(*) FILTER (WHERE j.reason NOT IN (0, 1)
        AND CASE WHEN j.optin = 'double' THEN j.sub_status = 'confirmed' ELSE j.sub_status != 'unsubscribed' END
        AND j.subscriber_id > (SELECT last_subscriber_id FROM campaigns WHERE id = $1)) AS reachable_after
FROM axm_followup_judge($1, NULL, $2) j;

-- name: axm-followup-members
-- Keeps who got original $3 (its send records) and who the draft $1 was made for (on list $2).
INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id, made_for)
SELECT $1::INT, subscriber_id, TRUE FROM subscriber_lists WHERE list_id = $2::INT
UNION ALL
SELECT $1::INT, cs.subscriber_id, FALSE FROM axm_campaign_sends cs
WHERE cs.campaign_id = $3 AND NOT EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.list_id = $2 AND sl.subscriber_id = cs.subscriber_id);

-- name: axm-followup-reactions-now
-- Keeps for follow-up $1 the reactions to original $2 that are on record now (view and click
-- rows, and the open and click times of the send records), for everyone, also people not known
-- yet to have got the original (their send record can come later). It runs when
-- the follow-up is made, again once that is committed, and at the start of every check, so a
-- reaction is kept before its record can be cleared or expire.
INSERT INTO axm_campaign_followup_reactions (campaign_id, subscriber_id, opened, clicked)
SELECT $1::INT, e.sid, bool_or(e.o), bool_or(e.c) FROM (
    SELECT v.subscriber_id AS sid, TRUE AS o, FALSE AS c FROM campaign_views v WHERE v.campaign_id = $2 AND v.subscriber_id IS NOT NULL
    UNION ALL
    SELECT k.subscriber_id, FALSE, TRUE FROM link_clicks k WHERE k.campaign_id = $2 AND k.subscriber_id IS NOT NULL
    UNION ALL
    SELECT x.subscriber_id, x.opened_at IS NOT NULL, x.clicked_at IS NOT NULL FROM axm_campaign_sends x
    WHERE x.campaign_id = $2 AND (x.opened_at IS NOT NULL OR x.clicked_at IS NOT NULL)
) e
GROUP BY e.sid
ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET
    opened = axm_campaign_followup_reactions.opened OR EXCLUDED.opened,
    clicked = axm_campaign_followup_reactions.clicked OR EXCLUDED.clicked
    WHERE NOT (axm_campaign_followup_reactions.opened >= EXCLUDED.opened AND axm_campaign_followup_reactions.clicked >= EXCLUDED.clicked);

-- name: axm-followup-lock-original
-- Taken by the transaction that makes a follow-up, just before it commits. It waits for the
-- reactions to the original being recorded now (axm_followup_reaction takes it shared), which are
-- then kept once the follow-up is committed, and makes the ones that come later wait to see it.
SELECT pg_advisory_xact_lock(4747, $1);

-- name: axm-followup-guard
-- The original of follow-up $1, locked exclusively as a follow-up being made locks it: it waits
-- for the reactions and send records of the original being written now (their triggers lock it
-- shared). No row means the campaign is not a follow-up.
SELECT f.parent_campaign_id, pg_advisory_xact_lock(4747, f.parent_campaign_id)::TEXT AS locked
FROM axm_campaign_followups f WHERE f.campaign_id = $1;

-- name: axm-followup-guard-recipients
-- Keeps the people $3 of a batch who got original $2 for follow-up $1 (a send record of theirs that
-- was written after the last check).
INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id, made_for)
SELECT $1::INT, cs.subscriber_id, FALSE FROM axm_campaign_sends cs
WHERE cs.campaign_id = $2 AND cs.subscriber_id = ANY($3::INT[])
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;

-- name: axm-followup-guard-hold
-- Holds back the people $2 of a batch of follow-up $1 that the check's judgement says to, with $3
-- = opens and clicks recorded per subscriber now.
INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
SELECT $1::INT, j.subscriber_id, j.reason FROM axm_followup_judge($1, $2::INT[], $3) j WHERE j.reason <> 0
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING
RETURNING subscriber_id;

-- name: axm-followup-recipients-now
-- Keeps for follow-up $1 the people of the send records of original $2 that are not kept yet (send
-- records written after the follow-up was made, before axm_followup_recipients saw it).
INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id, made_for)
SELECT $1::INT, cs.subscriber_id, FALSE FROM axm_campaign_sends cs WHERE cs.campaign_id = $2
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;

-- name: axm-tracking-off-start
-- The app starts with opens and clicks not recorded per subscriber: a time without tracking
-- begins, unless one is open already.
INSERT INTO axm_tracking_off (started_at)
SELECT NOW() WHERE NOT EXISTS (SELECT 1 FROM axm_tracking_off WHERE ended_at IS NULL)
ON CONFLICT DO NOTHING;

-- name: axm-tracking-off-end
-- The app starts with tracking on: an open time without tracking ends.
UPDATE axm_tracking_off SET ended_at = NOW() WHERE ended_at IS NULL;

-- name: axm-lock-campaign-status
-- The status of a campaign, locked until the end of the transaction (core.UpdateCampaign).
SELECT status FROM campaigns WHERE id = $1 FOR UPDATE;

-- name: axm-followup-record
-- Records the check. The counts are those of the people held back now.
UPDATE axm_campaign_followups f SET rechecked_at = NOW(), checked_version = $2, recheck = jsonb_build_object(
    'reacted', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = -1),
    'opted_out', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason IN (2, 3)),
    'unknown', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 4),
    'blocked', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 1),
    'runs', COALESCE((f.recheck->>'runs')::INT, 0) + 1)
WHERE f.campaign_id = $1
RETURNING f.recheck;
