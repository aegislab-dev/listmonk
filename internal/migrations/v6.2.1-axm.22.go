package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_22 preserves the finished exclusion count independently of subscriber deletion.
func V6_2_1_axm_22(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM completed recent-send counts (LST-65). Keep this block identical to V6_2_1_axm_22.
ALTER TABLE axm_campaign_recent_rules ADD COLUMN IF NOT EXISTS finished_held_count INTEGER;

-- Existing finished campaigns can only be backfilled from their remaining holds.
UPDATE axm_campaign_recent_rules r
SET finished_held_count = (SELECT COUNT(*) FROM axm_campaign_recent_holds h WHERE h.campaign_id = r.campaign_id)
FROM campaigns c
WHERE c.id = r.campaign_id AND c.status = 'finished' AND r.finished_held_count IS NULL;

-- Save the aggregate with the status change. The subscriber-specific holds can still cascade.
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
    EXECUTE FUNCTION axm_snapshot_recent_holds();
-- End of AXM completed recent-send counts.`)
	return err
}
