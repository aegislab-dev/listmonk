package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_21 exposes the retained send IDs for rebuilding a resumed campaign's audience.
func V6_2_1_axm_21(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM recent-send audience on resume (LST-65). Keep this block identical to V6_2_1_axm_21.
-- Losing the ledger table at runtime must not break campaign discovery.
CREATE OR REPLACE FUNCTION axm_recent_campaign_sent_ids(p_campaign INT)
RETURNS TABLE(subscriber_id INT) LANGUAGE plpgsql STABLE AS $$
BEGIN
    RETURN QUERY SELECT s.subscriber_id FROM axm_campaign_sends s WHERE s.campaign_id = p_campaign;
EXCEPTION WHEN undefined_table THEN
    RETURN;
END;
$$;
-- End of AXM recent-send audience on resume.`)
	return err
}
