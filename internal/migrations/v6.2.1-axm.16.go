package migrations

import (
	"database/sql"
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56), the
// trigger that fills it from the bounce records, the one that releases an address when a person
// deletes the bounce record, and the record of when a subscriber's address changed (a bounce that
// does not say its address is only for the current one when it has not changed since the mail).
// The bounces recorded before are taken in by the address their webhook payload gives, or else
// by that same rule.
func V6_2_1_axm_16(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf, lo *log.Logger) error {
	_, err := db.Exec(`-- AXM bounced addresses (LST-56). Keep this block identical to V6_2_1_axm_16.
-- Addresses that had a hard bounce or a complaint stay known when the subscriber row is deleted
-- (the bounce rows go with it), so that a person cannot add them again. Only a hash of the
-- lowercased address is kept. A row with bounce_id is for that bounce record and goes when a
-- person deletes the record (not when the subscriber is deleted, which takes its bounce rows
-- with it). A row without bounce_id is a bounce that left no record of its own (the subscriber
-- was blocklisted already, or over the count of the bounce action): it goes with the last hard
-- bounce or complaint record of its subscriber_id, or when a person deletes that subscriber's
-- bounces. One with no subscriber at all (the address is nobody's any more) only goes by SQL
-- (FORK.md).
-- RecordBounce names the address the bounce is for in the setting axm.bounce_email of its
-- transaction: the address the bounce gives, or else axm_mailed_email. Failing to keep the
-- address never loses the bounce: the trigger only warns.
CREATE TABLE IF NOT EXISTS axm_bounced_emails (
    email_hash     BYTEA NOT NULL,
    kind           TEXT NOT NULL,
    bounce_id      INT NULL,
    subscriber_id  INT NULL,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_axm_bounced_hash ON axm_bounced_emails(email_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_axm_bounced_bounce ON axm_bounced_emails(bounce_id) WHERE bounce_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_axm_bounced_sub ON axm_bounced_emails(subscriber_id) WHERE bounce_id IS NULL;

-- When a subscriber's address last changed: a bounce that does not say its address is for the
-- current one only when it has not changed since the mail. Before this table, an edit may have
-- changed it: the last edit stands for a change.
CREATE TABLE IF NOT EXISTS axm_email_changes (
    subscriber_id  INT PRIMARY KEY REFERENCES subscribers(id) ON DELETE CASCADE ON UPDATE CASCADE,
    changed_at     TIMESTAMP WITH TIME ZONE NOT NULL
);
INSERT INTO axm_email_changes (subscriber_id, changed_at)
    SELECT id, updated_at FROM subscribers WHERE updated_at > created_at
    ON CONFLICT (subscriber_id) DO NOTHING;

CREATE OR REPLACE FUNCTION axm_email_changed()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO axm_email_changes (subscriber_id, changed_at) VALUES (NEW.id, NOW())
        ON CONFLICT (subscriber_id) DO UPDATE SET changed_at = EXCLUDED.changed_at;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_email_changed ON subscribers;
CREATE TRIGGER axm_email_changed AFTER UPDATE OF email ON subscribers
    FOR EACH ROW WHEN (OLD.email IS DISTINCT FROM NEW.email)
    EXECUTE FUNCTION axm_email_changed();

-- The address a mail of the campaign (NULL: an opt-in or a transactional mail, or not known)
-- went to, when it is surely the subscriber's current one: it never changed, or it changed more
-- than an hour (a send in progress) before the recorded send of that campaign to them. NULL
-- otherwise: a send record that is gone (retention) proves nothing.
CREATE OR REPLACE FUNCTION axm_mailed_email(p_subscriber INT, p_campaign INT)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT s.email FROM subscribers s
    WHERE s.id = p_subscriber AND NOT EXISTS (
        SELECT 1 FROM axm_email_changes c WHERE c.subscriber_id = s.id AND c.changed_at >= COALESCE(
            (SELECT x.sent_at - INTERVAL '1 hour' FROM axm_campaign_sends x
                WHERE x.campaign_id = p_campaign AND x.subscriber_id = s.id), '-infinity'))
$$;

-- The bounces recorded before: the webhooks say in their payload which address a bounce was for,
-- each in its own field. The others are taken in by axm_mailed_email. SES and SendGrid are taken
-- in by the migration's Go code (V6_2_1_axm_16): an SES notification can name several bounced
-- recipients, and SendGrid's payload is a whole batch of events.
INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
    SELECT axm_email_hash(a.addr), b.type::TEXT, b.id, b.subscriber_id
    FROM bounces b
    CROSS JOIN LATERAL (SELECT COALESCE(CASE b.source
        WHEN 'postmark' THEN NULLIF(b.meta->>'Email', '')
        WHEN 'forwardemail' THEN NULLIF(b.meta->>'recipient', '')
        WHEN 'lettermint' THEN NULLIF(b.meta->'data'->>'recipient', '')
        WHEN 'azure' THEN NULLIF(b.meta->'data'->>'recipient', '')
        END, axm_mailed_email(b.subscriber_id, b.campaign_id)) AS addr) a
    WHERE b.type IN ('hard', 'complaint') AND b.source NOT IN ('ses', 'sendgrid') AND a.addr LIKE '%@%'
    ON CONFLICT (bounce_id) WHERE bounce_id IS NOT NULL DO NOTHING;

CREATE OR REPLACE FUNCTION axm_bounced_record()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_email TEXT := NULLIF(current_setting('axm.bounce_email', true), '');
BEGIN
    IF v_email IS NOT NULL THEN
        BEGIN
            INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
                VALUES (axm_email_hash(v_email), NEW.type::TEXT, NEW.id, NEW.subscriber_id);
            PERFORM set_config('axm.bounce_recorded', '1', true);
        EXCEPTION WHEN OTHERS THEN
            -- The bounce itself must not be lost for the sake of the address.
            RAISE WARNING 'axm_bounced_record: %', SQLERRM;
        END;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_bounced_record ON bounces;
CREATE TRIGGER axm_bounced_record AFTER INSERT ON bounces
    FOR EACH ROW WHEN (NEW.type IN ('hard', 'complaint'))
    EXECUTE FUNCTION axm_bounced_record();

CREATE OR REPLACE FUNCTION axm_bounced_release()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    -- The subscriber is gone when the bounce rows are deleted with it: the address stays.
    IF EXISTS (SELECT 1 FROM subscribers WHERE id = OLD.subscriber_id) THEN
        DELETE FROM axm_bounced_emails WHERE bounce_id = OLD.id;
        IF NOT EXISTS (SELECT 1 FROM bounces WHERE subscriber_id = OLD.subscriber_id AND type IN ('hard', 'complaint')) THEN
            DELETE FROM axm_bounced_emails WHERE subscriber_id = OLD.subscriber_id AND bounce_id IS NULL;
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS axm_bounced_release ON bounces;
CREATE TRIGGER axm_bounced_release AFTER DELETE ON bounces
    FOR EACH ROW WHEN (OLD.type IN ('hard', 'complaint'))
    EXECUTE FUNCTION axm_bounced_release();
-- End of AXM bounced addresses.`)
	if err != nil {
		return err
	}
	if err := axm16SES(db); err != nil {
		return err
	}
	return axm16Sendgrid(db)
}

