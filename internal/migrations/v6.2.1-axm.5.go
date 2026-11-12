package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_5 adds the campaign send records table (who was sent which campaign and when,
// with the opens and clicks that came back). It only adds a table and its indexes.
// The table has no sequence. A backup role with per-object grants needs
// GRANT SELECT ON axm_campaign_sends after this runs (see FORK.md).
func V6_2_1_axm_5(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM campaign send records. Keep this block identical to V6_2_1_axm_5.
-- One row per campaign message that a messenger accepted: who got which campaign and when,
-- and what came back for it. A row is written only after a successful send, so a missing
-- row means the message did not go out (or its record was lost), never the reverse.
-- open_kind / click_kind hold the client class (models.AgentKind*) of the first request;
-- open_kinds / click_kinds are bitmasks (1 << kind) of every class seen. The raw
-- user-agent and the IP address are never stored. Reaction columns stay NULL until the
-- first open or click.
CREATE TABLE IF NOT EXISTS axm_campaign_sends (
    campaign_id      INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id    INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    sent_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    opened_at        TIMESTAMP WITH TIME ZONE NULL,
    last_opened_at   TIMESTAMP WITH TIME ZONE NULL,
    open_count       INTEGER NULL,
    open_kind        SMALLINT NULL,
    open_kinds       SMALLINT NULL,

    clicked_at       TIMESTAMP WITH TIME ZONE NULL,
    last_clicked_at  TIMESTAMP WITH TIME ZONE NULL,
    click_count      INTEGER NULL,
    click_kind       SMALLINT NULL,
    click_kinds      SMALLINT NULL,

    PRIMARY KEY (campaign_id, subscriber_id)
);
-- Per-subscriber history, and the lookup that deleting a subscriber cascades through.
CREATE INDEX IF NOT EXISTS idx_axm_sends_sub ON axm_campaign_sends(subscriber_id, sent_at DESC);
-- Time ranges (recent sends, retention). Rows arrive in sent_at order, so BRIN stays tiny.
CREATE INDEX IF NOT EXISTS idx_axm_sends_sent_at ON axm_campaign_sends USING BRIN (sent_at);
-- End of AXM campaign send records.`)
	return err
}
