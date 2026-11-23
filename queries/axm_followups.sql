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

-- name: axm-followup-by-ids
-- Follow-up campaigns with their original, condition, names and the follow-up list (only while it
-- is still one of the campaign's lists, and not dynamic).
SELECT f.campaign_id, f.parent_campaign_id, f.condition, c.name, pc.name AS parent_name,
    CASE WHEN l.id IS NOT NULL AND l.axm_filter IS NULL AND EXISTS (
        SELECT 1 FROM campaign_lists x WHERE x.campaign_id = f.campaign_id AND x.list_id = l.id
    ) THEN ARRAY[l.id] ELSE '{}'::INT[] END AS list_ids
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
JOIN campaigns pc ON pc.id = f.parent_campaign_id
LEFT JOIN lists l ON l.uuid = f.list_uuid
WHERE f.campaign_id = ANY($1::INT[]);

-- name: axm-followup-claim-lock
-- The original of follow-up $1, locked exclusively before a batch of it is judged: this waits for
-- the reactions and send records of the original being written now (their triggers lock it
-- shared), so the judgement after it sees them. No row means the campaign is not a follow-up.
SELECT f.parent_campaign_id, pg_advisory_xact_lock(4747, f.parent_campaign_id)::TEXT AS locked
FROM axm_campaign_followups f WHERE f.campaign_id = $1;

-- name: axm-followup-claim-recipients
-- Keeps the people $3 of a batch who got original $2 for follow-up $1 (a send record of theirs that
-- came after the follow-up was made).
INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id, made_for)
SELECT $1::INT, cs.subscriber_id, FALSE FROM axm_campaign_sends cs
WHERE cs.campaign_id = $2 AND cs.subscriber_id = ANY($3::INT[])
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;

-- name: axm-followup-claim
-- Decides, just before sending, who of the batch $2 of follow-up $1 may be sent to (with $3 = opens
-- and clicks recorded per subscriber now). It runs in the transaction that locked the original
-- first (axm-followup-claim-lock), so everything committed before it is seen. One row per person
-- of the batch who still exists, with the reason to leave them out: 0 none (send), 1 blocked, 5 no
-- longer subscribed through the campaign's lists, or the reason axm_followup_judge gives. Someone
-- with no row is not sent to. The outcome is recorded in the holds (inserted tells a person left
-- out now who was not before, given_back one sent now who was left out before).
WITH j AS MATERIALIZED (
    SELECT subscriber_id, reason FROM axm_followup_judge($1, $2::INT[], $3)
),
verdict AS MATERIALIZED (
    SELECT x.sid, CASE
        WHEN s.status = 'blocklisted' THEN 1
        WHEN NOT EXISTS (
            SELECT 1 FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
            WHERE cl.campaign_id = $1 AND (
                (l.axm_filter IS NULL AND EXISTS (
                    SELECT 1 FROM subscriber_lists sl WHERE sl.list_id = l.id AND sl.subscriber_id = x.sid
                        AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END))
                OR (l.axm_filter IS NOT NULL AND EXISTS (
                    SELECT 1 FROM axm_dynamic_list_members(l.id, x.sid - 1, x.sid, 1))))
        ) THEN 5
        ELSE COALESCE((SELECT j.reason FROM j WHERE j.subscriber_id = x.sid), 0)
    END AS reason
    FROM unnest($2::INT[]) AS x(sid) JOIN subscribers s ON s.id = x.sid
),
cleared AS (
    DELETE FROM axm_campaign_followup_holds h
    WHERE h.campaign_id = $1 AND h.subscriber_id = ANY($2::INT[])
        AND EXISTS (SELECT 1 FROM verdict v WHERE v.sid = h.subscriber_id AND v.reason = 0)
    RETURNING h.subscriber_id
),
held AS (
    INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
    SELECT $1::INT, sid, reason FROM verdict WHERE reason <> 0
    ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET reason = EXCLUDED.reason
    RETURNING subscriber_id, (xmax = 0) AS inserted
)
SELECT v.sid AS subscriber_id, v.reason, COALESCE(h.inserted, FALSE) AS inserted,
    EXISTS (SELECT 1 FROM cleared c WHERE c.subscriber_id = v.sid) AS given_back
FROM verdict v LEFT JOIN held h ON h.subscriber_id = v.sid;

-- name: axm-followup-to-send
-- Keeps follow-up $1's to_send what it means to send while it sends: $2 people newly left out come
-- off, $3 people left out before and sent now come back on. (The start of a run takes the people
-- left out off what it counts: axm_followup_recount.)
UPDATE campaigns SET to_send = GREATEST(0, to_send - $2 + $3) WHERE id = $1;

-- name: axm-followup-finish-empty
-- A follow-up that is due with nobody at all on its lists is ended here (the sender does not start
-- a campaign without recipients; left scheduled, it would go later to anyone who subscribed, without
-- a new schedule). Who is on its lists is counted as the sender counts it. The row is read again if
-- it changed meanwhile, so a schedule moved to a later time stays as it is.
UPDATE campaigns c SET status = 'finished', updated_at = NOW()
WHERE c.id = $1 AND c.status = 'scheduled' AND NOW() >= c.send_at
    AND EXISTS (SELECT 1 FROM axm_campaign_followups f WHERE f.campaign_id = c.id)
    AND NOT EXISTS (SELECT 1 FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
        WHERE cl.campaign_id = c.id AND l.axm_filter IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM campaign_lists cl
        JOIN lists l ON l.id = cl.list_id
        JOIN subscriber_lists sl ON sl.list_id = l.id
        JOIN subscribers s ON s.id = sl.subscriber_id
        WHERE cl.campaign_id = c.id AND s.status != 'blocklisted'
            AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END)
RETURNING c.id;

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

-- name: axm-followup-record
-- Records a judged batch. The counts are those of the people left out so far.
UPDATE axm_campaign_followups f SET rechecked_at = NOW(), recheck = jsonb_build_object(
    'reacted', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = -1),
    'opted_out', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason IN (2, 3)),
    'unknown', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 4),
    'blocked', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 1),
    'unsubscribed', (SELECT COUNT(*) FROM axm_campaign_followup_holds h WHERE h.campaign_id = $1 AND h.reason = 5),
    'runs', COALESCE((f.recheck->>'runs')::INT, 0) + 1)
WHERE f.campaign_id = $1
RETURNING f.recheck;