// The SES and SendGrid bounces recorded before LST-56 are taken in here, in Go: rows are read a
// page at a time and written after, so that this also runs on a pool of one connection.

const (
	// axm16Link keeps an address for a bounce record.
	axm16Link = `INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
		VALUES (axm_email_hash($1::TEXT), $2::TEXT, $3::INT, $4::INT) ON CONFLICT (bounce_id) WHERE bounce_id IS NOT NULL DO NOTHING`
	// axm16Unlinked keeps an address that no bounce record of its own stands for, for the
	// subscriber who has it now (it goes with their bounce records), or for nobody (only SQL lets
	// it go). Once per address and subscriber.
	axm16Unlinked = `INSERT INTO axm_bounced_emails (email_hash, kind, subscriber_id)
		SELECT axm_email_hash($1::TEXT), $2::TEXT, (SELECT id FROM subscribers WHERE email = $1::TEXT)
		WHERE NOT EXISTS (SELECT 1 FROM axm_bounced_emails b WHERE b.email_hash = axm_email_hash($1) AND b.bounce_id IS NULL
			AND b.subscriber_id IS NOT DISTINCT FROM (SELECT id FROM subscribers WHERE email = $1))`
	// axm16Mailed keeps the subscriber's own address when it surely is the one the mail went to.
	axm16Mailed = `INSERT INTO axm_bounced_emails (email_hash, kind, bounce_id, subscriber_id)
		SELECT axm_email_hash(a), $1::TEXT, $2::INT, $3::INT FROM (SELECT axm_mailed_email($3::INT, $4::INT) AS a) x WHERE a LIKE '%@%'
		ON CONFLICT (bounce_id) WHERE bounce_id IS NOT NULL DO NOTHING`
)

