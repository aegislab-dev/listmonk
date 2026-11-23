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
    -- Who the sender left out so far, by reason, and how many batches it judged.
    recheck            JSONB NOT NULL DEFAULT '{}',
    rechecked_at       TIMESTAMP WITH TIME ZONE NULL,
    created_by         INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CHECK (campaign_id <> parent_campaign_id)
);
-- People the sender left out of a follow-up campaign when it judged their batch, with the reason
-- (as axm_followup_judge, and 5: no longer subscribed through the campaign's lists). Their
-- subscriptions are not changed. A later judgement of the same person replaces the row.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_holds (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    reason        SMALLINT NOT NULL,
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- Who got the original campaign, kept when the follow-up is made (and when a send record of the
-- original comes later), and whether the draft was made for them (made_for: put on the follow-up
-- list then). They are judged whatever happens to their list row or to the original's send
-- records, which expire.
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
-- Opens and clicks of the original campaign, kept for the follow-up as they are recorded (and
-- copied from the records when it is made): clearing the statistics or the send records' keeping
-- time does not take them away.
CREATE TABLE IF NOT EXISTS axm_campaign_followup_reactions (
    campaign_id   INTEGER NOT NULL REFERENCES axm_campaign_followups(campaign_id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    opened        BOOLEAN NOT NULL DEFAULT FALSE,
    clicked       BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- Whether follow-up campaign p_campaign also reaches p_sub through a list other than its
-- follow-up list(s) p_lists (a list added to it by hand): such a person is not left out.
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
-- The judgement of who a follow-up leaves out, in one place: the sender reads it for every batch
-- just before sending (axm-followup-claim), and the count of who would be left out now reads it. One row per person on the follow-up list (subscribed, not sent the follow-up yet, not
-- reached through another list of the campaign) who got the original, with the reason to hold
-- them back: 0 none, 1 blocked, 2 opted out of the original's lists, 4 reactions cannot be seen
-- (per-subscriber tracking off now, p_tracking, or for a time since the original went out), -1
-- the condition is broken. A reaction counts from the copy kept for the follow-up or any record
-- of it still there. A "did" condition held for the people the draft was made for. p_subs NULL
-- means everyone.
CREATE OR REPLACE FUNCTION axm_followup_judge(p_campaign INT, p_subs INT[], p_tracking BOOLEAN)
RETURNS TABLE (subscriber_id INT, sub_status TEXT, optin TEXT, reason INT)
LANGUAGE SQL STABLE AS $$
    WITH f AS (
        SELECT fu.parent_campaign_id AS parent, fu.condition->>'open' AS open, fu.condition->>'click' AS click,
            fu.list_uuid, COALESCE(pc.started_at, pc.created_at) AS parent_start
        FROM axm_campaign_followups fu JOIN campaigns pc ON pc.id = fu.parent_campaign_id
        WHERE fu.campaign_id = p_campaign
    ),
    lst AS (
        SELECT l.id, l.optin::TEXT AS optin FROM f
        JOIN lists l ON l.uuid = f.list_uuid AND l.axm_filter IS NULL
        JOIN campaign_lists cl ON cl.list_id = l.id AND cl.campaign_id = p_campaign
    ),
    known AS (
        SELECT (f.open <> 'no' AND f.click <> 'no') OR (p_tracking AND NOT EXISTS (
            SELECT 1 FROM axm_tracking_off o WHERE o.ended_at IS NULL OR o.ended_at > f.parent_start)) AS ok
        FROM f
    )
    SELECT sl.subscriber_id, sl.status::TEXT, lst.optin, CASE
        WHEN s.status = 'blocklisted' THEN 1
        WHEN EXISTS (
            SELECT 1 FROM campaign_lists pl JOIN subscriber_lists u ON u.list_id = pl.list_id
            WHERE pl.campaign_id = f.parent AND u.subscriber_id = sl.subscriber_id AND u.status = 'unsubscribed'
        ) THEN 2
        WHEN NOT known.ok THEN 4
        WHEN (f.open = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = p_campaign AND r.subscriber_id = sl.subscriber_id AND r.opened)
                OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = f.parent AND v.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = f.parent AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
            OR (f.click = 'no' AND (
                EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = p_campaign AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = f.parent AND k.subscriber_id = sl.subscriber_id)
                OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = f.parent AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))
            OR (NOT m.made_for AND (
                (f.open = 'yes' AND NOT (
                    EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = p_campaign AND r.subscriber_id = sl.subscriber_id AND r.opened)
                    OR EXISTS (SELECT 1 FROM campaign_views v WHERE v.campaign_id = f.parent AND v.subscriber_id = sl.subscriber_id)
                    OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = f.parent AND x.subscriber_id = sl.subscriber_id AND x.opened_at IS NOT NULL)))
                OR (f.click = 'yes' AND NOT (
                    EXISTS (SELECT 1 FROM axm_campaign_followup_reactions r WHERE r.campaign_id = p_campaign AND r.subscriber_id = sl.subscriber_id AND r.clicked)
                    OR EXISTS (SELECT 1 FROM link_clicks k WHERE k.campaign_id = f.parent AND k.subscriber_id = sl.subscriber_id)
                    OR EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = f.parent AND x.subscriber_id = sl.subscriber_id AND x.clicked_at IS NOT NULL)))))
            THEN -1
        ELSE 0
    END
    FROM f CROSS JOIN known CROSS JOIN lst
    JOIN subscriber_lists sl ON sl.list_id = lst.id
    JOIN subscribers s ON s.id = sl.subscriber_id
    JOIN axm_campaign_followup_members m ON m.campaign_id = p_campaign AND m.subscriber_id = sl.subscriber_id
    WHERE sl.status != 'unsubscribed' AND (p_subs IS NULL OR sl.subscriber_id = ANY(p_subs))
        AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends sent WHERE sent.campaign_id = p_campaign AND sent.subscriber_id = sl.subscriber_id)
        AND NOT axm_followup_reaches_otherwise(p_campaign, sl.subscriber_id, ARRAY[lst.id]);
