-- name: axm-address-facts
-- What the database knows about each address of a batch (the gate at LST-56): whether it was
-- blocklisted before (axm_suppressed_emails) and whether a hard bounce or a complaint is on
-- record (axm_bounced_emails). The addresses are lowercase and trimmed.
SELECT e AS email,
    EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(e)) AS suppressed,
    EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash(e)) AS bounced
FROM UNNEST($1::TEXT[]) AS e;

-- name: axm-bounce-address
-- Names the address a bounce is for in the setting axm.bounce_email of the transaction, for the
-- trigger on bounces: the address the bounce gives ($2), else the address of the subscriber ($1:
-- UUID) when it can not have changed since the mail it is for: not edited since the mail of the
-- campaign the bounce names ($3), or, when it names none (or none was sent to them), since the
-- first mail ever sent to them. Otherwise none, and no address is kept.
SELECT set_config('axm.bounce_email', COALESCE(NULLIF(lower(btrim($2)), ''), (
    SELECT s.email FROM subscribers s
    WHERE s.uuid = NULLIF($1, '')::UUID
        AND COALESCE(s.updated_at, s.created_at) <= COALESCE(
            (SELECT x.sent_at FROM axm_campaign_sends x JOIN campaigns c ON c.id = x.campaign_id
                WHERE c.uuid = NULLIF($3, '')::UUID AND x.subscriber_id = s.id),
            (SELECT MIN(x.sent_at) FROM axm_campaign_sends x WHERE x.subscriber_id = s.id))
), ''), true);

-- name: axm-bounced-orphan
-- A hard bounce or a complaint for an address no subscriber has any more (it was changed): kept
-- all the same, unless it is kept already. An address a subscriber has is that subscriber's own
-- business (blocklisted, bounce records) and would be kept for good here.
INSERT INTO axm_bounced_emails (email_hash, kind)
SELECT axm_email_hash($1), $2::TEXT
WHERE NOT EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1))
    AND NOT EXISTS (SELECT 1 FROM subscribers WHERE email = lower(btrim($1)));

-- name: axm-bounced-unless-recorded
-- The same for a bounce that left no record of its own (the subscriber was already blocklisted):
-- unless the trigger on bounces has just kept the address.
INSERT INTO axm_bounced_emails (email_hash, kind)
SELECT axm_email_hash($1), $2::TEXT
WHERE COALESCE(current_setting('axm.bounce_recorded', true), '') <> '1'
    AND NOT EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1))
    AND NOT EXISTS (SELECT 1 FROM subscribers WHERE email = lower(btrim($1)));
