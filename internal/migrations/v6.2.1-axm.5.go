package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_5 gives every list a column preset for the subscribers table. It only adds a
// column to lists; existing lists get the default preset and no other data changes.
func V6_2_1_axm_5(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`ALTER TABLE lists ADD COLUMN IF NOT EXISTS axm_preset TEXT NOT NULL DEFAULT 'creator';`)
	return err
}
