package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_9 keeps blocklisted addresses blocklisted for good: one table of address hashes
// (axm_suppressed_emails), one function to hash, five trigger functions and six triggers.
// A backup role with per-object grants needs SELECT on the new table after this runs (see
// FORK.md). No upstream query, campaign query or send engine code is touched.
func V6_2_1_axm_9(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM suppressed addresses. Keep this block identical to V6_2_1_axm_9.
-- Addresses that were blocklisted stay blocklisted when the subscriber row is deleted and the
-- address is added or imported again. Only a hash of the lowercased address is kept. The
-- table follows subscribers.status: it gets a row when a subscriber is blocklisted and loses
-- it when the subscriber is unblocked. Deleting a blocklisted subscriber leaves the row.
CREATE TABLE IF NOT EXISTS axm_suppressed_emails (
    email_hash       BYTEA PRIMARY KEY,
    reason           TEXT NOT NULL DEFAULT 'blocklist',
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION axm_email_hash(p_email TEXT)
RETURNS BYTEA LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT sha256(convert_to(lower(btrim(p_email)), 'UTF8'));
$$;

INSERT INTO axm_suppressed_emails (email_hash, reason)
    SELECT axm_email_hash(email), 'blocklist' FROM subscribers WHERE status = 'blocklisted'
    ON CONFLICT (email_hash) DO NOTHING;

-- A new subscriber with a suppressed address starts blocklisted.
CREATE OR REPLACE FUNCTION axm_suppress_new_subscriber()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(NEW.email)) THEN
        NEW.status = 'blocklisted';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_suppress_new_subscriber ON subscribers;
CREATE TRIGGER axm_suppress_new_subscriber BEFORE INSERT ON subscribers
    FOR EACH ROW EXECUTE FUNCTION axm_suppress_new_subscriber();

-- A subscriber whose e-mail is changed to a suppressed address becomes blocklisted, with every
-- subscription unsubscribed, as a new subscriber with that address would.
CREATE OR REPLACE FUNCTION axm_suppress_changed_email()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status != 'blocklisted'
        AND EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(NEW.email)) THEN
        NEW.status = 'blocklisted';
        UPDATE subscriber_lists SET status = 'unsubscribed', updated_at = NOW()
            WHERE subscriber_id = NEW.id AND status != 'unsubscribed';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_suppress_changed_email ON subscribers;
CREATE TRIGGER axm_suppress_changed_email BEFORE UPDATE OF email ON subscribers
    FOR EACH ROW WHEN (lower(btrim(NEW.email)) != lower(btrim(OLD.email))) EXECUTE FUNCTION axm_suppress_changed_email();

-- Keeps the table equal to the blocklisted addresses. An address is released only when its
-- subscriber is taken off the blocklist; editing the e-mail of a blocked subscriber keeps the
-- old address blocked and blocks the new one too.
CREATE OR REPLACE FUNCTION axm_track_blocklist()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status = 'blocklisted' AND NEW.status != 'blocklisted' THEN
        DELETE FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(OLD.email);
    END IF;
    IF NEW.status = 'blocklisted' THEN
        INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash(NEW.email))
            ON CONFLICT (email_hash) DO NOTHING;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_track_blocklist ON subscribers;
DROP TRIGGER IF EXISTS axm_track_blocklist_ins ON subscribers;
DROP TRIGGER IF EXISTS axm_track_blocklist_upd ON subscribers;
-- The WHEN clauses keep the function from running for the ordinary subscriber.
CREATE TRIGGER axm_track_blocklist_ins AFTER INSERT ON subscribers
    FOR EACH ROW WHEN (NEW.status = 'blocklisted') EXECUTE FUNCTION axm_track_blocklist();
CREATE TRIGGER axm_track_blocklist_upd AFTER UPDATE OF status, email ON subscribers
    FOR EACH ROW WHEN (OLD.status = 'blocklisted' OR NEW.status = 'blocklisted') EXECUTE FUNCTION axm_track_blocklist();

-- Deleting a blocklisted subscriber keeps its address suppressed.
CREATE OR REPLACE FUNCTION axm_keep_blocklist()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'blocklisted' THEN
        INSERT INTO axm_suppressed_emails (email_hash, reason) VALUES (axm_email_hash(OLD.email), 'delete')
            ON CONFLICT (email_hash) DO NOTHING;
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS axm_keep_blocklist ON subscribers;
CREATE TRIGGER axm_keep_blocklist BEFORE DELETE ON subscribers
    FOR EACH ROW EXECUTE FUNCTION axm_keep_blocklist();

-- A blocklisted subscriber is never subscribed to anything, whichever way the row arrives.
CREATE OR REPLACE FUNCTION axm_block_subscription()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM subscribers WHERE id = NEW.subscriber_id AND status = 'blocklisted') THEN
        NEW.status = 'unsubscribed';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_block_subscription ON subscriber_lists;
CREATE TRIGGER axm_block_subscription BEFORE INSERT OR UPDATE ON subscriber_lists
    FOR EACH ROW WHEN (NEW.status != 'unsubscribed') EXECUTE FUNCTION axm_block_subscription();
-- End of AXM suppressed addresses.`)
	return err
}
