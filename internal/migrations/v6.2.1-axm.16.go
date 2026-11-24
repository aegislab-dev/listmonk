package migrations

import (
	"crypto/sha256"
	"encoding/json"
	"log"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

// V6_2_1_axm_16 adds the table of addresses that had a hard bounce or a complaint (LST-56), the
// trigger that fills it from the bounce records, the one that releases an address when a person
// deletes the bounce record, and the record of when a subscriber's address changed (a bounce that
// does not say its address is only for the current one when it has not changed since the mail).
// The bounces recorded before are taken in by the address their webhook payload gives, or else
// by that same rule; those of SES and SendGrid by the addresses their notifications name alone
// (axm16Notifications).
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
-- in by the migration's Go code (V6_2_1_axm_16), by the addresses their notifications name alone,
-- tied to no record or subscriber: a person lets such an address go by deleting its row (FORK.md).
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
	kept, unjudged, err := axm16Notifications(db)
	if err != nil {
		return err
	}
	if lo != nil {
		lo.Printf("bounced addresses from earlier SES and SendGrid notifications: %d kept, %d not judged (not kept)", kept, unjudged)
	}
	return nil
}

// The SES and SendGrid bounces recorded before LST-56 (decided 2026-11-24): every address a stored
// notification names as hard bounced or complaining is kept, by the address alone. It is not tied
// to a bounce record or a subscriber (bounce_id and subscriber_id are NULL): the records, the
// subscribers' names and addresses, the order of a batch and what was deleted since do not decide
// anything. Such a row goes only when a person deletes it (SQL, FORK.md). What can not be judged
// (a notification that does not parse, a complaint that names several recipients who may have
// complained, a bounce that names no recipient of a mail to several) is not kept, only counted.
// Rows are read a page at a time and written after, so that this also runs on a pool of one
// connection.

// axm16Address keeps an address by itself, once.
const axm16Address = `INSERT INTO axm_bounced_emails (email_hash, kind)
	SELECT axm_email_hash($1::TEXT), $2::TEXT
	WHERE NOT EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1::TEXT)
		AND bounce_id IS NULL AND subscriber_id IS NULL)`

type axm16Row struct {
	ID     int    `db:"id"`
	Source string `db:"source"`
	Type   string `db:"type"`
	Meta   []byte `db:"meta"`
}

func axm16Notifications(db *sqlx.DB) (kept, unjudged int, err error) {
	// SendGrid keeps the whole batch with each record of it: a batch is read once.
	done := map[[32]byte]bool{}
	keep := func(email, kind string) error {
		res, err := db.Exec(axm16Address, email, kind)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		kept += int(n)
		return nil
	}
	last := 0
	for {
		var rows []axm16Row
		if err := db.Select(&rows, `SELECT id, source, type::TEXT AS type, meta FROM bounces
			WHERE source IN ('ses', 'sendgrid') AND id > $1 ORDER BY id LIMIT 500`, last); err != nil {
			return kept, unjudged, err
		}
		if len(rows) == 0 {
			return kept, unjudged, nil
		}
		for _, r := range rows {
			last = r.ID
			var addrs []axm16Recipient
			var n int
			if r.Source == "sendgrid" {
				key := sha256.Sum256(r.Meta)
				if done[key] {
					continue
				}
				done[key] = true
				addrs, n = axm16SendgridAddresses(r.Meta)
			} else {
				addrs, n = axm16SESAddresses(r.Meta, r.Type)
			}
			unjudged += n
			for _, a := range addrs {
				if err := keep(a.Email, a.Type); err != nil {
					return kept, unjudged, err
				}
			}
		}
	}
}

// axm16Recipient is an address of a notification with its own type.
type axm16Recipient struct{ Email, Type string }

