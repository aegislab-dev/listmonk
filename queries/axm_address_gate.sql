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
-- trigger on bounces: the address the bounce gives ($2), else the subscriber's ($1: UUID) when it
-- is surely the one the mail of the campaign the bounce names ($3) went to (axm_mailed_email).
-- Otherwise none, and no address is kept.
SELECT set_config('axm.bounce_email', COALESCE(NULLIF(lower(btrim($2)), ''), (
    SELECT axm_mailed_email(s.id, (SELECT c.id FROM campaigns c WHERE c.uuid = NULLIF($3, '')::UUID))
    FROM subscribers s WHERE s.uuid = NULLIF($1, '')::UUID
), ''), true);

-- name: axm-bounced-unless-recorded
-- A hard bounce or a complaint that left no bounce record of its own (the subscriber was
-- blocklisted already, or over the count of the bounce action) still keeps the address named
-- above, for that subscriber ($1: UUID, else $2: address, as record-bounce finds them): it goes
-- with their bounce records. Once per address and subscriber.
INSERT INTO axm_bounced_emails (email_hash, kind, subscriber_id)
SELECT axm_email_hash(v.email), $3::TEXT, (SELECT id FROM subscribers WHERE CASE WHEN $1 != '' THEN uuid = $1::UUID ELSE email = $2 END)
FROM (SELECT NULLIF(current_setting('axm.bounce_email', true), '') AS email) v
WHERE v.email IS NOT NULL
    AND COALESCE(current_setting('axm.bounce_recorded', true), '') <> '1'
    AND NOT EXISTS (SELECT 1 FROM axm_bounced_emails b WHERE b.email_hash = axm_email_hash(v.email)
        AND b.subscriber_id IS NOT DISTINCT FROM (SELECT id FROM subscribers WHERE CASE WHEN $1 != '' THEN uuid = $1::UUID ELSE email = $2 END));

-- name: axm-bounced-orphan
-- A hard bounce or a complaint for a subscriber that is not there (gone, or their address changed):
-- the address the bounce gives is kept all the same. When a subscriber has that address now, it
-- goes with their bounce records like the one above. Once per address and subscriber.
INSERT INTO axm_bounced_emails (email_hash, kind, subscriber_id)
SELECT axm_email_hash($1), $2::TEXT, (SELECT id FROM subscribers WHERE email = lower(btrim($1)))
WHERE NOT EXISTS (SELECT 1 FROM axm_bounced_emails b WHERE b.email_hash = axm_email_hash($1)
    AND b.subscriber_id IS NOT DISTINCT FROM (SELECT id FROM subscribers WHERE email = lower(btrim($1))));

-- name: axm-bounced-release-subscriber
-- A person deletes the bounce records of a subscriber ($1: ID, else $2: UUID): the addresses kept
-- for bounces of theirs that left no record go too (the trigger on bounces takes the others).
DELETE FROM axm_bounced_emails
WHERE bounce_id IS NULL AND subscriber_id = (SELECT id FROM subscribers WHERE CASE WHEN $1 > 0 THEN id = $1 ELSE uuid = $2 END);
