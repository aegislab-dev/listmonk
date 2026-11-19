package migrations

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_13 adds recent-send preview functions and per-campaign rules.
func V6_2_1_axm_13(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM recent-send exclusion. Keep this block identical to V6_2_1_axm_13.
CREATE TABLE IF NOT EXISTS axm_campaign_recent_rules (
    campaign_id   INTEGER PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    days          SMALLINT NOT NULL CHECK (days IN (7, 14, 30)),
    scope         TEXT NOT NULL CHECK (scope IN ('all', 'team', 'campaigns')),
    campaign_ids  INTEGER[] NOT NULL DEFAULT '{}',
    updated_by    INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    updated_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- A subscriber can be eligible through more than one ordinary or dynamic list.
CREATE OR REPLACE FUNCTION axm_campaign_audience(p_lists INT[])
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT DISTINCT t.subscriber_id
    FROM unnest(p_lists) AS l(list_id)
    CROSS JOIN LATERAL axm_list_target(l.list_id) AS t;
$$;

CREATE OR REPLACE FUNCTION axm_recent_exclusion(
    p_lists INT[], p_days INT, p_scope TEXT, p_campaigns INT[], p_self INT
)
RETURNS TABLE(subscriber_id INT, excluded BOOL) LANGUAGE SQL STABLE AS $$
    WITH team_tags AS (
        SELECT DISTINCT tag
        FROM lists l CROSS JOIN LATERAL unnest(l.tags) AS t(tag)
        WHERE p_scope = 'team' AND l.id = ANY(p_lists) AND tag LIKE '팀:_%'
    ), camps AS MATERIALIZED (
        SELECT c.id
        FROM campaigns c
        WHERE c.type = 'regular' AND c.id <> COALESCE(p_self, 0)
            AND (p_scope = 'all'
                OR (p_scope = 'campaigns' AND c.id = ANY(p_campaigns))
                OR (p_scope = 'team' AND c.id IN (
                    SELECT cl.campaign_id
                    FROM campaign_lists cl JOIN lists l ON l.id = cl.list_id
                    WHERE l.tags && ARRAY(SELECT tag FROM team_tags)
                )))
    )
    SELECT t.subscriber_id, EXISTS (
        SELECT 1 FROM axm_campaign_sends cs
        WHERE cs.subscriber_id = t.subscriber_id
            AND cs.sent_at > NOW() - make_interval(days => p_days)
            AND cs.campaign_id <> COALESCE(p_self, 0)
            AND cs.campaign_id IN (SELECT id FROM camps)
    )
    FROM axm_campaign_audience(p_lists) t;
$$;
-- End of AXM recent-send exclusion.`)
	return err
}
