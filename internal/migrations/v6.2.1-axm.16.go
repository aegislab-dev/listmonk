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
-- Who got the original campaign, kept when the follow-up is made, and whether the draft was made
-- for them (made_for: put on the follow-up list then). They are judged before sending whatever
-- happens to their list row or to the original's send records, which expire.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_members (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    made_for      BOOLEAN NOT NULL,
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- The times opens and clicks were not recorded per subscriber (individual tracking off, or
-- tracking switched off). Settings take effect when the app starts, so a time begins at a start
-- with tracking off and ends at the next start with it on. A follow-up of a campaign that went
-- out before such a time ended cannot see its people's reactions of that time.
CREATE TABLE IF NOT EXISTS axm_tracking_off (
    started_at TIMESTAMP WITH TIME ZONE PRIMARY KEY,
    ended_at   TIMESTAMP WITH TIME ZONE NULL
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
-- Whether follow-up campaign p_campaign also reaches p_sub through a list other than its
-- follow-up list(s) p_lists (a list added to it by hand): such a person is not held back.
CREATE OR REPLACE FUNCTION axm_followup_reaches_otherwise(p_campaign INT, p_sub INT, p_lists INT[])
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM campaign_lists cl JOIN lists ol ON ol.id = cl.list_id
        WHERE cl.campaign_id = p_campaign AND NOT (cl.list_id = ANY(p_lists)) AND (
            (ol.axm_filter IS NULL AND EXISTS (
                SELECT 1 FROM subscriber_lists o WHERE o.list_id = ol.id AND o.subscriber_id = p_sub
                    AND CASE WHEN ol.optin = 'double' THEN o.status = 'confirmed' ELSE o.status != 'unsubscribed' END))
            OR (ol.axm_filter IS NOT NULL AND EXISTS (
                SELECT 1 FROM axm_dynamic_list_members(ol.id, p_sub - 1, p_sub, 1))))
    );
$$;
-- Keeps an open or click of an original campaign for its follow-up as it is recorded. On a "did
-- not" condition it also holds the person back at once (the sender skips held people on every
-- batch), so a reaction after the check before sending still keeps the mail from going out.
CREATE OR REPLACE FUNCTION axm_followup_reaction()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    f RECORD;
    lid INT;
BEGIN
    -- A follow-up being made of this campaign takes this lock exclusively just before it commits,
    -- so it waits for this reaction to be committed (and keeps it afterwards), and a reaction
    -- recorded meanwhile waits for it and then sees it below.
    PERFORM pg_advisory_xact_lock_shared(4747, NEW.campaign_id);
    FOR f IN SELECT fu.* FROM axm_campaign_followups fu JOIN campaigns c ON c.id = fu.campaign_id
        WHERE fu.parent_campaign_id = NEW.campaign_id AND c.status NOT IN ('finished', 'cancelled')
    LOOP
        INSERT INTO axm_campaign_followup_reactions (campaign_id, subscriber_id, opened, clicked)
        VALUES (f.campaign_id, NEW.subscriber_id, TG_TABLE_NAME = 'campaign_views', TG_TABLE_NAME = 'link_clicks')
        ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET
            opened = axm_campaign_followup_reactions.opened OR EXCLUDED.opened,
            clicked = axm_campaign_followup_reactions.clicked OR EXCLUDED.clicked;
        IF (TG_TABLE_NAME = 'campaign_views' AND f.condition->>'open' = 'no')
            OR (TG_TABLE_NAME = 'link_clicks' AND f.condition->>'click' = 'no') THEN
            SELECT l.id INTO lid FROM lists l JOIN campaign_lists cl ON cl.list_id = l.id AND cl.campaign_id = f.campaign_id
            WHERE l.uuid = f.list_uuid AND l.axm_filter IS NULL;
            IF lid IS NOT NULL THEN
                INSERT INTO axm_campaign_followup_holds (campaign_id, subscriber_id, reason)
                SELECT f.campaign_id, NEW.subscriber_id, -1
                WHERE EXISTS (SELECT 1 FROM subscriber_lists sl WHERE sl.list_id = lid
                        AND sl.subscriber_id = NEW.subscriber_id AND sl.status != 'unsubscribed')
                    AND NOT EXISTS (SELECT 1 FROM subscribers s WHERE s.id = NEW.subscriber_id AND s.status = 'blocklisted')
                    AND EXISTS (SELECT 1 FROM axm_campaign_followup_members m WHERE m.campaign_id = f.campaign_id AND m.subscriber_id = NEW.subscriber_id)
                    AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = f.campaign_id AND x.subscriber_id = NEW.subscriber_id)
                    AND NOT axm_followup_reaches_otherwise(f.campaign_id, NEW.subscriber_id, ARRAY[lid])
                ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;
            END IF;
        END IF;
    END LOOP;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_followup_reaction_view ON campaign_views;
CREATE TRIGGER axm_followup_reaction_view AFTER INSERT ON campaign_views
    FOR EACH ROW WHEN (NEW.subscriber_id IS NOT NULL) EXECUTE FUNCTION axm_followup_reaction();
DROP TRIGGER IF EXISTS axm_followup_reaction_click ON link_clicks;
CREATE TRIGGER axm_followup_reaction_click AFTER INSERT ON link_clicks
    FOR EACH ROW WHEN (NEW.subscriber_id IS NOT NULL) EXECUTE FUNCTION axm_followup_reaction();
-- Send records of an original campaign written after a follow-up of it was made (they are saved
-- shortly after the messages went out): the people are kept for the follow-up as well.
CREATE OR REPLACE FUNCTION axm_followup_recipients()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(4747, c.campaign_id) FROM (SELECT DISTINCT campaign_id FROM axm_new_sends) c;
    INSERT INTO axm_campaign_followup_members (campaign_id, subscriber_id, made_for)
    SELECT f.campaign_id, n.subscriber_id, FALSE
    FROM axm_new_sends n JOIN axm_campaign_followups f ON f.parent_campaign_id = n.campaign_id
    ON CONFLICT (campaign_id, subscriber_id) DO NOTHING;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_followup_recipients ON axm_campaign_sends;
CREATE TRIGGER axm_followup_recipients AFTER INSERT ON axm_campaign_sends
    REFERENCING NEW TABLE AS axm_new_sends FOR EACH STATEMENT EXECUTE FUNCTION axm_followup_recipients();
-- End of AXM campaign follow-ups.`)
	return err
}
