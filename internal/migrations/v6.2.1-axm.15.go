package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_15 adds the team's list of From addresses (empty = free input).
func V6_2_1_axm_15(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('axm.from_addresses', '[]')
 ON CONFLICT (key) DO NOTHING;`)
	return err
}
