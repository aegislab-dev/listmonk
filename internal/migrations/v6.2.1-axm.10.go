package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_10 adds the audit log: one table (axm_audit_log, with a BIGSERIAL sequence) and four
// indexes. A backup role with per-object grants needs SELECT on the table and on the sequence
// axm_audit_log_id_seq after this runs (see FORK.md). No existing table, query or trigger is
// touched.
func V6_2_1_axm_10(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM audit log. Keep this block identical to V6_2_1_axm_10.
-- One row per action somebody (or the server) took that changes lists, subscribers, imports,
-- campaigns, roles, users or settings. Who is copied in (user_id has no foreign key) so a
-- row outlives the account. No subscriber e-mail, name or attribute value is ever stored:
-- a subscriber is an ID or a count. Rows are only added; the screen and the API read them,
-- and the daily job deletes the ones past the retention period.
CREATE TABLE IF NOT EXISTS axm_audit_log (
    id               BIGSERIAL PRIMARY KEY,
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    user_id          INTEGER NULL,
    username         TEXT NOT NULL DEFAULT '',
    user_name        TEXT NOT NULL DEFAULT '',
    user_type        TEXT NOT NULL DEFAULT '',
    action           TEXT NOT NULL,
    target_type      TEXT NOT NULL DEFAULT '',
    target_id        INTEGER NULL,
    target_name      TEXT NOT NULL DEFAULT '',
    count            INTEGER NULL,
    detail           JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_axm_audit_created ON axm_audit_log (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_axm_audit_action ON axm_audit_log (action);
CREATE INDEX IF NOT EXISTS idx_axm_audit_user ON axm_audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_axm_audit_target ON axm_audit_log (target_type, target_id);
-- End of AXM audit log.`)
	return err
}