$$;
-- Keeps an open or click of an original campaign for its follow-up as it is recorded: a view or
-- click row, or the open or click time written on the send record (that can come later than the
-- row, and the row can be cleared meanwhile).
CREATE OR REPLACE FUNCTION axm_followup_reaction()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    f RECORD;
    o BOOLEAN;
    k BOOLEAN;
BEGIN
    IF TG_TABLE_NAME = 'axm_campaign_sends' THEN
        o := NEW.opened_at IS NOT NULL;
        k := NEW.clicked_at IS NOT NULL;
    ELSE
        o := TG_TABLE_NAME = 'campaign_views';
        k := TG_TABLE_NAME = 'link_clicks';
    END IF;
    -- A follow-up being made of this campaign, and the sender judging a batch of one, take this
    -- lock exclusively: they wait for this reaction to be committed and then see it.
    PERFORM pg_advisory_xact_lock_shared(4747, NEW.campaign_id);
    FOR f IN SELECT fu.campaign_id FROM axm_campaign_followups fu JOIN campaigns c ON c.id = fu.campaign_id
        WHERE fu.parent_campaign_id = NEW.campaign_id AND c.status NOT IN ('finished', 'cancelled')
    LOOP
        INSERT INTO axm_campaign_followup_reactions (campaign_id, subscriber_id, opened, clicked)
        VALUES (f.campaign_id, NEW.subscriber_id, o, k)
        ON CONFLICT (campaign_id, subscriber_id) DO UPDATE SET
            opened = axm_campaign_followup_reactions.opened OR EXCLUDED.opened,
            clicked = axm_campaign_followup_reactions.clicked OR EXCLUDED.clicked;
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
DROP TRIGGER IF EXISTS axm_followup_reaction_send ON axm_campaign_sends;
CREATE TRIGGER axm_followup_reaction_send AFTER UPDATE OF opened_at, clicked_at ON axm_campaign_sends
    FOR EACH ROW WHEN ((OLD.opened_at IS NULL AND NEW.opened_at IS NOT NULL) OR (OLD.clicked_at IS NULL AND NEW.clicked_at IS NOT NULL))
    EXECUTE FUNCTION axm_followup_reaction();
-- The start of a follow-up's run (next-campaigns sets to_send and max_subscriber_id together, and
-- is the only statement that sets max_subscriber_id) counts everyone on its lists. The people the
-- sender has left out and who are still counted there come off, so to_send is what the follow-up
-- means to send. Ordinary campaigns are left as they are.
CREATE OR REPLACE FUNCTION axm_followup_recount()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM axm_campaign_followups WHERE campaign_id = NEW.id) THEN
        NEW.to_send := GREATEST(0, NEW.to_send - (
            SELECT COUNT(*) FROM axm_campaign_followup_holds h
            JOIN subscribers s ON s.id = h.subscriber_id AND s.status != 'blocklisted'
            WHERE h.campaign_id = NEW.id AND EXISTS (
                SELECT 1 FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
                WHERE cl.campaign_id = NEW.id AND (
                    (l.axm_filter IS NULL AND EXISTS (
                        SELECT 1 FROM subscriber_lists sl WHERE sl.list_id = l.id AND sl.subscriber_id = h.subscriber_id
                            AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END))
                    OR (l.axm_filter IS NOT NULL AND EXISTS (
                        SELECT 1 FROM axm_dynamic_list_members(l.id, h.subscriber_id - 1, h.subscriber_id, 1)))))));
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_followup_recount ON campaigns;
CREATE TRIGGER axm_followup_recount BEFORE UPDATE OF max_subscriber_id ON campaigns
    FOR EACH ROW EXECUTE FUNCTION axm_followup_recount();
-- End of AXM campaign follow-ups.`)
	return err
}
