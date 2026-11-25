-- name: axm-record-campaign-sends
-- Records a batch of delivered campaign messages. $3 holds, per message, how many
-- milliseconds ago it was handed to the messenger when this statement runs, so sent_at
-- is on the database clock like opened_at, clicked_at and every other timestamp here.
-- A message whose campaign or subscriber was deleted meanwhile is skipped rather than
-- failing the batch, and a repeated (campaign, subscriber) keeps its first record.
INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at)
SELECT u.campaign_id, u.subscriber_id, NOW() - (u.age_ms * INTERVAL '1 millisecond')
FROM UNNEST($1::INT[], $2::INT[], $3::BIGINT[]) AS u(campaign_id, subscriber_id, age_ms)
JOIN campaigns c ON c.id = u.campaign_id
JOIN subscribers s ON s.id = u.subscriber_id
ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;

-- name: axm-mark-send-opened
-- Marks a view on the send record of ($1 campaign UUID, $2 subscriber UUID). $3 is the
-- client class of this request (models.AgentKind*). Does nothing when the message has no
-- send record, e.g. it went out before records existed.
UPDATE axm_campaign_sends cs SET
    opened_at = COALESCE(cs.opened_at, NOW()),
    last_opened_at = NOW(),
    open_count = LEAST(COALESCE(cs.open_count, 0), 2147483646) + 1,
    open_kind = COALESCE(cs.open_kind, $3::SMALLINT),
    open_kinds = (COALESCE(cs.open_kinds, 0) | (1 << $3::INT))::SMALLINT
FROM campaigns c, subscribers s
WHERE c.uuid = $1::UUID AND s.uuid = $2::UUID
    AND cs.campaign_id = c.id AND cs.subscriber_id = s.id;

-- name: axm-mark-send-clicked
-- Same as axm-mark-send-opened, for a tracked link click.
UPDATE axm_campaign_sends cs SET
    clicked_at = COALESCE(cs.clicked_at, NOW()),
    last_clicked_at = NOW(),
    click_count = LEAST(COALESCE(cs.click_count, 0), 2147483646) + 1,
    click_kind = COALESCE(cs.click_kind, $3::SMALLINT),
    click_kinds = (COALESCE(cs.click_kinds, 0) | (1 << $3::INT))::SMALLINT
FROM campaigns c, subscribers s
WHERE c.uuid = $1::UUID AND s.uuid = $2::UUID
    AND cs.campaign_id = c.id AND cs.subscriber_id = s.id;

-- name: axm-get-campaign-sent-ids
-- The subscribers above ID $2 who were already sent campaign $1. A campaign that is
-- picked up again reads from its checkpoint and leaves these out, so that nobody
-- gets it twice (LST-20).
SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id > $2;

-- name: axm-count-campaign-sends
SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id = $1;

-- name: axm-delete-campaign-sends
-- Delete a batch and carry over its count atomically, without retaining recipient IDs.
WITH due AS MATERIALIZED (
    SELECT ctid, campaign_id FROM axm_campaign_sends WHERE sent_at < $1 LIMIT $2
),
locked AS MATERIALIZED (
    -- Lock the parent before its send records. Deleting a campaign takes the same
    -- locks in that order; let its cascade handle any records that are being deleted.
    SELECT c.id FROM campaigns c
    WHERE c.id IN (SELECT campaign_id FROM due)
    FOR KEY SHARE OF c SKIP LOCKED
),
d AS (
    DELETE FROM axm_campaign_sends s USING due, locked
    WHERE s.ctid = due.ctid AND s.campaign_id = locked.id
    RETURNING s.campaign_id, s.subscriber_id
),
expired AS (
    INSERT INTO axm_campaign_expired_sends AS tally (campaign_id, sent)
    SELECT d.campaign_id, COUNT(*) FROM d
    -- A surviving follow-up decision already records this delivery.
    WHERE NOT EXISTS (
        SELECT 1 FROM axm_campaign_followup_claims k
        WHERE k.campaign_id = d.campaign_id AND k.subscriber_id = d.subscriber_id AND k.sent
    )
    GROUP BY d.campaign_id
    ORDER BY d.campaign_id
    ON CONFLICT (campaign_id) DO UPDATE SET sent = tally.sent + EXCLUDED.sent
)
SELECT COUNT(*) FROM d;
