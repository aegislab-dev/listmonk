-- name: axm-followup-existing
SELECT f.campaign_id AS id, c.name, c.status, f.condition,
    (SELECT COUNT(*) FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id WHERE l.uuid = f.list_uuid AND sl.status != 'unsubscribed') AS list_count
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
WHERE f.parent_campaign_id = $1;

-- name: axm-followup-is-followup
SELECT EXISTS (SELECT 1 FROM axm_campaign_followups WHERE campaign_id = $1);

-- name: axm-followup-insert
-- $7 is TRUE when opens and clicks are not recorded per subscriber now, so the follow-up starts with
-- a tracking gap then (reactions from now on cannot be seen).
INSERT INTO axm_campaign_followups (campaign_id, parent_campaign_id, list_uuid, condition, counts, created_by, tracking_gap)
VALUES ($1, $2, $3::UUID, $4, $5, $6, $7);

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
-- Holds back from the follow-up campaign the people on its list who no longer meet the condition,
-- because they reacted to the original (-1), opted out of it (2) or were blocked (1), or because
-- the records to judge by are gone (4). Subscriptions are not changed. The sender skips the people
-- held here (next-campaign-subscribers, next-campaign-subscribers-dynamic). Judged are the people
-- the draft was made for and anyone else on the list who got the original. Not judged are people
-- added by hand who never got it, people who already got the follow-up, people not subscribed to
-- the list, and people the campaign also reaches through another of its lists.
-- A reaction is one recorded for the follow-up as it happened (axm_followup_reaction) or any record
-- of it still there, a view or click row or the open or click time of the send record.
-- $1 original, $2 open, $3 click, $4 follow-up list ids, $5 follow-up campaign, $6 opens and
-- clicks recorded per subscriber now (the app's settings).
-- Keep the judged part identical to axm-followup-pending.
WITH ev AS (
    -- Whether reactions can be seen. Without per-subscriber tracking now ($6), or after a time
    -- without it since the follow-up was made, an open or click may not have been recorded, so
    -- nobody's can be judged on a "did not" condition.
    SELECT ($2 <> 'no' AND $3 <> 'no')
        OR ($6::BOOLEAN AND NOT (SELECT f.tracking_gap FROM axm_campaign_followups f WHERE f.campaign_id = $5)) AS known
),
judged AS (
    SELECT sl.subscriber_id, sl.status AS sub_status, l.optin, CASE
        WHEN s.status = 'blocklisted' THEN 1
        WHEN EXISTS (
            SELECT 1 FROM campaign_lists cl JOIN subscriber_lists u ON u.list_id = cl.list_id
            WHERE cl.campaign_id = $1 AND u.subscriber_id = sl.subscriber_id AND u.status = 'unsubscribed'
        ) THEN 2
        WHEN NOT (SELECT known FROM ev) THEN 4
        -- A "did not" condition is broken by a reaction kept for the follow-up (as it happened, or
        -- found when the draft was made) or by any record of one that is still there. A "did"
        -- condition held for the people the draft was made for; anyone else needs a record of it.
        WHEN ($2 = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.opened)
                OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = $1 AND v.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
            OR ($3 = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = $1 AND k.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))
            OR (($2 = 'yes' OR $3 = 'yes')
                AND NOT EXISTS (SELECT 1 FROM axm_campaign_followup_members m WHERE m.campaign_id = $5 AND m.subscriber_id = sl.subscriber_id)
                AND (($2 = 'yes' AND NOT (
                        EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.opened)
                        OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = $1 AND v.subscriber_id = sl.subscriber_id)
                        OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
                    OR ($3 = 'yes' AND NOT (
                        EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                        OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = $1 AND k.subscriber_id = sl.subscriber_id)
                        OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))))
            THEN -1
        ELSE 0
    END AS reason
    FROM subscriber_lists sl
    JOIN subscribers s ON s.id = sl.subscriber_id
    JOIN lists l ON l.id = sl.list_id
    WHERE sl.list_id = ANY($4::INT[]) AND sl.status != 'unsubscribed'
        -- Who the draft was made for (kept apart, so also after their row was taken off and put
        -- back or the original's send records expired), or anyone else who got the original.
        AND (EXISTS (SELECT 1 FROM axm_campaign_followup_members m WHERE m.campaign_id = $5 AND m.subscriber_id = sl.subscriber_id)
            OR EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id))
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $5 AND sent.subscriber_id = sl.subscriber_id)
        AND NOT axm_followup_reaches_otherwise($5, sl.subscriber_id, $4::INT[])
)
INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
SELECT $5, subscriber_id, reason FROM judged WHERE reason <> 0
-- A reaction recorded meanwhile may have held the person already (axm_followup_reaction).
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING
RETURNING reason;

