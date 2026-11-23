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
-- trigger on bounces: the address the bounce gives ($2), else the address of the subscriber
-- ($1: UUID) unless it was changed in the last 14 days (the mail may have gone to the old one:
-- then none, and no address is kept). Returns what was set.
SELECT set_config('axm.bounce_email', COALESCE(NULLIF(lower(btrim($2)), ''), (
    SELECT s.email FROM subscribers s
    WHERE s.uuid = NULLIF($1, '')::UUID
        AND NOT EXISTS (SELECT 1 FROM axm_email_changes c WHERE c.subscriber_id = s.id AND c.changed_at > NOW() - INTERVAL '14 days')
), ''), true);

-- name: axm-bounced-orphan
-- A hard bounce or a complaint for an address no subscriber has (it was changed): kept all the same.
INSERT INTO axm_bounced_emails (email_hash, kind) VALUES (axm_email_hash($1), $2::TEXT);
