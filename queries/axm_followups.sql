-- name: axm-followup-existing
SELECT f.campaign_id AS id, c.name, c.status
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
WHERE f.parent_campaign_id = $1;

-- name: axm-followup-is-followup
SELECT EXISTS (SELECT 1 FROM axm_campaign_followups WHERE campaign_id = $1);

-- name: axm-followup-insert
INSERT INTO axm_campaign_followups (campaign_id, parent_campaign_id, list_id, condition, counts, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: axm-followup-get
-- One row for the campaign on either side of a link. role is followup when the campaign is
-- the follow-up and parent when it is the original.
SELECT CASE WHEN f.campaign_id = $1 THEN 'followup' ELSE 'parent' END AS role,
    f.campaign_id, f.parent_campaign_id, f.list_id, f.condition, f.counts, f.recheck, f.rechecked_at, f.created_at,
    fc.name AS followup_name, fc.status AS followup_status, fc.send_at AS followup_send_at,
    pc.name AS parent_name, pc.status AS parent_status,
    l.name AS list_name,
    (SELECT COUNT(*) FROM subscriber_lists sl WHERE sl.list_id = f.list_id AND sl.status != 'unsubscribed') AS list_count,
    (SELECT COUNT(*) FROM campaign_lists cl WHERE cl.campaign_id = f.campaign_id) AS campaign_lists,
    EXISTS (SELECT 1 FROM campaign_lists cl WHERE cl.campaign_id = f.campaign_id AND cl.list_id = f.list_id) AS has_list
FROM axm_campaign_followups f
JOIN campaigns fc ON fc.id = f.campaign_id
JOIN campaigns pc ON pc.id = f.parent_campaign_id
LEFT JOIN lists l ON l.id = f.list_id
WHERE f.campaign_id = $1 OR f.parent_campaign_id = $1;

-- name: axm-followup-due
-- Follow-up campaigns the sender is about to pick up that it does not hold yet: running, or
-- scheduled and due. They are checked again before every start, resume and restart.
SELECT f.campaign_id, f.parent_campaign_id, f.list_id, f.condition
FROM axm_campaign_followups f JOIN campaigns c ON c.id = f.campaign_id
WHERE f.list_id IS NOT NULL
    AND (c.status = 'running' OR (c.status = 'scheduled' AND NOW() >= c.send_at))
    AND NOT (c.id = ANY(COALESCE($1::INT[], '{}')));

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
    WHERE sl.list_id = $6
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = $7 AND sent.subscriber_id = sl.subscriber_id)
),
gone AS (
    DELETE FROM subscriber_lists sl USING verdict v
    WHERE sl.list_id = $6 AND sl.subscriber_id = v.subscriber_id AND v.reason <> 0
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
    WHERE sl.list_id = $6
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