type axm16Bounce struct {
	ID         int            `db:"id"`
	SubID      int            `db:"subscriber_id"`
	CampaignID sql.NullInt64  `db:"campaign_id"`
	Camp       sql.NullString `db:"camp"`
	Type       string         `db:"type"`
	Meta       []byte         `db:"meta"`
	Epoch      int64          `db:"epoch"`
	CreatedAt  string         `db:"created_at"`
	// Own is the subscriber's address when it did not change since the bounce: the webhooks
	// that give an address found the subscriber by it.
	Own sql.NullString `db:"own"`
}

const axm16Columns = `b.id, b.subscriber_id, b.campaign_id, (SELECT c.uuid::TEXT FROM campaigns c WHERE c.id = b.campaign_id) AS camp,
	b.type::TEXT AS type, b.meta, EXTRACT(EPOCH FROM b.created_at)::BIGINT AS epoch, b.created_at::TEXT AS created_at,
	(SELECT s.email FROM subscribers s WHERE s.id = b.subscriber_id AND NOT EXISTS (
		SELECT 1 FROM axm_email_changes x WHERE x.subscriber_id = s.id AND x.changed_at >= b.created_at)) AS own`

func axm16Permanent(typ string) bool { return typ == "hard" || typ == "complaint" }

// axm16SES: an SES notification names its bounced (complaining) recipients apart from the mail's
// recipients, and the collector before LST-56 recorded it for the subscriber of the mail's first
// recipient, whoever bounced. Each named recipient is kept: the first recipient with that record;
// another one that a subscriber has now with a record of its own for that subscriber (a copy of
// the notification marked axm_recipient, so that deleting it lets the address go, and deleting the
// first recipient's record does not); one nobody has as an address of nobody's.
func axm16SES(db *sqlx.DB) error {
	last := 0
	for {
		var rows []axm16Bounce
		if err := db.Select(&rows, `SELECT `+axm16Columns+` FROM bounces b
			WHERE b.source = 'ses' AND b.type IN ('hard', 'complaint') AND b.id > $1 ORDER BY b.id LIMIT 500`, last); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, b := range rows {
			last = b.ID
			if err := axm16OneSES(db, b); err != nil {
				return err
			}
		}
	}
}

