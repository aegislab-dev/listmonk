DROP TYPE IF EXISTS list_type CASCADE; CREATE TYPE list_type AS ENUM ('public', 'private', 'temporary');
DROP TYPE IF EXISTS list_optin CASCADE; CREATE TYPE list_optin AS ENUM ('single', 'double');
DROP TYPE IF EXISTS list_status CASCADE; CREATE TYPE list_status AS ENUM ('active', 'archived');
DROP TYPE IF EXISTS subscriber_status CASCADE; CREATE TYPE subscriber_status AS ENUM ('enabled', 'disabled', 'blocklisted');
DROP TYPE IF EXISTS subscription_status CASCADE; CREATE TYPE subscription_status AS ENUM ('unconfirmed', 'confirmed', 'unsubscribed');
DROP TYPE IF EXISTS campaign_status CASCADE; CREATE TYPE campaign_status AS ENUM ('draft', 'running', 'scheduled', 'paused', 'cancelled', 'finished');
DROP TYPE IF EXISTS campaign_type CASCADE; CREATE TYPE campaign_type AS ENUM ('regular', 'optin');
DROP TYPE IF EXISTS content_type CASCADE; CREATE TYPE content_type AS ENUM ('richtext', 'html', 'plain', 'markdown', 'visual');
DROP TYPE IF EXISTS bounce_type CASCADE; CREATE TYPE bounce_type AS ENUM ('soft', 'hard', 'complaint');
DROP TYPE IF EXISTS template_type CASCADE; CREATE TYPE template_type AS ENUM ('campaign', 'campaign_visual', 'tx');
DROP TYPE IF EXISTS user_type CASCADE; CREATE TYPE user_type AS ENUM ('user', 'api');
DROP TYPE IF EXISTS user_status CASCADE; CREATE TYPE user_status AS ENUM ('enabled', 'disabled');
DROP TYPE IF EXISTS role_type CASCADE; CREATE TYPE role_type AS ENUM ('user', 'list');
DROP TYPE IF EXISTS twofa_type CASCADE; CREATE TYPE twofa_type AS ENUM ('none', 'totp');

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- subscribers
DROP TABLE IF EXISTS subscribers CASCADE;
CREATE TABLE subscribers (
    id              SERIAL PRIMARY KEY,
    uuid uuid       NOT NULL UNIQUE,
    email           TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    attribs         JSONB NOT NULL DEFAULT '{}',
    status          subscriber_status NOT NULL DEFAULT 'enabled',

    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_subs_email; CREATE UNIQUE INDEX idx_subs_email ON subscribers(LOWER(email));
DROP INDEX IF EXISTS idx_subs_status; CREATE INDEX idx_subs_status ON subscribers(status);
DROP INDEX IF EXISTS idx_subs_id_status; CREATE INDEX idx_subs_id_status ON subscribers(id, status);
DROP INDEX IF EXISTS idx_subs_created_at; CREATE INDEX idx_subs_created_at ON subscribers(created_at);
DROP INDEX IF EXISTS idx_subs_updated_at; CREATE INDEX idx_subs_updated_at ON subscribers(updated_at);

-- lists
DROP TABLE IF EXISTS lists CASCADE;
CREATE TABLE lists (
    axm_filter JSONB NULL,
    -- The column preset the subscribers table shows for this list (see core.ColumnPresets).
    axm_preset TEXT NOT NULL DEFAULT 'creator',
    id              SERIAL PRIMARY KEY,
    uuid            uuid NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    type            list_type NOT NULL,
    optin           list_optin NOT NULL DEFAULT 'single',
    status          list_status NOT NULL DEFAULT 'active',
    tags            VARCHAR(100)[],
    description     TEXT NOT NULL DEFAULT '',

    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_lists_type; CREATE INDEX idx_lists_type ON lists(type);
DROP INDEX IF EXISTS idx_lists_optin; CREATE INDEX idx_lists_optin ON lists(optin);
DROP INDEX IF EXISTS idx_lists_status; CREATE INDEX idx_lists_status ON lists(status);
DROP INDEX IF EXISTS idx_lists_name; CREATE INDEX idx_lists_name ON lists(name);
DROP INDEX IF EXISTS idx_lists_created_at; CREATE INDEX idx_lists_created_at ON lists(created_at);
DROP INDEX IF EXISTS idx_lists_updated_at; CREATE INDEX idx_lists_updated_at ON lists(updated_at);


DROP TABLE IF EXISTS subscriber_lists CASCADE;
CREATE TABLE subscriber_lists (
    subscriber_id      INTEGER REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    list_id            INTEGER NULL REFERENCES lists(id) ON DELETE CASCADE ON UPDATE CASCADE,
    meta               JSONB NOT NULL DEFAULT '{}',
    status             subscription_status NOT NULL DEFAULT 'unconfirmed',

    created_at         TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at         TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    PRIMARY KEY(subscriber_id, list_id)
);
DROP INDEX IF EXISTS idx_sub_lists_sub_id; CREATE INDEX idx_sub_lists_sub_id ON subscriber_lists(subscriber_id);
DROP INDEX IF EXISTS idx_sub_lists_list_id; CREATE INDEX idx_sub_lists_list_id ON subscriber_lists(list_id);
DROP INDEX IF EXISTS idx_sub_lists_status; CREATE INDEX idx_sub_lists_status ON subscriber_lists(status);

-- templates
DROP TABLE IF EXISTS templates CASCADE;
CREATE TABLE templates (
    id              SERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    type            template_type NOT NULL DEFAULT 'campaign',
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    body_source     TEXT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false,

    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE UNIQUE INDEX ON templates (is_default) WHERE is_default = true;


-- campaigns
DROP TABLE IF EXISTS campaigns CASCADE;
CREATE TABLE campaigns (
    id               SERIAL PRIMARY KEY,
    uuid uuid        NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    subject          TEXT NOT NULL,
    from_email       TEXT NOT NULL,
    reply_to         TEXT NOT NULL DEFAULT '',
    body             TEXT NOT NULL,
    body_source      TEXT NULL,
    altbody          TEXT NULL,
    content_type     content_type NOT NULL DEFAULT 'richtext',
    send_at          TIMESTAMP WITH TIME ZONE,
    headers          JSONB NOT NULL DEFAULT '[]',
    attribs          JSONB NOT NULL DEFAULT '{}',
    status           campaign_status NOT NULL DEFAULT 'draft',
    tags             VARCHAR(100)[],

    -- The subscription statuses of subscribers to which a campaign will be sent.
    -- For opt-in campaigns, this will be 'unsubscribed'.
    type campaign_type DEFAULT 'regular',

    -- The ID of the messenger backend used to send this campaign.
    messenger        TEXT NOT NULL,
    template_id      INTEGER REFERENCES templates(id) ON DELETE SET NULL,

    -- Progress and stats.
    to_send            INT NOT NULL DEFAULT 0,
    sent               INT NOT NULL DEFAULT 0,
    max_subscriber_id  INT NOT NULL DEFAULT 0,
    last_subscriber_id INT NOT NULL DEFAULT 0,

    -- Publishing.
    archive             BOOLEAN NOT NULL DEFAULT false,
    archive_slug        TEXT NULL UNIQUE,
    archive_template_id INTEGER REFERENCES templates(id) ON DELETE SET NULL,
    archive_meta        JSONB NOT NULL DEFAULT '{}',

    started_at       TIMESTAMP WITH TIME ZONE,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_camps_status; CREATE INDEX idx_camps_status ON campaigns(status);
DROP INDEX IF EXISTS idx_camps_name; CREATE INDEX idx_camps_name ON campaigns(name);
DROP INDEX IF EXISTS idx_camps_created_at; CREATE INDEX idx_camps_created_at ON campaigns(created_at);
DROP INDEX IF EXISTS idx_camps_updated_at; CREATE INDEX idx_camps_updated_at ON campaigns(updated_at);


DROP TABLE IF EXISTS campaign_lists CASCADE;
CREATE TABLE campaign_lists (
    id           BIGSERIAL PRIMARY KEY,
    campaign_id  INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,

    -- Lists may be deleted, so list_id is nullable
    -- and a copy of the original list name is maintained here.
    list_id      INTEGER NULL REFERENCES lists(id) ON DELETE SET NULL ON UPDATE CASCADE,
    list_name    TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ON campaign_lists (campaign_id, list_id);
DROP INDEX IF EXISTS idx_camp_lists_camp_id; CREATE INDEX idx_camp_lists_camp_id ON campaign_lists(campaign_id);
DROP INDEX IF EXISTS idx_camp_lists_list_id; CREATE INDEX idx_camp_lists_list_id ON campaign_lists(list_id);

DROP TABLE IF EXISTS campaign_views CASCADE;
CREATE TABLE campaign_views (
    id               BIGSERIAL PRIMARY KEY,
    campaign_id      INTEGER NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,

    -- Subscribers may be deleted, but the view counts should remain.
    subscriber_id    INTEGER NULL REFERENCES subscribers(id) ON DELETE SET NULL ON UPDATE CASCADE,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_views_camp_id; CREATE INDEX idx_views_camp_id ON campaign_views(campaign_id);
DROP INDEX IF EXISTS idx_views_subscriber_id; CREATE INDEX idx_views_subscriber_id ON campaign_views(subscriber_id);
DROP INDEX IF EXISTS idx_views_date; CREATE INDEX idx_views_date ON campaign_views(created_at);

-- media
DROP TABLE IF EXISTS media CASCADE;
CREATE TABLE media (
    id               SERIAL PRIMARY KEY,
    uuid uuid        NOT NULL UNIQUE,
    provider         TEXT NOT NULL DEFAULT '',
    filename         TEXT NOT NULL,
    content_type     TEXT NOT NULL DEFAULT 'application/octet-stream',
    thumb            TEXT NOT NULL,
    meta             JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_media_filename; CREATE INDEX idx_media_filename ON media(provider, filename);

-- campaign_media
DROP TABLE IF EXISTS campaign_media CASCADE;
CREATE TABLE campaign_media (
    campaign_id  INTEGER REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,

    -- Media items may be deleted, so media_id is nullable
    -- and a copy of the original name is maintained here.
    media_id     INTEGER NULL REFERENCES media(id) ON DELETE SET NULL ON UPDATE CASCADE,

    filename     TEXT NOT NULL DEFAULT ''
);
DROP INDEX IF EXISTS idx_camp_media_id; CREATE UNIQUE INDEX idx_camp_media_id ON campaign_media (campaign_id, media_id);
DROP INDEX IF EXISTS idx_camp_media_camp_id; CREATE INDEX idx_camp_media_camp_id ON campaign_media(campaign_id);


-- links
DROP TABLE IF EXISTS links CASCADE;
CREATE TABLE links (
    id               SERIAL PRIMARY KEY,
    uuid uuid        NOT NULL UNIQUE,
    url              TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

DROP TABLE IF EXISTS link_clicks CASCADE;
CREATE TABLE link_clicks (
    id               BIGSERIAL PRIMARY KEY,
    campaign_id      INTEGER NULL REFERENCES campaigns(id) ON DELETE CASCADE ON UPDATE CASCADE,
    link_id          INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE ON UPDATE CASCADE,

    -- Subscribers may be deleted, but the link counts should remain.
    subscriber_id    INTEGER NULL REFERENCES subscribers(id) ON DELETE SET NULL ON UPDATE CASCADE,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_clicks_camp_id; CREATE INDEX idx_clicks_camp_id ON link_clicks(campaign_id);
DROP INDEX IF EXISTS idx_clicks_link_id; CREATE INDEX idx_clicks_link_id ON link_clicks(link_id);
DROP INDEX IF EXISTS idx_clicks_sub_id; CREATE INDEX idx_clicks_sub_id ON link_clicks(subscriber_id);
DROP INDEX IF EXISTS idx_clicks_date; CREATE INDEX idx_clicks_date ON link_clicks(created_at);

-- settings
DROP TABLE IF EXISTS settings CASCADE;
CREATE TABLE settings (
    key             TEXT NOT NULL UNIQUE,
    value           JSONB NOT NULL DEFAULT '{}',
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_settings_key; CREATE INDEX idx_settings_key ON settings(key);
INSERT INTO settings (key, value) VALUES
    ('axm.sender_domains', '[]'),
    ('axm.test_domains', '[]'),
    ('axm.default_from', '""'),
    ('axm.default_reply_to', '""'),
    ('axm.from_addresses', '[]'),
    ('axm.business_footer', '{"enabled": false, "company_name": "", "representative": "", "registration_no": "", "address": "", "phone": "", "email": "", "extra": ""}'),
    ('app.site_name', '"Mailing list"'),
    ('app.root_url', '"http://localhost:9000"'),
    ('app.favicon_url', '""'),
    ('app.from_email', '"listmonk <noreply@listmonk.yoursite.com>"'),
    ('app.logo_url', '""'),
    ('app.concurrency', '10'),
    ('app.message_rate', '10'),
    ('app.batch_size', '1000'),
    ('app.max_send_errors', '1000'),
    ('app.message_sliding_window', 'false'),
    ('app.message_sliding_window_duration', '"1h"'),
    ('app.message_sliding_window_rate', '10000'),
    ('app.cache_slow_queries', 'false'),
    ('app.cache_slow_queries_interval', '"0 3 * * *"'),
    ('app.enable_public_archive', 'true'),
    ('app.enable_public_subscription_page', 'true'),
    ('app.show_optin_page', 'true'),
    ('app.enable_public_archive_rss_content', 'true'),
    ('app.send_optin_confirmation', 'true'),
    ('app.check_updates', 'true'),
    ('app.notify_emails', '[]'),
    ('app.lang', '"en"'),
    ('privacy.individual_tracking', 'false'),
    ('privacy.disable_tracking', 'false'),
    ('privacy.unsubscribe_header', 'true'),
    ('privacy.allow_blocklist', 'true'),
    ('privacy.allow_export', 'true'),
    ('privacy.allow_wipe', 'true'),
    ('privacy.allow_preferences', 'true'),
    ('privacy.exportable', '["profile", "subscriptions", "campaign_views", "link_clicks"]'),
    ('privacy.domain_blocklist', '[]'),
    ('privacy.domain_allowlist', '[]'),
    ('privacy.record_optin_ip', 'false'),
    ('security.captcha', '{"altcha": {"enabled": false, "complexity": 300000}, "hcaptcha": {"enabled": false, "key": "", "secret": ""}}'),
    ('security.oidc', '{"enabled": false, "provider_url": "", "provider_name": "", "client_id": "", "client_secret": "", "auto_create_users": false, "default_user_role_id": null, "default_list_role_id": null}'),
    ('security.trusted_urls', '[]'),
    ('upload.provider', '"filesystem"'),
    ('upload.max_file_size', '5000'),
    ('upload.extensions', '["jpg","jpeg","png","gif","svg","*"]'),
    ('upload.filesystem.upload_path', '"uploads"'),
    ('upload.filesystem.upload_uri', '"/uploads"'),
    ('upload.s3.url', '"https://ap-south-1.s3.amazonaws.com"'),
    ('upload.s3.public_url', '""'),
    ('upload.s3.aws_access_key_id', '""'),
    ('upload.s3.aws_secret_access_key', '""'),
    ('upload.s3.aws_default_region', '"ap-south-1"'),
    ('upload.s3.bucket', '""'),
    ('upload.s3.bucket_domain', '""'),
    ('upload.s3.bucket_path', '"/"'),
    ('upload.s3.bucket_type', '"public"'),
    ('upload.s3.expiry', '"167h"'),
    ('smtp',
        '[{"enabled":true, "host":"smtp.yoursite.com","port":25,"auth_protocol":"cram","username":"username","password":"password","hello_hostname":"","max_conns":10,"idle_timeout":"15s","wait_timeout":"5s","max_msg_retries":2,"msg_retry_delay":"10ms","tls_type":"STARTTLS","tls_skip_verify":false,"email_headers":[], "from_addresses":[]},
          {"enabled":false, "host":"smtp.gmail.com","port":465,"auth_protocol":"login","username":"username@gmail.com","password":"password","hello_hostname":"","max_conns":10,"idle_timeout":"15s","wait_timeout":"5s","max_msg_retries":2,"msg_retry_delay":"10ms","tls_type":"TLS","tls_skip_verify":false,"email_headers":[], "from_addresses":[]}]'),
    ('messengers', '[]'),
    ('bounce.enabled', 'false'),
    ('bounce.webhooks_enabled', 'false'),
    ('bounce.actions', '{"soft": {"count": 2, "action": "none"}, "hard": {"count": 1, "action": "blocklist"}, "complaint" : {"count": 1, "action": "blocklist"}}'),
    ('bounce.ses_enabled', 'false'),
    ('bounce.azure', '{"enabled": false, "shared_secret": "", "shared_secret_header": ""}'),
    ('bounce.sendgrid_enabled', 'false'),
    ('bounce.sendgrid_key', '""'),
    ('bounce.postmark', '{"enabled": false, "username": "", "password": ""}'),
    ('bounce.forwardemail', '{"enabled": false, "key": ""}'),
    ('bounce.lettermint', '{"enabled": false, "key": ""}'),
    ('bounce.mailboxes',
        '[{"enabled":false, "type": "pop", "host":"pop.yoursite.com","port":995,"auth_protocol":"userpass","username":"username","password":"password","return_path": "bounce@listmonk.yoursite.com","scan_interval":"15m","tls_enabled":true,"tls_skip_verify":false}]'),
    ('appearance.admin.custom_css', '""'),
    ('appearance.admin.custom_js', '""'),
    ('appearance.public.custom_css', '""'),
    ('appearance.public.custom_js', '""'),
    ('maintenance.db', '{"vacuum": false, "vacuum_cron_interval": "0 2 * * *"}');

-- bounces
DROP TABLE IF EXISTS bounces CASCADE;
CREATE TABLE bounces (
    id               SERIAL PRIMARY KEY,
    subscriber_id    INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    campaign_id      INTEGER NULL REFERENCES campaigns(id) ON DELETE SET NULL ON UPDATE CASCADE,
    type             bounce_type NOT NULL DEFAULT 'hard',
    source           TEXT NOT NULL DEFAULT '',
    meta             JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_bounces_sub_id; CREATE INDEX idx_bounces_sub_id ON bounces(subscriber_id);
DROP INDEX IF EXISTS idx_bounces_camp_id; CREATE INDEX idx_bounces_camp_id ON bounces(campaign_id);
DROP INDEX IF EXISTS idx_bounces_source; CREATE INDEX idx_bounces_source ON bounces(source);
DROP INDEX IF EXISTS idx_bounces_date; CREATE INDEX idx_bounces_date ON bounces(created_at);

-- roles
DROP TABLE IF EXISTS roles CASCADE;
CREATE TABLE roles (
    id               SERIAL PRIMARY KEY,
    type             role_type NOT NULL DEFAULT 'user',
    parent_id        INTEGER NULL REFERENCES roles(id) ON DELETE CASCADE ON UPDATE CASCADE,
    list_id          INTEGER NULL REFERENCES lists(id) ON DELETE CASCADE ON UPDATE CASCADE,
    permissions      TEXT[] NOT NULL DEFAULT '{}',
    name             TEXT NULL,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_roles ON roles (parent_id, list_id);
CREATE UNIQUE INDEX idx_roles_name ON roles (type, name) WHERE name IS NOT NULL;

-- users
DROP TABLE IF EXISTS users CASCADE;
CREATE TABLE users (
    id               SERIAL PRIMARY KEY,
    username         TEXT NOT NULL UNIQUE,
    password_login   BOOLEAN NOT NULL DEFAULT false,
    password         TEXT NULL,
    email            TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    avatar           TEXT NULL,
    type             user_type NOT NULL DEFAULT 'user',
    user_role_id     INTEGER NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    list_role_id     INTEGER NULL REFERENCES roles(id) ON DELETE CASCADE,
    status           user_status NOT NULL DEFAULT 'disabled',
    twofa_type       twofa_type NOT NULL DEFAULT 'none',
    twofa_key        TEXT NULL,
    loggedin_at      TIMESTAMP WITH TIME ZONE NULL,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- user sessions
DROP TABLE IF EXISTS sessions CASCADE;
CREATE TABLE sessions (
    id TEXT NOT NULL PRIMARY KEY,
    data JSONB DEFAULT '{}'::jsonb NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL
);
DROP INDEX IF EXISTS idx_sessions; CREATE INDEX idx_sessions ON sessions (id, created_at);

-- materialized views

-- dashboard stats
DROP MATERIALIZED VIEW IF EXISTS mat_dashboard_counts;
CREATE MATERIALIZED VIEW mat_dashboard_counts AS
    WITH subs AS (
        SELECT COUNT(*) AS num, status FROM subscribers GROUP BY status
    )
    SELECT NOW() AS updated_at,
        JSON_BUILD_OBJECT(
            'subscribers', JSON_BUILD_OBJECT(
                'total', (SELECT SUM(num) FROM subs),
                'blocklisted', (SELECT num FROM subs WHERE status='blocklisted'),
                'orphans', (
                    SELECT COUNT(id) FROM subscribers
                    LEFT JOIN subscriber_lists ON (subscribers.id = subscriber_lists.subscriber_id)
                    WHERE subscriber_lists.subscriber_id IS NULL
                )
            ),
            'lists', JSON_BUILD_OBJECT(
                'total', (SELECT COUNT(*) FROM lists),
                'private', (SELECT COUNT(*) FROM lists WHERE type='private'),
                'public', (SELECT COUNT(*) FROM lists WHERE type='public'),
                'optin_single', (SELECT COUNT(*) FROM lists WHERE optin='single'),
                'optin_double', (SELECT COUNT(*) FROM lists WHERE optin='double')
            ),
            'campaigns', JSON_BUILD_OBJECT(
                'total', (SELECT COUNT(*) FROM campaigns),
                'by_status', (
                    SELECT JSON_OBJECT_AGG (status, num) FROM
                    (SELECT status, COUNT(*) AS num FROM campaigns GROUP BY status) r
                )
            ),
            'messages', (SELECT SUM(sent) AS messages FROM campaigns)
        ) AS data;
DROP INDEX IF EXISTS mat_dashboard_stats_idx; CREATE UNIQUE INDEX mat_dashboard_stats_idx ON mat_dashboard_counts (updated_at);


DROP MATERIALIZED VIEW IF EXISTS mat_dashboard_charts;
CREATE MATERIALIZED VIEW mat_dashboard_charts AS
    WITH clicks AS (
        SELECT JSON_AGG(ROW_TO_JSON(row))
        FROM (
            WITH viewDates AS (
              SELECT created_at::DATE AS to_date,
                     created_at::DATE - INTERVAL '30 DAY' AS from_date
                     FROM link_clicks ORDER BY id DESC LIMIT 1
            )
            SELECT COUNT(*) AS count, created_at::DATE as date FROM link_clicks
              WHERE created_at >= (SELECT from_date FROM viewDates)
                AND created_at < (SELECT to_date FROM viewDates) + INTERVAL '1 day'
              GROUP by date ORDER BY date
        ) row
    ),
    views AS (
        SELECT JSON_AGG(ROW_TO_JSON(row))
        FROM (
            WITH viewDates AS (
              SELECT created_at::DATE AS to_date,
                     created_at::DATE - INTERVAL '30 DAY' AS from_date
                     FROM campaign_views ORDER BY id DESC LIMIT 1
            )
            SELECT COUNT(*) AS count, created_at::DATE as date FROM campaign_views
              WHERE created_at >= (SELECT from_date FROM viewDates)
                AND created_at < (SELECT to_date FROM viewDates) + INTERVAL '1 day'
              GROUP by date ORDER BY date
        ) row
    )
    SELECT NOW() AS updated_at, JSON_BUILD_OBJECT('link_clicks', COALESCE((SELECT * FROM clicks), '[]'),
                                  'campaign_views', COALESCE((SELECT * FROM views), '[]')
                                ) AS data;
DROP INDEX IF EXISTS mat_dashboard_charts_idx; CREATE UNIQUE INDEX mat_dashboard_charts_idx ON mat_dashboard_charts (updated_at);

-- subscriber counts stats for lists
DROP MATERIALIZED VIEW IF EXISTS mat_list_subscriber_stats;
CREATE MATERIALIZED VIEW mat_list_subscriber_stats AS
    SELECT NOW() AS updated_at, lists.id AS list_id, subscriber_lists.status, COUNT(subscriber_lists.status) AS subscriber_count FROM lists
    LEFT JOIN subscriber_lists ON (subscriber_lists.list_id = lists.id)
    GROUP BY lists.id, subscriber_lists.status
    UNION ALL
    SELECT NOW() AS updated_at, 0 AS list_id, NULL AS status, COUNT(id) AS subscriber_count FROM subscribers;
DROP INDEX IF EXISTS mat_list_subscriber_stats_idx; CREATE UNIQUE INDEX mat_list_subscriber_stats_idx ON mat_list_subscriber_stats (list_id, status);

-- AXM dynamic list conditions. Keep this block identical to V6_2_1_axm_4.
-- Reads a JSON number, or a JSON string that holds one, as NUMERIC. A string of up to 64
-- characters may have blanks around it, a sign, a fraction, and either a two-digit exponent
-- or thousands commas in groups of three. Any other value is NULL, never an error: each cast
-- runs only after its pattern matched, and the patterns admit nothing NUMERIC rejects.
CREATE OR REPLACE FUNCTION axm_to_number(p JSONB)
RETURNS NUMERIC LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE jsonb_typeof(p)
        WHEN 'number' THEN p::NUMERIC
        WHEN 'string' THEN CASE
            WHEN length(p #>> '{}') > 64 THEN NULL
            WHEN btrim(p #>> '{}', E' \t\n\r') ~ '^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]{1,2})?$'
                THEN btrim(p #>> '{}', E' \t\n\r')::NUMERIC
            WHEN btrim(p #>> '{}', E' \t\n\r') ~ '^[+-]?[0-9]{1,3}(,[0-9]{3})+(\.[0-9]*)?$'
                THEN replace(btrim(p #>> '{}', E' \t\n\r'), ',', '')::NUMERIC
        END
    END;
$$;

CREATE OR REPLACE FUNCTION axm_filter_match(p_attribs JSONB, p_filter JSONB)
RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    c JSONB;
    v JSONB;
    w JSONB;
    n NUMERIC;
    a NUMERIC;
    op TEXT;
    hit BOOLEAN;
    result BOOLEAN;
BEGIN
    IF p_filter->>'version' IS DISTINCT FROM '1'
       OR p_filter->>'match' IS NULL OR p_filter->>'match' NOT IN ('all', 'any')
       OR jsonb_typeof(p_filter->'conditions') IS DISTINCT FROM 'array' THEN
        RETURN FALSE;
    END IF;
    IF jsonb_array_length(p_filter->'conditions') NOT BETWEEN 1 AND 20 THEN RETURN FALSE; END IF;
    result := p_filter->>'match' = 'all';
    FOR i IN 0 .. jsonb_array_length(p_filter->'conditions') - 1 LOOP
        c := p_filter->'conditions'->i;
        IF jsonb_typeof(c) IS DISTINCT FROM 'object'
            OR jsonb_typeof(c->'key') IS DISTINCT FROM 'string'
            OR length(c->>'key') NOT BETWEEN 1 AND 64 THEN
            RETURN FALSE;
        END IF;
        v := p_attribs->(c->>'key');
        op := c->>'op';
        IF op IN ('gte', 'gt', 'lte', 'lt', 'between', 'not_number') THEN
            -- Plain variables keep the inlined axm_to_number() from repeating JSON lookups.
            w := c->'value';
            n := axm_to_number(v);
            a := axm_to_number(w);
            hit := CASE op
                WHEN 'gte' THEN n >= a
                WHEN 'gt' THEN n > a
                WHEN 'lte' THEN n <= a
                WHEN 'lt' THEN n < a
                WHEN 'between' THEN n >= a AND n < axm_to_number(c->'value2')
                -- Preview diagnostic, not accepted in saved filters: a value that is not a number.
                ELSE n IS NULL AND NOT (v IS NULL OR v = 'null'::JSONB OR v = '[]'::JSONB
                    OR (jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\n\r') = ''))
            END;
        ELSE
            hit := CASE op
                WHEN 'eq' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean') AND v #>> '{}' = c->>'value'
                WHEN 'contains' THEN jsonb_typeof(v) IN ('string', 'number', 'boolean')
                    AND strpos(lower(v #>> '{}'), lower(c->>'value')) > 0
                WHEN 'empty' THEN v IS NULL OR v = 'null'::JSONB OR v = '[]'::JSONB
                    OR (jsonb_typeof(v) = 'string' AND btrim(v #>> '{}', E' \t\n\r') = '')
                WHEN 'has' THEN jsonb_typeof(v) = 'array' AND v @> jsonb_build_array(c->>'value')
                ELSE FALSE
            END;
        END IF;
        IF p_filter->>'match' = 'all' THEN result := result AND COALESCE(hit, FALSE);
        ELSE result := result OR COALESCE(hit, FALSE); END IF;
    END LOOP;
    RETURN result;
END;
$$;

-- AXM dynamic list members and guard. Keep this block identical to V6_2_1_axm_3.
CREATE OR REPLACE FUNCTION axm_filter_members(p_filter JSONB, p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT s.id FROM subscribers s
    WHERE s.status != 'blocklisted' AND s.id > p_after AND s.id <= p_upto
        AND axm_filter_match(s.attribs, p_filter)
        AND EXISTS (
            SELECT 1 FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id
            WHERE sl.subscriber_id = s.id AND l.axm_filter IS NULL
                AND (p_filter->'base'->>'type' = 'all' OR
                    (p_filter->'base'->>'type' = 'lists' AND
                        COALESCE(p_filter->'base'->'lists', '[]'::JSONB) @> to_jsonb(l.id)))
                AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed'
                    ELSE sl.status != 'unsubscribed' END
        )
        AND (p_list_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM subscriber_lists ex WHERE ex.subscriber_id = s.id
                AND ex.list_id = p_list_id AND ex.status = 'unsubscribed'
        ))
    ORDER BY s.id LIMIT p_limit;
$$;

CREATE OR REPLACE FUNCTION axm_dynamic_list_members(p_list_id INT,
    p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
RETURNS TABLE(subscriber_id INT) LANGUAGE SQL STABLE AS $$
    SELECT m.subscriber_id FROM lists l
    CROSS JOIN LATERAL axm_filter_members(l.axm_filter, l.id, p_after, p_upto, p_limit) m
    WHERE l.id = p_list_id AND l.axm_filter IS NOT NULL;
$$;

CREATE OR REPLACE FUNCTION axm_guard_dynamic_subscription()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status != 'unsubscribed' AND EXISTS (
        SELECT 1 FROM lists WHERE id = NEW.list_id AND axm_filter IS NOT NULL
    ) THEN RETURN NULL; END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_guard_dynamic_subscription ON subscriber_lists;
CREATE TRIGGER axm_guard_dynamic_subscription BEFORE INSERT OR UPDATE ON subscriber_lists
    FOR EACH ROW EXECUTE FUNCTION axm_guard_dynamic_subscription();

-- AXM dynamic list planning hints. Keep this block identical to V6_2_1_axm_4.
ALTER FUNCTION axm_filter_members(JSONB, INT, INT, INT, INT) PARALLEL SAFE;
ALTER FUNCTION axm_dynamic_list_members(INT, INT, INT, INT) PARALLEL SAFE;


-- Verified external reply addresses (AXM).

CREATE TABLE IF NOT EXISTS sender_addresses (
    id SERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE CHECK (email = LOWER(email)),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified')),
    token_hash TEXT NULL,
    token_expires_at TIMESTAMPTZ NULL,
    requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    verified_at TIMESTAMPTZ NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sender_token ON sender_addresses(token_hash) WHERE token_hash IS NOT NULL;

-- A fresh install starts without send records. Dropping subscribers and campaigns above
-- would otherwise leave this table behind with its rows and without its foreign keys.
DROP TABLE IF EXISTS axm_campaign_sends CASCADE;
-- The same for the list draw tables: they reference lists, users and campaigns.
DROP TABLE IF EXISTS axm_list_draw_campaigns CASCADE;
DROP TABLE IF EXISTS axm_list_draws CASCADE;
DROP TABLE IF EXISTS axm_suppressed_emails CASCADE;
DROP TABLE IF EXISTS axm_audit_log CASCADE;
DROP TABLE IF EXISTS axm_deleted_list_subs CASCADE;
DROP TABLE IF EXISTS axm_deleted_lists CASCADE;
DROP TABLE IF EXISTS axm_column_presets CASCADE;
DROP TABLE IF EXISTS axm_campaign_recent_rules CASCADE;
DROP TABLE IF EXISTS axm_test_addresses CASCADE;
DROP TABLE IF EXISTS axm_campaign_followup_holds CASCADE;
DROP TABLE IF EXISTS axm_campaign_followup_members CASCADE;
DROP TABLE IF EXISTS axm_tracking_off CASCADE;
DROP TABLE IF EXISTS axm_campaign_followup_reactions CASCADE;
DROP TABLE IF EXISTS axm_campaign_followups CASCADE;

-- AXM campaign send records. Keep this block identical to V6_2_1_axm_6.
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
-- Time ranges (recent sends, retention). Rows are inserted in sent_at order, so a BRIN
-- index is a few pages. It stays selective for recent ranges. For old ranges it loses
-- selectivity as pages are reused, at worst down to a full scan for the daily retention job.
CREATE INDEX IF NOT EXISTS idx_axm_sends_sent_at ON axm_campaign_sends USING BRIN (sent_at);
-- End of AXM campaign send records.

-- AXM list draws and exclude lists. Keep this block identical to V6_2_1_axm_7.
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
-- End of AXM list draws and exclude lists.

-- AXM team roles. Keep this block identical to V6_2_1_axm_8.
-- Team roles: a list role whose axm_team_tag is set holds exactly the lists that carry
-- that tag, as ordinary child rows (parent_id, list_id) with list:get and list:manage.
-- Nothing else about roles changes, so the permission checks read them as before.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS axm_team_tag VARCHAR(100) NULL;
-- The user role that members of the team get when an admin adds them (a convenience default).
ALTER TABLE roles ADD COLUMN IF NOT EXISTS axm_team_user_role_id INTEGER NULL REFERENCES roles(id) ON DELETE SET NULL ON UPDATE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_axm_team_tag ON roles(axm_team_tag) WHERE axm_team_tag IS NOT NULL;

-- Makes the child rows of the team roles match the lists' tags. p_role_id / p_list_id
-- limit the work to one role or one list (NULL = all). Safe to run again.
CREATE OR REPLACE FUNCTION axm_sync_team_lists(p_role_id INT, p_list_id INT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM roles c USING roles r
    WHERE c.parent_id = r.id AND r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
        AND (p_role_id IS NULL OR r.id = p_role_id)
        AND (p_list_id IS NULL OR c.list_id = p_list_id)
        AND NOT EXISTS (SELECT 1 FROM lists l WHERE l.id = c.list_id AND r.axm_team_tag = ANY(l.tags));

    INSERT INTO roles (parent_id, list_id, type, permissions)
    SELECT r.id, l.id, 'list', ARRAY['list:get', 'list:manage']
    FROM roles r JOIN lists l ON r.axm_team_tag = ANY(l.tags)
    WHERE r.parent_id IS NULL AND r.type = 'list' AND r.axm_team_tag IS NOT NULL
        AND (p_role_id IS NULL OR r.id = p_role_id)
        AND (p_list_id IS NULL OR l.id = p_list_id)
    ON CONFLICT (parent_id, list_id) DO UPDATE SET permissions = EXCLUDED.permissions;
END;
$$;

CREATE OR REPLACE FUNCTION axm_team_lists_trigger()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM roles WHERE axm_team_tag IS NOT NULL) THEN
        PERFORM axm_sync_team_lists(NULL, NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_team_lists_sync ON lists;
CREATE TRIGGER axm_team_lists_sync AFTER INSERT OR UPDATE OF tags ON lists
    FOR EACH ROW EXECUTE FUNCTION axm_team_lists_trigger();

CREATE OR REPLACE FUNCTION axm_team_role_trigger()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM axm_sync_team_lists(NEW.id, NULL);
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_team_role_sync ON roles;
CREATE TRIGGER axm_team_role_sync AFTER INSERT OR UPDATE OF axm_team_tag ON roles
    FOR EACH ROW WHEN (NEW.parent_id IS NULL AND NEW.type = 'list' AND NEW.axm_team_tag IS NOT NULL)
    EXECUTE FUNCTION axm_team_role_trigger();
-- End of AXM team roles.

-- AXM suppressed addresses. Keep this block identical to V6_2_1_axm_9.
-- Addresses that were blocklisted stay blocklisted when the subscriber row is deleted and the
-- address is added or imported again. Only a hash of the lowercased address is kept. The
-- table follows subscribers.status: it gets a row when a subscriber is blocklisted and loses
-- it when the subscriber is unblocked. Deleting a blocklisted subscriber leaves the row.
CREATE TABLE IF NOT EXISTS axm_suppressed_emails (
    email_hash       BYTEA PRIMARY KEY,
    reason           TEXT NOT NULL DEFAULT 'blocklist',
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION axm_email_hash(p_email TEXT)
RETURNS BYTEA LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT sha256(convert_to(lower(btrim(p_email)), 'UTF8'));
$$;

INSERT INTO axm_suppressed_emails (email_hash, reason)
    SELECT axm_email_hash(email), 'blocklist' FROM subscribers WHERE status = 'blocklisted'
    ON CONFLICT (email_hash) DO NOTHING;

-- A new subscriber with a suppressed address starts blocklisted.
CREATE OR REPLACE FUNCTION axm_suppress_new_subscriber()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(NEW.email)) THEN
        NEW.status = 'blocklisted';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_suppress_new_subscriber ON subscribers;
CREATE TRIGGER axm_suppress_new_subscriber BEFORE INSERT ON subscribers
    FOR EACH ROW EXECUTE FUNCTION axm_suppress_new_subscriber();

-- A subscriber whose e-mail is changed to a suppressed address becomes blocklisted, as a new
-- subscriber with that address would. Its subscriptions are unsubscribed when the statement is
-- done (a deferred trigger): the statements that edit a subscriber also write its
-- subscriptions, and a trigger must not change those rows in the middle of them.
CREATE OR REPLACE FUNCTION axm_suppress_changed_email()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status != 'blocklisted'
        AND EXISTS (SELECT 1 FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(NEW.email)) THEN
        NEW.status = 'blocklisted';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_suppress_changed_email ON subscribers;
CREATE TRIGGER axm_suppress_changed_email BEFORE UPDATE OF email ON subscribers
    FOR EACH ROW WHEN (lower(btrim(NEW.email)) != lower(btrim(OLD.email))) EXECUTE FUNCTION axm_suppress_changed_email();

CREATE OR REPLACE FUNCTION axm_unsubscribe_blocked_email()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE subscriber_lists SET status = 'unsubscribed', updated_at = NOW()
        WHERE subscriber_id = NEW.id AND status != 'unsubscribed';
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_unsubscribe_blocked_email ON subscribers;
CREATE CONSTRAINT TRIGGER axm_unsubscribe_blocked_email AFTER UPDATE OF email ON subscribers
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.status = 'blocklisted' AND OLD.status != 'blocklisted')
    EXECUTE FUNCTION axm_unsubscribe_blocked_email();

-- Keeps the table equal to the blocklisted addresses. An address is released only when its
-- subscriber is taken off the blocklist; editing the e-mail of a blocked subscriber keeps the
-- old address blocked and blocks the new one too.
CREATE OR REPLACE FUNCTION axm_track_blocklist()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status = 'blocklisted' AND NEW.status != 'blocklisted' THEN
        DELETE FROM axm_suppressed_emails WHERE email_hash = axm_email_hash(OLD.email);
    END IF;
    IF NEW.status = 'blocklisted' THEN
        INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash(NEW.email))
            ON CONFLICT (email_hash) DO NOTHING;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_track_blocklist ON subscribers;
DROP TRIGGER IF EXISTS axm_track_blocklist_ins ON subscribers;
DROP TRIGGER IF EXISTS axm_track_blocklist_upd ON subscribers;
-- The WHEN clauses keep the function from running for the ordinary subscriber.
CREATE TRIGGER axm_track_blocklist_ins AFTER INSERT ON subscribers
    FOR EACH ROW WHEN (NEW.status = 'blocklisted') EXECUTE FUNCTION axm_track_blocklist();
CREATE TRIGGER axm_track_blocklist_upd AFTER UPDATE OF status, email ON subscribers
    FOR EACH ROW WHEN (OLD.status = 'blocklisted' OR NEW.status = 'blocklisted') EXECUTE FUNCTION axm_track_blocklist();

-- Deleting a blocklisted subscriber keeps its address suppressed.
CREATE OR REPLACE FUNCTION axm_keep_blocklist()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'blocklisted' THEN
        INSERT INTO axm_suppressed_emails (email_hash, reason) VALUES (axm_email_hash(OLD.email), 'delete')
            ON CONFLICT (email_hash) DO NOTHING;
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS axm_keep_blocklist ON subscribers;
CREATE TRIGGER axm_keep_blocklist BEFORE DELETE ON subscribers
    FOR EACH ROW EXECUTE FUNCTION axm_keep_blocklist();

-- A blocklisted subscriber is never subscribed to anything, whichever way the row arrives.
CREATE OR REPLACE FUNCTION axm_block_subscription()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM subscribers WHERE id = NEW.subscriber_id AND status = 'blocklisted') THEN
        NEW.status = 'unsubscribed';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS axm_block_subscription ON subscriber_lists;
CREATE TRIGGER axm_block_subscription BEFORE INSERT OR UPDATE ON subscriber_lists
    FOR EACH ROW WHEN (NEW.status != 'unsubscribed') EXECUTE FUNCTION axm_block_subscription();
-- End of AXM suppressed addresses.

-- AXM audit log. Keep this block identical to V6_2_1_axm_10.
-- One row per action somebody (or the server) took that changes lists, subscribers, imports,
-- campaigns, roles, users or settings. Who is copied in (user_id has no foreign key) so a
-- row outlives the account. No subscriber e-mail, name or attribute value is ever stored:
-- a subscriber is an ID or a count. Rows are only added; the screen and the API read them,
-- and the daily job deletes the ones past the retention period.
CREATE TABLE IF NOT EXISTS axm_audit_log (
    id               BIGSERIAL PRIMARY KEY,
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    user_id          INTEGER NULL,
    username         TEXT NOT NULL DEFAULT '',
    user_name        TEXT NOT NULL DEFAULT '',
    user_type        TEXT NOT NULL DEFAULT '',
    action           TEXT NOT NULL,
    target_type      TEXT NOT NULL DEFAULT '',
    target_id        INTEGER NULL,
    target_name      TEXT NOT NULL DEFAULT '',
    count            INTEGER NULL,
    detail           JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_axm_audit_created ON axm_audit_log (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_axm_audit_action ON axm_audit_log (action);
CREATE INDEX IF NOT EXISTS idx_axm_audit_user ON axm_audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_axm_audit_target ON axm_audit_log (target_type, target_id);
-- End of AXM audit log.

-- AXM list trash. Keep this block identical to V6_2_1_axm_11.
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
-- End of AXM list trash.

-- AXM column presets. Keep this block identical to V6_2_1_axm_12.
-- Presets people make for the subscribers table: a name and the attribute keys to show, in order.
CREATE TABLE IF NOT EXISTS axm_column_presets (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    columns     TEXT[] NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_column_presets_name ON axm_column_presets (lower(name));
-- End of AXM column presets.

-- AXM recent-send exclusion. Keep this block identical to V6_2_1_axm_13.
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
-- End of AXM recent-send exclusion.

-- AXM test addresses (LST-41). Keep this block identical to V6_2_1_axm_14.
CREATE TABLE IF NOT EXISTS axm_test_addresses (
    user_id           INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE,
    email             TEXT NOT NULL CHECK (email = LOWER(email)),
    token_hash        TEXT NULL,
    token_expires_at  TIMESTAMP WITH TIME ZONE NULL,
    verified_at       TIMESTAMP WITH TIME ZONE NULL,
    last_sent_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_test_addr_token ON axm_test_addresses(token_hash) WHERE token_hash IS NOT NULL;
-- End of AXM test addresses.

-- AXM campaign follow-ups (LST-47). Keep this block identical to V6_2_1_axm_16.
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
-- End of AXM campaign follow-ups.
