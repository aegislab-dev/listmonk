package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the link between a campaign and its follow-up campaign (LST-47): a
// draft made from the people of a finished campaign who did not react to it.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM campaign follow-ups (LST-47). Keep this block identical to V6_2_1_axm_16.
CREATE TABLE IF NOT EXISTS axm_campaign_followups (
    campaign_id        INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    parent_campaign_id INTEGER NOT NULL UNIQUE REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    -- The follow-up list by its UUID: a list that goes to the trash and comes back has the same UUID.
    list_uuid          UUID NOT NULL,
    condition          JSONB NOT NULL,
    counts             JSONB NOT NULL DEFAULT '{}',
    recheck            JSONB NOT NULL DEFAULT '{}',
    -- People the check before sending holds back (subscriber, status before, updated_at after, reason).
    held               JSONB NOT NULL DEFAULT '[]',
    rechecked_at       TIMESTAMP WITH TIME ZONE NULL,
    created_by         INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CHECK (campaign_id <> parent_campaign_id)
);
-- End of AXM campaign follow-ups.`)
	return err
}