func axm16OneSES(db *sqlx.DB, b axm16Bounce) error {
	var m struct {
		Recipient string `json:"axm_recipient"`
		Bounce    struct {
			Recipients []struct {
				Email string `json:"emailAddress"`
			} `json:"bouncedRecipients"`
		} `json:"bounce"`
		Complaint struct {
			Recipients []struct {
				Email string `json:"emailAddress"`
			} `json:"complainedRecipients"`
		} `json:"complaint"`
		Mail struct {
			Destination []string `json:"destination"`
		} `json:"mail"`
	}
	if err := json.Unmarshal(b.Meta, &m); err != nil {
		// Not a notification: the subscriber's own address if it is surely the one.
		_, err := db.Exec(axm16Mailed, b.Type, b.ID, b.SubID, b.CampaignID)
		return err
	}
	if r := axm16Clean(m.Recipient); r != "" {
		_, err := db.Exec(axm16Link, r, b.Type, b.ID, b.SubID)
		return err
	}
	var named []string
	seen := map[string]bool{}
	for _, r := range m.Bounce.Recipients {
		if e := axm16Clean(r.Email); e != "" && !seen[e] {
			seen[e] = true
			named = append(named, e)
		}
	}
	for _, r := range m.Complaint.Recipients {
		if e := axm16Clean(r.Email); e != "" && !seen[e] {
			seen[e] = true
			named = append(named, e)
		}
	}
	first := ""
	if len(m.Mail.Destination) > 0 {
		first = axm16Clean(m.Mail.Destination[0])
	}
	if len(named) == 0 {
		if len(m.Mail.Destination) == 1 && first != "" {
			_, err := db.Exec(axm16Link, first, b.Type, b.ID, b.SubID)
			return err
		}
		_, err := db.Exec(axm16Mailed, b.Type, b.ID, b.SubID, b.CampaignID)
		return err
	}
	for _, r := range named {
		if r == first {
			if _, err := db.Exec(axm16Link, r, b.Type, b.ID, b.SubID); err != nil {
				return err
			}
			continue
		}
		var owner sql.NullInt64
		if err := db.Get(&owner, `SELECT (SELECT id FROM subscribers WHERE email = $1)`, r); err != nil {
			return err
		}
		if !owner.Valid || int(owner.Int64) == b.SubID {
			if _, err := db.Exec(axm16Unlinked, r, b.Type); err != nil {
				return err
			}
			continue
		}
		// The record of its own, made once.
		var id int
		err := db.Get(&id, `SELECT id FROM bounces WHERE subscriber_id = $1 AND source = 'ses' AND created_at = $2::TIMESTAMPTZ
			AND meta->>'axm_recipient' = $3 ORDER BY id LIMIT 1`, owner.Int64, b.CreatedAt, r)
		if err == sql.ErrNoRows {
			err = db.Get(&id, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at)
				SELECT $1, campaign_id, type, source, meta || jsonb_build_object('axm_recipient', $2::TEXT), created_at
				FROM bounces WHERE id = $3 RETURNING id`, owner.Int64, r, b.ID)
		}
		if err != nil {
			return err
		}
		if _, err := db.Exec(axm16Link, r, b.Type, id, owner.Int64); err != nil {
			return err
		}
	}
	return nil
}

func axm16Clean(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if !strings.Contains(e, "@") {
		return ""
	}
	return e
}

// axm16Sendgrid: SendGrid's webhook keeps the whole batch of events with each bounce it records.
// A record's address is the bounce event of its time, type and campaign. When several match (the
// same second and campaign), the records of one batch were written in the order of its bounce
// events, so when the batch left as many records as it has bounce events, the record's place says
// which event it is. Else the subscriber's own address when it did not change since; else every
// matching address is kept (they all bounced), for whoever has it now.
func axm16Sendgrid(db *sqlx.DB) error {
	type group struct {
		Key  string        `db:"k"`
		Meta []byte        `db:"meta"`
		IDs  pq.Int64Array `db:"ids"`
	}
	last := ""
	for {
		var groups []group
		if err := db.Select(&groups, `SELECT md5(b.meta::TEXT) AS k, b.meta, array_agg(b.id ORDER BY b.id) AS ids
			FROM bounces b WHERE b.source = 'sendgrid' AND md5(b.meta::TEXT) > $1
			GROUP BY 1, 2 ORDER BY 1 LIMIT 200`, last); err != nil {
			return err
		}
		if len(groups) == 0 {
			return nil
		}
		for _, g := range groups {
			last = g.Key
			if err := axm16OneBatch(db, g.Meta, g.IDs); err != nil {
				return err
			}
		}
	}
}

type axm16Event struct {
	Email string          `json:"email"`
	Event string          `json:"event"`
	TS    json.RawMessage `json:"timestamp"`
	Class string          `json:"bounce_classification"`
	Camp  string          `json:"XListmonkCampaign"`
}

func (e axm16Event) typ() string {
	if e.Class == "technical" || e.Class == "content" {
		return "soft"
	}
	return "hard"
}

func (e axm16Event) matches(b axm16Bounce) bool {
	ts := strings.Trim(string(e.TS), `"`)
	return axm16Clean(e.Email) != "" && ts == strconv.FormatInt(b.Epoch, 10) && e.typ() == b.Type &&
		(!b.Camp.Valid || strings.EqualFold(strings.TrimSpace(e.Camp), b.Camp.String))
}

func axm16OneBatch(db *sqlx.DB, meta []byte, ids pq.Int64Array) error {
	var rows []axm16Bounce
	if err := db.Select(&rows, `SELECT `+axm16Columns+` FROM bounces b WHERE b.id = ANY($1) ORDER BY b.id`, ids); err != nil {
		return err
	}
	var all []axm16Event
	_ = json.Unmarshal(meta, &all) // not a list: no events, each record falls back below
	var events []axm16Event
	for _, e := range all {
		if e.Event == "bounce" {
			events = append(events, e)
		}
	}
	for i, b := range rows {
		if !axm16Permanent(b.Type) {
			continue
		}
		var match []string
		seen := map[string]bool{}
		for _, e := range events {
			if a := axm16Clean(e.Email); e.matches(b) && !seen[a] {
				seen[a] = true
				match = append(match, a)
			}
		}
		addr := ""
		switch {
		case len(match) == 0:
			if _, err := db.Exec(axm16Mailed, b.Type, b.ID, b.SubID, b.CampaignID); err != nil {
				return err
			}
			continue
		case len(match) == 1:
			addr = match[0]
		case len(rows) == len(events) && events[i].matches(b):
			addr = axm16Clean(events[i].Email)
		case b.Own.Valid && seen[strings.ToLower(b.Own.String)]:
			addr = strings.ToLower(b.Own.String)
		}
		if addr != "" {
			if _, err := db.Exec(axm16Link, addr, b.Type, b.ID, b.SubID); err != nil {
				return err
			}
			continue
		}
		for _, a := range match {
			if _, err := db.Exec(axm16Unlinked, a, b.Type); err != nil {
				return err
			}
		}
	}
	return nil
}
