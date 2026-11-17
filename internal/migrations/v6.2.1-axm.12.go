package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_12 adds the subscriber-table column presets people make themselves: one table
// (axm_column_presets, with its id sequence) and a unique index on the lower-cased name.
// Lists keep storing only a preset ID in lists.axm_preset ('creator' and 'b2b' are fixed in
// the code, 'u<id>' is a row of this table), so no list changes. A backup role with
// per-object grants needs SELECT on the new table and sequence after this runs (see FORK.md).
func V6_2_1_axm_12(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM column presets. Keep this block identical to V6_2_1_axm_12.
-- Presets people make for the subscribers table: a name and the attribute keys to show, in order.
CREATE TABLE IF NOT EXISTS axm_column_presets (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    columns     TEXT[] NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_column_presets_name ON axm_column_presets (lower(name));
-- End of AXM column presets.`)
	return err
}
