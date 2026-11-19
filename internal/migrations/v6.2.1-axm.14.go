package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_14 adds the domains that may receive a campaign test message without being
// subscribers (LST-41). The setting starts as a copy of the sender domains.
func V6_2_1_axm_14(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`INSERT INTO settings (key, value)
SELECT 'axm.test_domains', COALESCE((SELECT value FROM settings WHERE key = 'axm.sender_domains'), '[]'::JSONB)
ON CONFLICT (key) DO NOTHING;`)
	return err
}
