package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_20 retains aggregate exclusions when a subscriber is deleted before the campaign ends.
func V6_2_1_axm_20(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM recent-send counts after subscriber deletion (LST-65). Keep this block identical to V6_2_1_axm_20.
ALTER TABLE axm_campaign_recent_rules ADD COLUMN IF NOT EXISTS removed_held_count INTEGER NOT NULL DEFAULT 0;

-- Keep only the count when subscriber deletion cascades to the holds.
CREATE OR REPLACE FUNCTION axm_count_removed_recent_holds()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    WITH removed AS (
        SELECT campaign_id, COUNT(*) AS n FROM removed_holds GROUP BY campaign_id
    ), locked AS (
        SELECT r.campaign_id, removed.n
        FROM axm_campaign_recent_rules r JOIN removed USING (campaign_id)
        ORDER BY r.campaign_id
        FOR UPDATE OF r
    )
    UPDATE axm_campaign_recent_rules r
    SET removed_held_count = r.removed_held_count + locked.n
    FROM locked WHERE r.campaign_id = locked.campaign_id;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_recent_holds_removed ON axm_campaign_recent_holds;
CREATE TRIGGER axm_recent_holds_removed AFTER DELETE ON axm_campaign_recent_holds
    REFERENCING OLD TABLE AS removed_holds FOR EACH STATEMENT
    EXECUTE FUNCTION axm_count_removed_recent_holds();

CREATE OR REPLACE FUNCTION axm_snapshot_recent_holds()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    held_count INTEGER;
BEGIN
    -- Read both counts from one snapshot, even when a subscriber is being deleted concurrently.
    SELECT r.removed_held_count + (SELECT COUNT(*) FROM axm_campaign_recent_holds h WHERE h.campaign_id = NEW.id)
    INTO held_count FROM axm_campaign_recent_rules r WHERE r.campaign_id = NEW.id;
    IF FOUND THEN
        UPDATE axm_campaign_recent_rules SET finished_held_count = held_count WHERE campaign_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
-- End of AXM recent-send counts after subscriber deletion.`)
	return err
}
