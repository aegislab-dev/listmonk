package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56), the
// trigger that fills it from the bounce records, the one that releases an address when a person
// deletes the bounce record, and the note of when a subscriber's address was last changed.
// Nothing is taken in from the bounce records there are: they do not say which address they
// were for, and a subscriber edited since may have another one now.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM bounced addresses (LST-56). Keep this block identical to V6_2_1_axm_16.
-- Addresses that had a hard bounce or a complaint stay known when the subscriber row is deleted
-- (the bounce rows go with it), so that a person cannot add them again. Only a hash of the
-- lowercased address is kept. One row per bounce record: bounce_id is the bounce it is for, and
-- the row goes when a person deletes that bounce (not when the subscriber is deleted, which
-- takes its bounce rows with it). A row with no bounce_id is a bounce for an address no
-- subscriber had any more: it is only removed by SQL (see FORK.md).
-- RecordBounce names the address the bounce is for in the setting axm.bounce_email of its
-- transaction: the address the bounce gives, or else the subscriber's own one, unless that was
-- changed in the last 14 days (the mail may have gone to the old address: then none is kept).
CREATE TABLE IF NOT EXISTS axm_bounced_emails (
    email_hash  BYTEA NOT NULL,
    kind        TEXT NOT NULL,
    bounce_id   INT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_axm_bounced_hash ON axm_bounced_emails(email_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_bounced_bounce ON axm_bounced_emails(bounce_id) WHERE bounce_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS axm_email_changes (
    subscriber_id  INTEGER PRIMARY KEY REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    changed_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION axm_note_email_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_email_changes (subscriber_id, changed_at) VALUES (NEW.id, NOW())
        ON CONFLICT (subscriber_id) DO UPDATE SET changed_at = NOW();
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_note_email_change ON subscribers;
CREATE TRIGGER axm_note_email_change AFTER UPDATE OF email ON subscribers
    FOR EACH ROW WHEN (OLD.email IS DISTINCT FROM NEW.email)
    EXECUTE FUNCTION axm_note_email_change();

CREATE OR REPLACE FUNCTION axm_bounced_record()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_email TEXT := NULLIF(current_setting('axm.bounce_email', true), '');
BEGIN
    IF v_email IS NOT NULL THEN
        INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id) VALUES (axm_email_hash(v_email), NEW.type::TEXT, NEW.id);
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
