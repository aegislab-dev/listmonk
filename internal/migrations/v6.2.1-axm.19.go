package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_19 preserves campaign totals when per-recipient send records expire.
func V6_2_1_axm_19(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM expired send totals (LST-67). Keep this block identical to V6_2_1_axm_19.
CREATE TABLE IF NOT EXISTS axm_campaign_expired_sends (
    campaign_id INT PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    sent BIGINT NOT NULL CHECK (sent > 0)
);
-- End of AXM expired send totals.`)
	return err
}
