package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_11 adds the list trash: two tables (axm_deleted_lists, axm_deleted_list_subs) and two
// indexes. A backup role with per-object grants needs SELECT on both after this runs (see
// FORK.md). The list delete query, the triggers and the send engine are not touched; the
// application copies a list into the trash in the same transaction that deletes it.
func V6_2_1_axm_11(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM list trash. Keep this block identical to V6_2_1_axm_11.
-- Deleting a list keeps what it took with it for a while: the list row, who deleted it and
-- when, its subscriptions (subscriber ID, status, meta, dates) and the ids of the rows that
-- pointed at it (campaign links, list role rows, draw records). Restoring puts them back.
-- No e-mail or name is copied. A subscriber who is deleted leaves the trash with them.
CREATE TABLE IF NOT EXISTS axm_deleted_lists (
    list_id              INTEGER PRIMARY KEY,
    uuid                 UUID NOT NULL,
    name                 TEXT NOT NULL,
    list                 JSONB NOT NULL,
    sub_count            INTEGER NOT NULL DEFAULT 0,
    deleted_by           INTEGER NULL,
    deleted_by_username  TEXT NOT NULL DEFAULT '',
    deleted_by_name      TEXT NOT NULL DEFAULT '',
    deleted_at           TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    campaign_links       INTEGER[] NOT NULL DEFAULT '{}',
    roles                JSONB NOT NULL DEFAULT '[]',
    draws                INTEGER[] NOT NULL DEFAULT '{}',
    source_draws         INTEGER[] NOT NULL DEFAULT '{}',
    draw_campaigns       INTEGER[] NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_axm_deleted_lists_at ON axm_deleted_lists (deleted_at);

CREATE TABLE IF NOT EXISTS axm_deleted_list_subs (
    list_id          INTEGER NOT NULL REFERENCES axm_deleted_lists(list_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id    INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    status           subscription_status NOT NULL,
    meta             JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMP WITH TIME ZONE NULL,
    updated_at       TIMESTAMP WITH TIME ZONE NULL,
    PRIMARY KEY (list_id, subscriber_id)
);
-- The lookup that deleting a subscriber cascades through.
CREATE INDEX IF NOT EXISTS idx_axm_deleted_list_subs_sub ON axm_deleted_list_subs (subscriber_id);
-- End of AXM list trash.`)
	return err
}
