package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56),
// filled from the bounce records there are (for the subscribers not edited since their last
// bounce: the records do not say which address they were for), and the trigger that releases an
// address when a person deletes the bounce records of its subscriber.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM bounced addresses (LST-56). Keep this block identical to V6_2_1_axm_16.
-- Addresses that had a hard bounce or a complaint stay known when the subscriber row is deleted
-- (the bounce rows go with it), so that a person cannot add them again. Only a hash of the
-- lowercased address is kept: the address the bounce was for, which is not the subscriber's
-- current one when it was edited since. subscriber_id is the subscriber whose bounce records
-- release the address (no foreign key: the row outlives the subscriber). RecordBounce writes
-- the row in the transaction that records the bounce; a person who deletes the bounce records
-- of a subscriber who still exists releases the address.
CREATE TABLE IF NOT EXISTS axm_bounced_emails (
    email_hash     BYTEA PRIMARY KEY,
    kind           TEXT NOT NULL,
    hits           INT NOT NULL DEFAULT 1,
    last_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    subscriber_id  INT NULL
);
CREATE INDEX IF NOT EXISTS idx_axm_bounced_sub ON axm_bounced_emails(subscriber_id);

-- The bounce records do not say which address they were for. A subscriber edited after its last
-- bounce may have another address now, so only the ones not edited since are taken in.
INSERT INTO axm_bounced_emails (email_hash, kind, hits, last_at, subscriber_id)
    SELECT axm_email_hash(s.email), MIN(b.type::TEXT), COUNT(*), MAX(b.created_at), s.id
    FROM bounces b JOIN subscribers s ON s.id = b.subscriber_id
    WHERE b.type IN ('hard', 'complaint')
    GROUP BY s.id, s.email, s.updated_at, s.created_at
    HAVING COALESCE(s.updated_at, s.created_at) <= MAX(b.created_at)
    ON CONFLICT (email_hash) DO NOTHING;

CREATE OR REPLACE FUNCTION axm_bounced_release()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    -- The subscriber is gone when the bounce rows are deleted with it: the address stays.
    IF EXISTS (SELECT 1 FROM subscribers WHERE id = OLD.subscriber_id) AND NOT EXISTS (
        SELECT 1 FROM bounces WHERE subscriber_id = OLD.subscriber_id AND type IN ('hard', 'complaint')
    ) THEN
        DELETE FROM axm_bounced_emails WHERE subscriber_id = OLD.subscriber_id;
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
