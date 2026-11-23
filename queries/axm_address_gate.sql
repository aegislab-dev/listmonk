-- name: axm-address-facts
-- What the database knows about each address of a batch (the gate at LST-56): whether it was
-- blocklisted before (axm_suppressed_emails) and whether a hard bounce or a complaint is on
-- record (axm_bounced_emails). The addresses are lowercase and trimmed.
SELECT e AS email,
    EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(e)) AS suppressed,
    EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash(e)) AS bounced
FROM UNNEST($1::TEXT[]) AS e;

-- name: axm-bounced-record
-- Remember the address of the subscriber who has just bounced for good ($3: hard or
-- complaint), found by UUID or by e-mail as record-bounce finds it. It runs before
-- record-bounce, which can delete the subscriber.
INSERT INTO axm_bounced_emails (email_hash, kind, hits, last_at)
SELECT axm_email_hash(email), $3::TEXT, 1, NOW() FROM subscribers
WHERE CASE WHEN $1 != '' THEN uuid = $1::UUID ELSE email = $2 END
ON CONFLICT (email_hash) DO UPDATE SET hits = axm_bounced_emails.hits + 1, last_at = NOW(),
    kind = CASE WHEN EXCLUDED.kind = 'complaint' THEN 'complaint' ELSE axm_bounced_emails.kind END;
