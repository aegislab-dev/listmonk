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
    rechecked_at       TIMESTAMP WITH TIME ZONE NULL,
    -- The follow-up's version (see axm_followup_touch) and the version its last check was made for.
    version            INTEGER NOT NULL DEFAULT 0,
    checked_version    INTEGER NULL,
    -- Set when opens and clicks stopped being recorded per subscriber while the follow-up was
    -- waiting: reactions in that time cannot be seen, so nobody's can be judged.
    tracking_gap       BOOLEAN NOT NULL DEFAULT FALSE,
    created_by         INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CHECK (campaign_id <> parent_campaign_id)
);
-- People the check before sending holds back from a follow-up campaign. The sender skips them;
-- their subscriptions are not changed. Each check replaces the rows of its campaign.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_holds (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    reason        SMALLINT NOT NULL,
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- A follow-up's version counts the changes that make an earlier check before sending stale.
-- Those are a status change other than the sender starting a due schedule (a pause and resume, a
-- schedule saved or taken back), a new send time, and a list added to, taken off or restored on
-- the campaign.
-- The sender only gives a follow-up back, and only reads recipients for it, while its last check
-- was made for the version it has now.
CREATE OR REPLACE FUNCTION axm_followup_touch()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'campaigns' THEN
        UPDATE axm_campaign_followups SET version = version + 1 WHERE campaign_id = NEW.id;
        RETURN NULL;
    END IF;
    -- campaign_lists: a row added, taken off, or pointed at another list (a list deleted to the
    -- trash and restored from it updates list_id).
    IF TG_OP IN ('DELETE', 'UPDATE') THEN
        UPDATE axm_campaign_followups SET version = version + 1 WHERE campaign_id = OLD.campaign_id;
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.campaign_id IS DISTINCT FROM OLD.campaign_id) THEN
        UPDATE axm_campaign_followups SET version = version + 1 WHERE campaign_id = NEW.campaign_id;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_followup_touch_campaign ON campaigns;
CREATE TRIGGER axm_followup_touch_campaign AFTER UPDATE OF status, send_at ON campaigns
    FOR EACH ROW WHEN (OLD.send_at IS DISTINCT FROM NEW.send_at OR (OLD.status IS DISTINCT FROM NEW.status
        AND NOT (OLD.status = 'scheduled' AND NEW.status = 'running')))
    EXECUTE FUNCTION axm_followup_touch();
DROP TRIGGER IF EXISTS axm_followup_touch_lists ON campaign_lists;
CREATE TRIGGER axm_followup_touch_lists AFTER INSERT OR DELETE OR UPDATE OF list_id, campaign_id ON campaign_lists
    FOR EACH ROW EXECUTE FUNCTION axm_followup_touch();
-- Who the draft was made for: the people put on the follow-up list when it was made. They are
-- judged before sending whatever happens to their list row or to the original's send records.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_members (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- Opens and clicks of the original campaign since the follow-up was made, kept as they are
-- recorded: clearing the statistics or the send records' keeping time does not take them away.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_reactions (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    opened        BOOLEAN NOT NULL DEFAULT FALSE,
    clicked       BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (campaign_id, subscriber_id)
);
CREATE OR REPLACE FUNCTION axm_followup_reaction()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_campaign_followup_reactions (campaign_id, subscriber_id, opened, clicked)
    SELECT f.campaign_id, NEW.subscriber_id, TG_TABLE_NAME = 'campaign_views', TG_TABLE_NAME = 'link_clicks'
    FROM axm_campaign_followups f WHERE f.parent_campaign_id = NEW.campaign_id
    ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET
        opened = axm_campaign_followup_reactions.opened OR EXCLUDED.opened,
        clicked = axm_campaign_followup_reactions.clicked OR EXCLUDED.clicked;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_followup_reaction_view ON campaign_views;
CREATE TRIGGER axm_followup_reaction_view AFTER INSERT ON campaign_views
    FOR EACH ROW WHEN (NEW.subscriber_id IS NOT NULL) EXECUTE FUNCTION axm_followup_reaction();
DROP TRIGGER IF EXISTS axm_followup_reaction_click ON link_clicks;
CREATE TRIGGER axm_followup_reaction_click AFTER INSERT ON link_clicks
    FOR EACH ROW WHEN (NEW.subscriber_id IS NOT NULL) EXECUTE FUNCTION axm_followup_reaction();
-- End of AXM campaign follow-ups.`)
	return err
}
