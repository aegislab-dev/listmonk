package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56), the
// trigger that fills it from the bounce records, the one that releases an address when a person
// deletes the bounce record, and the record of when a subscriber's address changed (a bounce that
// does not say its address is only for the current one when it has not changed since the mail).
// The bounces recorded before are taken in by the address their webhook payload gives, or else
// by that same rule.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM bounced addresses (LST-56). Keep this block identical to V6_2_1_axm_16.
-- Addresses that had a hard bounce or a complaint stay known when the subscriber row is deleted
-- (the bounce rows go with it), so that a person cannot add them again. Only a hash of the
-- lowercased address is kept. A row with bounce_id is for that bounce record and goes when a
-- person deletes the record (not when the subscriber is deleted, which takes its bounce rows
-- with it). A row without bounce_id is a bounce that left no record of its own (the subscriber
-- was blocklisted already, or over the count of the bounce action): it goes with the last hard
-- bounce or complaint record of its subscriber_id, or when a person deletes that subscriber's
-- bounces. One with no subscriber at all (the address is nobody's any more) only goes by SQL
-- (FORK.md).
-- RecordBounce names the address the bounce is for in the setting axm.bounce_email of its
-- transaction: the address the bounce gives, or else axm_mailed_email. Failing to keep the
-- address never loses the bounce: the trigger only warns.
CREATE TABLE IF NOT EXISTS axm_bounced_emails (
    email_hash     BYTEA NOT NULL,
    kind           TEXT NOT NULL,
    bounce_id      INT NULL,
    subscriber_id  INT NULL,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_axm_bounced_hash ON axm_bounced_emails(email_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_bounced_bounce ON axm_bounced_emails(bounce_id) WHERE bounce_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_axm_bounced_sub ON axm_bounced_emails(subscriber_id) WHERE bounce_id IS NULL;

-- When a subscriber's address last changed: a bounce that does not say its address is for the
-- current one only when it has not changed since the mail. Before this table, an edit may have
-- changed it: the last edit stands for a change.
CREATE TABLE IF NOT EXISTS axm_email_changes (
    subscriber_id  INT PRIMARY KEY REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    changed_at     TIMESTAMP WITH TIME ZONE NOT NULL
);
INSERT INTO axm_email_changes (subscriber_id, changed_at)
    SELECT id, updated_at FROM subscribers WHERE updated_at > created_at
    ON CONFLICT (subscriber_id) DO NOTHING;

CREATE OR REPLACE FUNCTION axm_email_changed()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_email_changes (subscriber_id, changed_at) VALUES (NEW.id, NOW())
        ON CONFLICT (subscriber_id) DO UPDATE SET changed_at = EXCLUDED.changed_at;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_email_changed ON subscribers;
CREATE TRIGGER axm_email_changed AFTER UPDATE OF email ON subscribers
    FOR EACH ROW WHEN (OLD.email IS DISTINCT FROM NEW.email)
    EXECUTE FUNCTION axm_email_changed();

-- The address a mail of the campaign (NULL: an opt-in or a transactional mail, or not known)
-- went to, when it is surely the subscriber's current one: it never changed, or it changed more
-- than an hour (a send in progress) before the recorded send of that campaign to them. NULL
-- otherwise: a send record that is gone (retention) proves nothing.
CREATE OR REPLACE FUNCTION axm_mailed_email(p_subscriber INT, p_campaign INT)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT s.email FROM subscribers s
    WHERE s.id = p_subscriber AND NOT EXISTS (
        SELECT 1 FROM axm_email_changes c WHERE c.subscriber_id = s.id AND c.changed_at >= COALESCE(
            (SELECT x.sent_at - INTERVAL '1 hour' FROM axm_campaign_sends x
                WHERE x.campaign_id = p_campaign AND x.subscriber_id = s.id), '-infinity'))
$$;

-- The bounces recorded before: the webhooks say in their payload which address a bounce was for,
-- each in its own field. SES names the bounced (complaining) recipients apart from the mail's
-- recipients: the first of them, else the mail's only recipient. SendGrid's payload is the whole
-- batch of events, and the bounce is the event of its time, type and campaign; when several
-- addresses match, the subscriber's own one if it did not change since the bounce (the bounce
-- found the subscriber by it), else none. The others are taken in by axm_mailed_email.
INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
    SELECT axm_email_hash(a.addr), b.type::TEXT, b.id, b.subscriber_id
    FROM bounces b
    CROSS JOIN LATERAL (SELECT
        (SELECT c.uuid::TEXT FROM campaigns c WHERE c.id = b.campaign_id) AS camp,
        (SELECT s.email FROM subscribers s WHERE s.id = b.subscriber_id AND NOT EXISTS (
            SELECT 1 FROM axm_email_changes x WHERE x.subscriber_id = s.id AND x.changed_at >= b.created_at)) AS own) k
    CROSS JOIN LATERAL (SELECT COALESCE(CASE b.source
        WHEN 'postmark' THEN NULLIF(b.meta->>'Email', '')
        WHEN 'forwardemail' THEN NULLIF(b.meta->>'recipient', '')
        WHEN 'lettermint' THEN NULLIF(b.meta->'data'->>'recipient', '')
        WHEN 'azure' THEN NULLIF(b.meta->'data'->>'recipient', '')
        WHEN 'ses' THEN COALESCE(
            (SELECT lower(btrim(r->>'emailAddress'))
                FROM jsonb_array_elements(CASE WHEN jsonb_typeof(b.meta->'bounce'->'bouncedRecipients') = 'array'
                    THEN b.meta->'bounce'->'bouncedRecipients' ELSE '[]'::JSONB END) WITH ORDINALITY AS t(r, n)
                WHERE COALESCE(btrim(r->>'emailAddress'), '') <> '' ORDER BY n LIMIT 1),
            (SELECT lower(btrim(r->>'emailAddress'))
                FROM jsonb_array_elements(CASE WHEN jsonb_typeof(b.meta->'complaint'->'complainedRecipients') = 'array'
                    THEN b.meta->'complaint'->'complainedRecipients' ELSE '[]'::JSONB END) WITH ORDINALITY AS t(r, n)
                WHERE COALESCE(btrim(r->>'emailAddress'), '') <> '' ORDER BY n LIMIT 1),
            CASE WHEN jsonb_typeof(b.meta->'mail'->'destination') = 'array' AND jsonb_array_length(b.meta->'mail'->'destination') = 1
                THEN NULLIF(b.meta->'mail'->'destination'->>0, '') END)
        WHEN 'sendgrid' THEN (
            SELECT CASE WHEN COUNT(DISTINCT m.addr) = 1 THEN MIN(m.addr)
                WHEN bool_or(m.addr = k.own) THEN k.own END
            FROM (SELECT lower(btrim(e->>'email')) AS addr
                FROM jsonb_array_elements(CASE WHEN jsonb_typeof(b.meta) = 'array' THEN b.meta ELSE '[]'::JSONB END) e
                WHERE e->>'event' = 'bounce' AND COALESCE(btrim(e->>'email'), '') <> ''
                    AND e->>'timestamp' = EXTRACT(EPOCH FROM b.created_at)::BIGINT::TEXT
                    AND (CASE WHEN e->>'bounce_classification' IN ('technical', 'content') THEN 'soft' ELSE 'hard' END) = b.type::TEXT
                    AND (k.camp IS NULL OR lower(COALESCE(e->>'XListmonkCampaign', '')) = k.camp)) m)
        END, axm_mailed_email(b.subscriber_id, b.campaign_id)) AS addr) a
    WHERE b.type IN ('hard', 'complaint') AND a.addr LIKE '%@%'
    ON CONFLICT (bounce_id) WHERE bounce_id IS NOT NULL DO NOTHING;

CREATE OR REPLACE FUNCTION axm_bounced_record()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_email TEXT := NULLIF(current_setting('axm.bounce_email', true), '');
BEGIN
    IF v_email IS NOT NULL THEN
        BEGIN
            INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
                VALUES (axm_email_hash(v_email), NEW.type::TEXT, NEW.id, NEW.subscriber_id);
            PERFORM set_config('axm.bounce_recorded', '1', true);
        EXCEPTION WHEN OTHERS THEN
            -- The bounce itself must not be lost for the sake of the address.
            RAISE WARNING 'axm_bounced_record: %', SQLERRM;
        END;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_bounced_record ON bounces;
CREATE TRIGGER axm_bounced_record AFTER INSERT ON bounces
    FOR EACH ROW WHEN (NEW.type IN ('hard', 'complaint'))
    EXECUTE FUNCTION axm_bounced_record();

CREATE OR REPLACE FUNCTION axm_bounced_release()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    -- The subscriber is gone when the bounce rows are deleted with it: the address stays.
    IF EXISTS (SELECT 1 FROM subscribers WHERE id = OLD.subscriber_id) THEN
        DELETE FROM axm_bounced_emails WHERE bounce_id = OLD.id;
        IF NOT EXISTS (SELECT 1 FROM bounces WHERE subscriber_id = OLD.subscriber_id AND type IN ('hard', 'complaint')) THEN
            DELETE FROM axm_bounced_emails WHERE subscriber_id = OLD.subscriber_id AND bounce_id IS NULL;
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_bounced_release ON bounces;
CREATE TRIGGER axm_bounced_release AFTER DELETE ON bounces
    FOR EACH ROW WHEN (OLD.type IN ('hard', 'complaint'))
    EXECUTE FUNCTION axm_bounced_release();
-- End of AXM bounced addresses.`)
	return err
}
