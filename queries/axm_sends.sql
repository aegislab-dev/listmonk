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
-- Deletes up to $2 send records sent before $1 and returns how many were deleted.
-- The caller repeats it until it returns 0, so no single statement holds many rows.
WITH d AS (
    DELETE FROM axm_campaign_sends WHERE ctid = ANY(ARRAY(
        SELECT ctid FROM axm_campaign_sends WHERE sent_at < $1 LIMIT $2
    )) RETURNING 1
)
SELECT COUNT(*) FROM d;