-- name: axm-followup-pending
-- The same judgement, read only. How many people a check made now would hold back, and how many
-- of them the campaign would otherwise reach (the pre-send confirmation leaves them out), all and
-- after its checkpoint. Keep the judged part identical to axm-followup-recheck.
WITH ev AS (
    -- Whether reactions can be seen. Without per-subscriber tracking now ($6), or after a time
    -- without it since the follow-up was made, an open or click may not have been recorded, so
    -- nobody's can be judged on a "did not" condition.
    SELECT ($2 <> 'no' AND $3 <> 'no')
        OR ($6::BOOLEAN AND NOT (SELECT f.tracking_gap FROM axm_campaign_followups f WHERE f.campaign_id = $5)) AS known
),
judged AS (
    SELECT sl.subscriber_id, sl.status AS sub_status, l.optin, CASE
        WHEN s.status = 'blocklisted' THEN 1
        WHEN EXISTS (
            SELECT 1 FROM campaign_lists cl JOIN subscriber_lists u ON u.list_id = cl.list_id
            WHERE cl.campaign_id = $1 AND u.subscriber_id = sl.subscriber_id AND u.status = 'unsubscribed'
        ) THEN 2
        WHEN NOT (SELECT known FROM ev) THEN 4
        -- A "did not" condition is broken by a reaction kept for the follow-up (as it happened, or
        -- found when the draft was made) or by any record of one that is still there. A "did"
        -- condition held for the people the draft was made for; anyone else needs a record of it.
        WHEN ($2 = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.opened)
                OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = $1 AND v.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
            OR ($3 = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = $1 AND k.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))
            OR (($2 = 'yes' OR $3 = 'yes')
                AND NOT EXISTS (SELECT 1 FROM axm_campaign_followup_members m WHERE m.campaign_id = $5 AND m.subscriber_id = sl.subscriber_id)
                AND (($2 = 'yes' AND NOT (
                        EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.opened)
                        OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = $1 AND v.subscriber_id = sl.subscriber_id)
                        OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
                    OR ($3 = 'yes' AND NOT (
                        EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = $5 AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                        OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = $1 AND k.subscriber_id = sl.subscriber_id)
                        OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))))
            THEN -1
        ELSE 0
    END AS reason
    FROM subscriber_lists sl
    JOIN subscribers s ON s.id = sl.subscriber_id
    JOIN lists l ON l.id = sl.list_id
    WHERE sl.list_id = ANY($4::INT[]) AND sl.status != 'unsubscribed'
        -- Who the draft was made for (kept apart, so also after their row was taken off and put
        -- back or the original's send records expired), or anyone else who got the original.
        AND (EXISTS (SELECT 1 FROM axm_campaign_followup_members m WHERE m.campaign_id = $5 AND m.subscriber_id = sl.subscriber_id)
            OR EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $1 AND cs.subscriber_id = sl.subscriber_id))
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $5 AND sent.subscriber_id = sl.subscriber_id)
        AND NOT axm_followup_reaches_otherwise($5, sl.subscriber_id, $4::INT[])
)
SELECT COUNT(*) FILTER (WHERE reason = -1) AS reacted,
    COUNT(*) FILTER (WHERE reason = 2) AS opted_out,
    COUNT(*) FILTER (WHERE reason = 1) AS blocked,
    COUNT(*) FILTER (WHERE reason = 4) AS unknown,
    COUNT(*) FILTER (WHERE reason NOT IN (0, 1)
        AND CASE WHEN optin = 'double' THEN sub_status = 'confirmed' ELSE sub_status != 'unsubscribed' END) AS reachable,
    COUNT(*) FILTER (WHERE reason NOT IN (0, 1)
        AND CASE WHEN optin = 'double' THEN sub_status = 'confirmed' ELSE sub_status != 'unsubscribed' END
        AND subscriber_id > (SELECT last_subscriber_id FROM campaigns WHERE id = $5)) AS reachable_after
FROM judged;

-- name: axm-followup-members
-- Keeps who the draft was made for, everyone on the new follow-up list.
INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id)
SELECT $1, subscriber_id FROM subscriber_lists WHERE list_id = $2;

-- name: axm-followup-reactions-now
-- Keeps for the new follow-up $1 the reactions to original $2 that are on record now for the
-- people it was made for (also only in the send record), so that they still count once that
-- record is gone.
INSERT INTO axm_campaign_followup_reactions (campaign_id, subscriber_id, opened, clicked)
SELECT x.campaign_id, x.subscriber_id, x.opened, x.clicked FROM (
    SELECT m.campaign_id, m.subscriber_id,
        EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = $2 AND v.subscriber_id = m.subscriber_id)
            OR EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $2 AND cs.subscriber_id = m.subscriber_id AND cs.opened_at IS NOT NULL) AS opened,
        EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = $2 AND k.subscriber_id = m.subscriber_id)
            OR EXISTS (SELECT 1 FROM axm_campaign_sends cs WHERE cs.campaign_id = $2 AND cs.subscriber_id = m.subscriber_id AND cs.clicked_at IS NOT NULL) AS clicked
    FROM axm_campaign_followup_members m WHERE m.campaign_id = $1
) x WHERE x.opened OR x.clicked
ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET
    opened = axm_campaign_followup_reactions.opened OR EXCLUDED.opened,
    clicked = axm_campaign_followup_reactions.clicked OR EXCLUDED.clicked;

-- name: axm-followup-tracking-gap
-- Opens and clicks are not recorded per subscriber now, so the follow-ups still to be sent cannot
-- see reactions of this time, so they hold their people back until made again.
UPDATE axm_campaign_followups f SET tracking_gap = TRUE, version = version + 1
FROM campaigns c
WHERE c.id = f.campaign_id AND NOT f.tracking_gap
    AND c.status IN ('draft', 'scheduled', 'paused', 'running');

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
