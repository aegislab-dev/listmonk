package migrations

import (
	"log"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

const axm22SQL = `-- AXM completed recent-send counts (LST-65). Keep this block identical to V6_2_1_axm_22.
ALTER TABLE axm_campaign_recent_rules ADD COLUMN IF NOT EXISTS finished_held_count INTEGER;

-- AXM-22 AUTOCOMMIT BOUNDARY

-- Existing campaigns can be backfilled from their remaining holds. The value is used only for
-- finished campaigns; active campaigns keep reading the live holds count, and the finish trigger
-- below replaces this value when they finish.
UPDATE axm_campaign_recent_rules r
SET finished_held_count = (SELECT COUNT(*) FROM axm_campaign_recent_holds h WHERE h.campaign_id = r.campaign_id)
WHERE r.finished_held_count IS NULL AND EXISTS (
    SELECT 1 FROM axm_campaign_recent_holds h WHERE h.campaign_id = r.campaign_id
);

-- AXM-22 AUTOCOMMIT BOUNDARY

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
-- End of AXM completed recent-send counts.`

// V6_2_1_axm_22 preserves the finished exclusion count independently of subscriber deletion.
func V6_2_1_axm_22(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	// Add the column, backfill rule/hold rows, and install the campaign trigger in separate
	// autocommit statements. Never read campaigns while a schema lock on the rule table is held:
	// a sender locks its campaign before scanning its rule, so the opposite lock order can cycle.
	for _, statement := range strings.Split(axm22SQL, "\n-- AXM-22 AUTOCOMMIT BOUNDARY\n") {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
