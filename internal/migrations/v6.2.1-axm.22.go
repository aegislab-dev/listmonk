package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_22 preserves the count of existing recipients when their subscriber data is deleted.
func V6_2_1_axm_22(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM delivered audience after subscriber deletion (LST-65). Keep this block identical to V6_2_1_axm_22.
ALTER TABLE axm_campaign_recent_rules ADD COLUMN IF NOT EXISTS removed_sent_count INTEGER NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION axm_count_removed_recent_sends()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    WITH removed AS (
        -- A subscriber's FK cascade removes the record after the subscriber is gone.
        -- Retention and other ledger-only deletes must not be counted as lost recipients.
        SELECT s.campaign_id, COUNT(*) AS n
        FROM removed_sends s
        JOIN axm_campaign_recent_rules r ON r.campaign_id = s.campaign_id
        WHERE NOT EXISTS (SELECT 1 FROM subscribers WHERE id = s.subscriber_id)
        GROUP BY s.campaign_id
    ), locked AS (
        SELECT r.campaign_id, removed.n
        FROM axm_campaign_recent_rules r JOIN removed USING (campaign_id)
        ORDER BY r.campaign_id
        FOR UPDATE OF r
    )
    UPDATE axm_campaign_recent_rules r
    SET removed_sent_count = r.removed_sent_count + locked.n
    FROM locked WHERE r.campaign_id = locked.campaign_id;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_recent_sends_removed ON axm_campaign_sends;
CREATE TRIGGER axm_recent_sends_removed AFTER DELETE ON axm_campaign_sends
    REFERENCING OLD TABLE AS removed_sends FOR EACH STATEMENT
    EXECUTE FUNCTION axm_count_removed_recent_sends();
-- End of AXM delivered audience after subscriber deletion.`)
	return err
}
