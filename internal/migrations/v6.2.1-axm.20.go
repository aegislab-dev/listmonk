package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_20 preserves campaign totals and marks campaigns that could have lost earlier history.
func V6_2_1_axm_20(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM preserve removed send totals (LST-67). Keep this block identical to V6_2_1_axm_20.
-- Only backfill once. Campaigns first sent after this migration have a complete tally.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'campaigns'::regclass
        AND attname = 'axm_legacy_send_history' AND NOT attisdropped) THEN
        ALTER TABLE campaigns ADD COLUMN axm_legacy_send_history BOOLEAN NOT NULL DEFAULT FALSE;
        UPDATE campaigns SET axm_legacy_send_history = TRUE
        WHERE started_at IS NOT NULL OR sent > 0 OR last_subscriber_id > 0;
    END IF;
END $$;

-- Lock parent campaigns before subscriber deletion cascades to their ledger rows.
-- Campaign deletion takes the parent lock first as well.
CREATE OR REPLACE FUNCTION axm_lock_send_campaigns_on_subscriber_delete()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM c.id FROM axm_campaign_sends s
    JOIN campaigns c ON c.id = s.campaign_id
    WHERE s.subscriber_id = OLD.id
    ORDER BY c.id
    FOR KEY SHARE OF c;
    RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION axm_count_removed_campaign_sends()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_campaign_expired_sends AS tally (campaign_id, sent)
    SELECT r.campaign_id, COUNT(*)
    FROM removed_sends r
    JOIN campaigns c ON c.id = r.campaign_id
    -- When the campaign itself was deleted it has no parent row. A surviving
    -- follow-up decision already records the delivery, so skip that as well.
    WHERE NOT EXISTS (
        SELECT 1 FROM axm_campaign_followup_claims k
        WHERE k.campaign_id = r.campaign_id AND k.subscriber_id = r.subscriber_id AND k.sent
    )
    GROUP BY r.campaign_id
    ORDER BY r.campaign_id
    ON CONFLICT (campaign_id) DO UPDATE SET sent = tally.sent + EXCLUDED.sent;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS axm_lock_send_campaigns_on_subscriber_delete ON subscribers;
CREATE TRIGGER axm_lock_send_campaigns_on_subscriber_delete
BEFORE DELETE ON subscribers FOR EACH ROW EXECUTE FUNCTION axm_lock_send_campaigns_on_subscriber_delete();

DROP TRIGGER IF EXISTS axm_count_removed_campaign_sends ON axm_campaign_sends;
CREATE TRIGGER axm_count_removed_campaign_sends
AFTER DELETE ON axm_campaign_sends REFERENCING OLD TABLE AS removed_sends
FOR EACH STATEMENT EXECUTE FUNCTION axm_count_removed_campaign_sends();
-- End of AXM preserve removed send totals.`)
	return err
}
