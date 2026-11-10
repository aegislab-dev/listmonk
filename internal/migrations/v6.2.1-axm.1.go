package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_1 adds the disabled-by-default campaign business footer setting.
func V6_2_1_axm_1(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`INSERT INTO settings (key, value) VALUES
 ('axm.business_footer', '{"enabled": false, "company_name": "", "representative": "", "registration_no": "", "address": "", "phone": "", "email": "", "extra": ""}') ON CONFLICT (key) DO NOTHING`)
	return err
}
