package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56), the
// trigger that fills it from the bounce records, and the one that releases an address when a
// person deletes the bounce record. The bounces the webhooks recorded before name their address
// in their payload and are taken in; the others (the mailbox collector's) do not, and a
// subscriber edited since may have another address now, so they are not.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM bounced addresses (LST-56). Keep this block identical to V6_2_1_axm_16.
-- Addresses that had a hard bounce or a complaint stay known when the subscriber row is deleted
-- (the bounce rows go with it), so that a person cannot add them again. Only a hash of the
-- lowercased address is kept. One row per bounce record: bounce_id is the bounce it is for, and
-- the row goes when a person deletes that bounce (not when the subscriber is deleted, which
-- takes its bounce rows with it). A row with no bounce_id is a bounce that left no record (the
-- subscriber was blocklisted, or has another address now): it is only removed by SQL (FORK.md).
-- RecordBounce names the address the bounce is for in the setting axm.bounce_email of its
-- transaction: the address the bounce gives, or else the subscriber's own one when it can
-- not have changed since the last mail (not edited since). Failing to keep the address never
-- loses the bounce: the trigger only warns.
CREATE TABLE IF NOT EXISTS axm_bounced_emails (
    email_hash  BYTEA NOT NULL,
    kind        TEXT NOT NULL,
    bounce_id   INT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_axm_bounced_hash ON axm_bounced_emails(email_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_bounced_bounce ON axm_bounced_emails(bounce_id) WHERE bounce_id IS NOT NULL;

-- The bounces the webhooks recorded say in their payload which address they were for
-- (Postmark, SendGrid: an object or a list of events, ForwardEmail, Lettermint, SES).
INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id)
    SELECT axm_email_hash(a.addr), b.type::TEXT, b.id
    FROM bounces b
    CROSS JOIN LATERAL (SELECT COALESCE(NULLIF(b.meta->>'Email', ''), NULLIF(b.meta->>'email', ''),
        NULLIF(b.meta->>'recipient', ''), NULLIF(b.meta->'data'->>'recipient', ''),
        NULLIF(b.meta->0->>'email', ''), NULLIF(b.meta->'mail'->'destination'->>0, '')) AS addr) a
    WHERE b.type IN ('hard', 'complaint') AND a.addr LIKE '%@%'
    ON CONFLICT (bounce_id) WHERE bounce_id IS NOT NULL DO NOTHING;

CREATE OR REPLACE FUNCTION axm_bounced_record()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_email TEXT := NULLIF(current_setting('axm.bounce_email', true), '');
BEGIN
    IF v_email IS NOT NULL THEN
        BEGIN
            INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id) VALUES (axm_email_hash(v_email), NEW.type::TEXT, NEW.id);
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