type axm16SESMail struct {
	NotifType string `json:"notificationType"`
	Bounce    struct {
		BounceType string `json:"bounceType"`
		Recipients []struct {
			Email  string `json:"emailAddress"`
			Action string `json:"action"`
			Status string `json:"status"`
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

// axm16SESAddresses are the hard bounced or complaining addresses of an SES notification, by the
// rules of the webhook (internal/bounce/webhooks/ses.go sesRecipients): each bounced recipient
// typed by its own status (a delayed one in a Permanent bounce is soft; 5.4.4 in a Transient one
// is hard, whatever the first recipient had: the collector before typed the whole notification,
// and so the record, by the first), or the one complaining recipient. typ is the type the record
// has. It also returns how many could not be judged.
func axm16SESAddresses(meta []byte, typ string) ([]axm16Recipient, int) {
	permanent := typ == "hard" || typ == "complaint"
	var m axm16SESMail
	if err := json.Unmarshal(meta, &m); err != nil {
		return nil, 1
	}
	unjudged := 0
	if typ == "complaint" || m.NotifType == "Complaint" {
		// Every recipient named counts as one who may have complained, also one whose address can
		// not be read: a complaint is kept only when it names one recipient, and that one reads.
		var named []string
		raw := map[string]bool{}
		for _, r := range m.Complaint.Recipients {
			v := strings.ToLower(strings.TrimSpace(r.Email))
			if v == "" || raw[v] {
				continue
			}
			raw[v] = true
			named = append(named, v)
		}
		switch {
		case len(named) == 1:
			if e := axm16Clean(named[0]); e != "" {
				return []axm16Recipient{{e, "complaint"}}, 0
			}
			return nil, 1
		case len(named) > 1:
			// Only the ones who may have complained: nobody is accused.
			return nil, 1
		}
	} else {
		// One address may be named more than once (in other letter cases): it is hard when any of
		// its entries is.
		var order []string
		hard := map[string]bool{}
		listed := map[string]bool{}
		named := false
		for _, r := range m.Bounce.Recipients {
			if strings.TrimSpace(r.Email) != "" {
				named = true
			}
			e := axm16Clean(r.Email)
			if e == "" {
				if strings.TrimSpace(r.Email) != "" {
					unjudged++
				}
				continue
			}
			if !listed[e] {
				listed[e] = true
				order = append(order, e)
			}
			if axm16SESType(m.Bounce.BounceType, r.Action, r.Status) == "hard" {
				hard[e] = true
			}
		}
		// Recipients named: those are the bounce, also when none of them reads (then nobody, not
		// the mail's other recipients).
		if named {
			var out []axm16Recipient
			for _, e := range order {
				if hard[e] {
					out = append(out, axm16Recipient{e, "hard"})
				}
			}
			return out, unjudged
		}
	}
	// None named: the mail's recipient when it had only one, for a record of a hard bounce or a
	// complaint. A mail to several recipients that names none is not judged, whatever its type.
	if len(m.Mail.Destination) == 1 {
		if e := axm16Clean(m.Mail.Destination[0]); e != "" {
			if permanent {
				return []axm16Recipient{{e, typ}}, unjudged
			}
			return nil, unjudged
		}
	}
	return nil, unjudged + 1
}

func axm16SESType(bounceType, action, status string) string {
	status = strings.TrimSpace(status)
	switch bounceType {
	case "Permanent":
		if strings.EqualFold(strings.TrimSpace(action), "delayed") || strings.HasPrefix(status, "4") {
			return "soft"
		}
		return "hard"
	case "Transient":
		if status == "5.4.4" {
			return "hard"
		}
	}
	return "soft"
}

// axm16SendgridAddresses are the hard bounced and complaining addresses of a SendGrid batch, as
// SendGrid defines its events: a bounce event that is not soft (bounce_classification technical or
// content, or type blocked: a temporary block), and a spamreport event (the recipient reported the
// mail as spam: a complaint). It also returns how many could not be judged.
func axm16SendgridAddresses(meta []byte) ([]axm16Recipient, int) {
	var events []struct {
		Email string `json:"email"`
		Event string `json:"event"`
		Type  string `json:"type"`
		Class string `json:"bounce_classification"`
	}
	if err := json.Unmarshal(meta, &events); err != nil {
		return nil, 1
	}
	var out []axm16Recipient
	n := 0
	for _, e := range events {
		kind := ""
		switch {
		case e.Event == "spamreport":
			kind = "complaint"
		case e.Event == "bounce" && !strings.EqualFold(e.Type, "blocked") && e.Class != "technical" && e.Class != "content":
			kind = "hard"
		default:
			continue
		}
		a := axm16Clean(e.Email)
		if a == "" {
			n++
			continue
		}
		out = append(out, axm16Recipient{a, kind})
	}
	return out, n
}

// axm16Clean is the address lowercased and trimmed, or "" when it is not an address: one @, with
// something before and after it, and no space. An address literal (local@[2001:db8::1]) and a
// one-label domain (local@internal) are addresses: the app takes them in.
func axm16Clean(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	at := strings.IndexByte(e, '@')
	if at <= 0 || at != strings.LastIndexByte(e, '@') || at == len(e)-1 || strings.ContainsAny(e, " \t\r\n") {
		return ""
	}
	return e
}
