package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_7 adds exclude lists to dynamic list rules (rule version 2) and the draw
// tables behind "draw N people from a list into a new list". It replaces one function
// and adds two tables (one with a sequence), five functions and three triggers. It never
// touches campaigns, axm_campaign_sends or the send engine's queries. A backup role with
// per-object grants needs SELECT on both tables and on the sequence axm_list_draws_id_seq
// after this runs (see FORK.md).
func V6_2_1_axm_7(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM list draws and exclude lists. Keep this block identical to V6_2_1_axm_7.
-- Dynamic list rules of version 2 may name exclude lists ("exclude": {"lists": [...]}):
-- a subscriber with a subscription row (any status) on one of them is left out. A version 2
-- rule may have no conditions. Version 1 rules give exactly the members they gave before.
CREATE OR REPLACE FUNCTION axm_filter_members(p_filter JSONB, p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE PARALLEL SAFE AS $$
    SELECT s.id FROM subscribers s
    WHERE s.status != 'blocklisted' AND s.id > p_after AND s.id <= p_upto
        AND CASE
            WHEN p_filter->>'version' IS DISTINCT FROM '2' THEN axm_filter_match(s.attribs, p_filter)
            WHEN p_filter->'conditions' = '[]'::JSONB THEN TRUE
            ELSE axm_filter_match(s.attribs, jsonb_set(p_filter, '{version}', '1'::JSONB))
        END
        AND EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL
                AND (p_filter->'base'->>'type' = 'all' OR
                    (p_filter->'base'->>'type' = 'lists' AND
                        COALESCE(p_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
                AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                    ELSE sl.status != 'unsubscribed' END
        )
        AND (p_filter->>'version' IS DISTINCT FROM '2' OR NOT EXISTS (
            SELECT 1 FROM subscriber_lists xl WHERE xl.subscriber_id = s.id
                AND COALESCE(p_filter->'exclude'->'lists', '[]'::JSONB) @> to_jsonb(xl.list_id)
        ))
        AND (p_list_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
                AND ex.list_id = p_list_id AND ex.status = 'unsubscribed'
        ))
    ORDER BY s.id LIMIT p_limit;
$$;

-- One row per list made by drawing from another list. The source and the people drawn
-- stay on record after either list is deleted (the list columns turn NULL).
CREATE TABLE IF NOT EXISTS axm_list_draws (
    id               SERIAL PRIMARY KEY,
    list_id          INTEGER NULL UNIQUE REFERENCES lists(id) ON DELETE SET NULL ON UPDATE CASCADE,
    source_list_id   INTEGER NULL REFERENCES lists(id) ON DELETE SET NULL ON UPDATE CASCADE,
    source_name      TEXT NOT NULL,
    list_name        TEXT NOT NULL,
    drawn_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    drawn_by         INTEGER NULL REFERENCES users(id) ON DELETE SET NULL ON UPDATE CASCADE,
    settings         JSONB NOT NULL DEFAULT '{}',
    result           JSONB NOT NULL DEFAULT '{}',
    deleted_at       TIMESTAMP WITH TIME ZONE NULL
);
CREATE INDEX IF NOT EXISTS idx_axm_draws_source ON axm_list_draws(source_list_id);

-- Campaigns that were sent to a drawn list that has since been deleted, kept per source so
-- the people who got them stay excluded from the same source's next draw.
CREATE TABLE IF NOT EXISTS axm_list_draw_campaigns (
    source_list_id   INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE ON UPDATE CASCADE DEFERRABLE INITIALLY DEFERRED,
    campaign_id      INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    PRIMARY KEY (source_list_id, campaign_id)
);

-- The people a list targets now. A regular list follows the rule campaigns use to read
-- it (not blocklisted, confirmed on a double opt-in list, not unsubscribed otherwise).
CREATE OR REPLACE FUNCTION axm_list_target(p_list_id INT)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT m.subscriber_id FROM axm_dynamic_list_members(p_list_id) m
    UNION ALL
    SELECT s.id FROM lists l
    JOIN subscriber_lists sl ON sl.list_id = l.id
    JOIN subscribers s ON s.id = sl.subscriber_id
    WHERE l.id = p_list_id AND l.axm_filter IS NULL AND s.status != 'blocklisted'
        AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
            ELSE sl.status != 'unsubscribed' END;
$$;

-- Everyone a draw from p_source looks at, with why they are left out: 0 = a candidate,
-- 1 = already drawn into a list from the same source, 2 = already received a campaign.
-- p_scope 'same_source' counts the campaigns sent to the source or to lists drawn from it
-- (also lists deleted since), 'all' counts every campaign. p_days > 0 limits the look back.
CREATE OR REPLACE FUNCTION axm_draw_candidates(p_source INT, p_scope TEXT, p_days INT)
RETURNS TABLE(subscriber_id INT, reason SMALLINT) LANGUAGE SQL STABLE AS $$
    WITH camps AS MATERIALIZED (
        SELECT cl.campaign_id FROM campaign_lists cl
        WHERE cl.list_id = p_source
            OR cl.list_id IN (SELECT d.list_id FROM axm_list_draws d WHERE d.source_list_id = p_source)
        UNION
        SELECT dc.campaign_id FROM axm_list_draw_campaigns dc WHERE dc.source_list_id = p_source
    )
    SELECT t.subscriber_id,
        (CASE
            WHEN EXISTS (
                SELECT 1 FROM axm_list_draws d JOIN subscriber_lists dl ON dl.list_id = d.list_id
                WHERE d.source_list_id = p_source AND dl.subscriber_id = t.subscriber_id
            ) THEN 1
            WHEN EXISTS (
                SELECT 1 FROM axm_campaign_sends cs WHERE cs.subscriber_id = t.subscriber_id
                    AND (COALESCE(p_days, 0) <= 0 OR cs.sent_at > NOW() - make_interval(days => p_days))
                    AND (p_scope = 'all' OR cs.campaign_id IN (SELECT campaign_id FROM camps))
            ) THEN 2
            ELSE 0
        END)::SMALLINT
    FROM axm_list_target(p_source) t;
$$;

-- A subscriber who unsubscribes from a source list also leaves the lists drawn from it, and
-- one who unsubscribes from a drawn list also leaves its source (a dynamic source gets the
-- same exclusion row and base list unsubscribes as an unsubscribe through its campaign).
-- Rows that are already unsubscribed are not touched again, so the two directions stop.
CREATE OR REPLACE FUNCTION axm_propagate_draw_unsub()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    d RECORD;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status = 'unsubscribed' THEN RETURN NULL; END IF;
    IF NOT EXISTS (SELECT 1 FROM axm_list_draws) THEN RETURN NULL; END IF;
    UPDATE subscriber_lists dl SET status = 'unsubscribed', updated_at = NOW()
    FROM axm_list_draws dr JOIN lists sl ON sl.id = dr.source_list_id
    WHERE dr.list_id = dl.list_id AND dl.subscriber_id = NEW.subscriber_id
        AND dl.status != 'unsubscribed' AND dr.list_id != NEW.list_id
        AND (
            (sl.axm_filter IS NULL AND sl.id = NEW.list_id)
            OR (sl.axm_filter IS NOT NULL AND NOT (
                EXISTS (SELECT 1 FROM subscribers s WHERE s.id = NEW.subscriber_id AND s.status != 'blocklisted')
                AND EXISTS (
                    SELECT 1 FROM subscriber_lists bl JOIN lists l ON l.id = bl.list_id
                    WHERE bl.subscriber_id = NEW.subscriber_id AND l.axm_filter IS NULL
                        AND NOT EXISTS (SELECT 1 FROM axm_list_draws xd WHERE xd.list_id = l.id)
                        AND (sl.axm_filter->'base'->>'type' = 'all' OR
                            (sl.axm_filter->'base'->>'type' = 'lists' AND
                                COALESCE(sl.axm_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
                        AND CASE WHEN l.optin = 'double' THEN bl.status = 'confirmed'
                            ELSE bl.status != 'unsubscribed' END
                )
                AND NOT EXISTS (
                    SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = NEW.subscriber_id
                        AND ex.list_id = sl.id AND ex.status = 'unsubscribed'
                )
            ))
        );
    FOR d IN SELECT dr.source_list_id, sl.axm_filter FROM axm_list_draws dr
        JOIN lists sl ON sl.id = dr.source_list_id WHERE dr.list_id = NEW.list_id
    LOOP
        IF d.axm_filter IS NULL THEN
            UPDATE subscriber_lists SET status = 'unsubscribed', updated_at = NOW()
            WHERE list_id = d.source_list_id AND subscriber_id = NEW.subscriber_id AND status != 'unsubscribed';
        ELSE
            INSERT INTO subscriber_lists (subscriber_id, list_id, status)
            VALUES (NEW.subscriber_id, d.source_list_id, 'unsubscribed')
            ON CONFLICT (subscriber_id, list_id) DO UPDATE SET status = 'unsubscribed', updated_at = NOW()
            WHERE subscriber_lists.status != 'unsubscribed';
            UPDATE subscriber_lists SET status = 'unsubscribed', updated_at = NOW()
            WHERE subscriber_id = NEW.subscriber_id AND status != 'unsubscribed' AND list_id IN (
                SELECT l.id FROM lists l WHERE l.axm_filter IS NULL
                    AND (d.axm_filter->'base'->>'type' = 'all' OR
                        (d.axm_filter->'base'->>'type' = 'lists' AND
                            COALESCE(d.axm_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
            );
        END IF;
    END LOOP;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_propagate_draw_unsub ON subscriber_lists;
CREATE TRIGGER axm_propagate_draw_unsub AFTER INSERT OR UPDATE OF status ON subscriber_lists
    FOR EACH ROW WHEN (NEW.status = 'unsubscribed') EXECUTE FUNCTION axm_propagate_draw_unsub();

-- Deleting a drawn list keeps the campaigns that were sent to it, per source, and marks the
-- draw as deleted. People who got those campaigns stay excluded from the source's draws.
CREATE OR REPLACE FUNCTION axm_keep_draw_campaigns()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_list_draw_campaigns (source_list_id, campaign_id)
    SELECT dr.source_list_id, cl.campaign_id FROM axm_list_draws dr
    JOIN campaign_lists cl ON cl.list_id = dr.list_id
    WHERE dr.list_id = OLD.id AND dr.source_list_id IS NOT NULL
    ON CONFLICT DO NOTHING;
    UPDATE axm_list_draws SET deleted_at = NOW(), result = result || jsonb_build_object(
        'campaigns', (SELECT COUNT(*) FROM campaign_lists cl WHERE cl.list_id = OLD.id),
        'campaigns_finished', (SELECT COUNT(*) FROM campaign_lists cl JOIN campaigns c ON c.id = cl.campaign_id
            WHERE cl.list_id = OLD.id AND c.status = 'finished'))
    WHERE list_id = OLD.id;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS axm_keep_draw_campaigns ON lists;
CREATE TRIGGER axm_keep_draw_campaigns BEFORE DELETE ON lists
    FOR EACH ROW EXECUTE FUNCTION axm_keep_draw_campaigns();

-- A list that a dynamic list names as an exclude list cannot be deleted: its people would
-- silently become members (and recipients) again. Remove it from the rule first.
CREATE OR REPLACE FUNCTION axm_guard_exclude_list_delete()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    dyn TEXT;
BEGIN
    SELECT l.name INTO dyn FROM lists l
    WHERE l.axm_filter IS NOT NULL AND l.id != OLD.id
        AND COALESCE(l.axm_filter->'exclude'->'lists', '[]'::JSONB) @> to_jsonb(OLD.id)
    LIMIT 1;
    IF dyn IS NOT NULL THEN
        RAISE EXCEPTION 'list % is an exclude list of the dynamic list "%"', OLD.id, dyn USING ERRCODE = 'AX001';
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS axm_guard_exclude_list_delete ON lists;
CREATE TRIGGER axm_guard_exclude_list_delete BEFORE DELETE ON lists
    FOR EACH ROW EXECUTE FUNCTION axm_guard_exclude_list_delete();
-- End of AXM list draws and exclude lists.`)
	return err
}
