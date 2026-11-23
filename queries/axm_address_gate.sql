-- name: axm-address-facts
-- What the database knows about each address of a batch (the gate at LST-56): whether it was
-- blocklisted before (axm_suppressed_emails) and whether a hard bounce or a complaint is on
-- record (axm_bounced_emails). The addresses are lowercase and trimmed.
SELECT e AS email,
    EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(e)) AS suppressed,
    EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash(e)) AS bounced
FROM UNNEST($1::TEXT[]) AS e;

-- name: axm-bounced-record
-- Remember the address that has just bounced for good ($3: hard or complaint). The subscriber is
-- found by UUID or by e-mail as record-bounce finds it; the address is the e-mail the bounce
-- names ($2), or the subscriber's own when it names none: a subscriber edited since the mail
-- was sent has another address now, and the bounce is for the one it was sent to. It runs in
-- the transaction of record-bounce, before it (which can delete the subscriber).
INSERT INTO axm_bounced_emails (email_hash, kind, hits, last_at, subscriber_id)
SELECT axm_email_hash(COALESCE(NULLIF($2, ''), email)), $3::TEXT, 1, NOW(), id FROM subscribers
WHERE CASE WHEN $1 != '' THEN uuid = $1::UUID ELSE email = $2 END
ON CONFLICT (email_hash) DO UPDATE SET hits = axm_bounced_emails.hits + 1, last_at = NOW(),
    subscriber_id = EXCLUDED.subscriber_id,
    kind = CASE WHEN EXCLUDED.kind = 'complaint' THEN 'complaint' ELSE axm_bounced_emails.kind END;
