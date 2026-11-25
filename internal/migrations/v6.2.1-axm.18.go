package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_18 makes the recent-send exclusion rule apply when a campaign is sent (LST-65): the
// people the sender left out of a campaign are kept in a table, and a function says who of a batch
// received a campaign of the rule's scope within the rule's days. It changes no existing object.
func V6_2_1_axm_18(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM recent-send exclusion at send time (LST-65). Keep this block identical to V6_2_1_axm_18.
-- Who a campaign with a recent-send rule has left out so far. The sender decides it when it reads a
-- batch and the person stays out of that campaign afterwards (a pause that outlasts the rule's days,
-- a restart, or the expiry of a send record does not let them in). The rows go with the campaign.
CREATE TABLE IF NOT EXISTS axm_campaign_recent_holds (
    campaign_id   INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    held_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (campaign_id, subscriber_id)
);
-- Whether the sender must apply the saved rule and preserve its holds. A rule whose chosen
-- campaigns were all deleted has no new matches, but any existing holds still apply.
CREATE OR REPLACE FUNCTION axm_recent_rule_on(p_campaign INT)
RETURNS BOOL LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM axm_campaign_recent_rules rule
        JOIN campaigns own ON own.id = rule.campaign_id AND own.type = 'regular'
        WHERE rule.campaign_id = p_campaign
            AND NOT EXISTS (SELECT 1 FROM axm_campaign_followups f WHERE f.campaign_id = p_campaign)
    );
$$;
-- Which of p_subs the recent-send rule of campaign p_campaign leaves out now: the people with a send
-- record of a campaign in the rule's scope (as axm_recent_exclusion counts it) in the last days of
-- the rule. A campaign without a rule, a follow-up campaign and a campaign other than a regular one
-- leave out nobody; a person this campaign already sent to is never left out later.
CREATE OR REPLACE FUNCTION axm_recent_received(p_campaign INT, p_subs INT[])
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    WITH r AS (
        SELECT rule.days, rule.scope, rule.campaign_ids
        FROM axm_campaign_recent_rules rule
        WHERE rule.campaign_id = p_campaign AND axm_recent_rule_on(p_campaign)
    ), team_tags AS (
        SELECT DISTINCT tag
        FROM r, campaign_lists cl
        JOIN lists l ON l.id = cl.list_id
        CROSS JOIN LATERAL unnest(l.tags) AS t(tag)
        WHERE r.scope = 'team' AND cl.campaign_id = p_campaign AND tag LIKE '팀:_%'
    ), camps AS MATERIALIZED (
        SELECT c.id
        FROM campaigns c, r
        WHERE c.type = 'regular' AND c.id <> p_campaign
            AND (r.scope = 'all'
                OR (r.scope = 'campaigns' AND c.id = ANY(r.campaign_ids))
                OR (r.scope = 'team' AND c.id IN (
                    SELECT cl.campaign_id
                    FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
                    WHERE l.tags && ARRAY(SELECT tag FROM team_tags)
                )))
    )
    SELECT s.id
    FROM unnest(p_subs) AS s(id), r
    CROSS JOIN LATERAL (
        SELECT 1 FROM axm_campaign_sends cs
        WHERE cs.subscriber_id = s.id
            AND cs.sent_at > NOW() - make_interval(days => r.days)
            AND cs.campaign_id = ANY(ARRAY(SELECT id FROM camps))
        LIMIT 1
    ) received
    WHERE EXISTS (SELECT 1 FROM camps)
        AND NOT EXISTS (
            SELECT 1 FROM axm_campaign_sends own
            WHERE own.campaign_id = p_campaign AND own.subscriber_id = s.id
        );
$$;
-- End of AXM recent-send exclusion at send time.`)
	return err
}
