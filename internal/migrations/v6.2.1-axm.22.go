package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_22 preserves the finished exclusion count independently of subscriber deletion.
func V6_2_1_axm_22(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	// Keep the schema lock held only for the column addition. Combining the ALTER and
	// backfill in one multi-statement Exec retains its ACCESS EXCLUSIVE lock while the
	// backfill reads campaigns; a live sender can then hold a campaign row and wait for
	// the rule table, forming a lock cycle. Each Exec autocommits before the next step.
	if _, err := db.Exec(`ALTER TABLE axm_campaign_recent_rules ADD COLUMN IF NOT EXISTS finished_held_count INTEGER`); err != nil {
		return err
	}
	if _, err := db.Exec(`-- Existing finished campaigns can only be backfilled from their remaining holds.
UPDATE axm_campaign_recent_rules r
SET finished_held_count = (SELECT COUNT(*) FROM axm_campaign_recent_holds h WHERE h.campaign_id = r.campaign_id)
FROM campaigns c
WHERE c.id = r.campaign_id AND c.status = 'finished' AND r.finished_held_count IS NULL`); err != nil {
		return err
	}
	_, err := db.Exec(`-- Save the aggregate with the status change. The subscriber-specific holds can still cascade.
CREATE OR REPLACE FUNCTION axm_snapshot_recent_holds()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE axm_campaign_recent_rules r
    SET finished_held_count = (SELECT COUNT(*) FROM axm_campaign_recent_holds h WHERE h.campaign_id = NEW.id)
    WHERE r.campaign_id = NEW.id;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_recent_holds_finished ON campaigns;
CREATE TRIGGER axm_recent_holds_finished AFTER UPDATE OF status ON campaigns
    FOR EACH ROW WHEN (NEW.status = 'finished' AND OLD.status IS DISTINCT FROM 'finished')
    EXECUTE FUNCTION axm_snapshot_recent_holds();`)
	return err
}
