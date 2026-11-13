package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/knadh/listmonk/models"
)

// bodyMessenger stands in for the SMTP messenger and keeps what each subscriber was sent.
type bodyMessenger struct {
	mu   sync.Mutex
	sent map[int]models.Message
}

func (b *bodyMessenger) Name() string { return "email" }
func (b *bodyMessenger) Flush() error { return nil }
func (b *bodyMessenger) Close() error { return nil }

func (b *bodyMessenger) Push(m models.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sent == nil {
		b.sent = map[int]models.Message{}
	}
	b.sent[m.Subscriber.ID] = m
	return nil
}

func (b *bodyMessenger) get(id int) (models.Message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.sent[id]
	return m, ok
}

// LST-22: a campaign run prints the attribute numbers that Postgres holds as digits, in the
// subject and in the body. Before, a follower count of a million or more went out as
// 1.234567e+06.
func TestCampaignRunPrintsAttributeNumbersAsDigits(t *testing.T) {
	e := newSendsEnv(t, 5)
	attribs := []string{
		`{"follower": 1234567}`,
		`{"follower": 1000000, "stats": {"posts": 2500000}}`,
		`{"follower": 999999}`,
		`{"follower": "1,234,567"}`,
		`{}`,
	}
	want := []string{
		"<p>1234567 / none / 1500000</p>",
		"<p>1000000 / 2500000 / 1500000</p>",
		"<p>999999 / none / 1500000</p>",
		"<p>1,234,567 / none / 1500000</p>",
		"<p>none / none / 1500000</p>",
	}
	wantSubject := []string{"1234567", "1000000", "999999", "1,234,567", "0"}
	for i, a := range attribs {
		e.db.MustExec(`UPDATE subscribers SET attribs = $2::JSONB WHERE id = $1`, e.eligible[i], a)
	}
	e.db.MustExec(`UPDATE campaigns SET attribs = '{"price": 1500000}',
		subject = '{{ .Subscriber.Attribs.follower | default "0" }}',
		body = '<p>{{ .Subscriber.Attribs.follower | default "none" }} / {{ if .Subscriber.Attribs.stats }}{{ .Subscriber.Attribs.stats.posts }}{{ else }}none{{ end }} / {{ .Campaign.Attribs.price }}</p>'
		WHERE id = $1`, e.campID)

	msgr := &bodyMessenger{}
	e.startManager(e.store, msgr, io.Discard)
	e.waitCampaign("finished", len(e.eligible))

	for i, id := range e.eligible {
		m, ok := msgr.get(id)
		if !ok {
			t.Errorf("subscriber %d got no message", id)
			continue
		}
		if string(m.Body) != want[i] || m.Subject != wantSubject[i] {
			t.Errorf("attribs %s: subject %q body %q, want %q %q", attribs[i], m.Subject, m.Body, wantSubject[i], want[i])
		}
	}
}

// The public archive renders the subject and the body with the campaign's archive meta
// (a subscriber written as JSON) on a path of its own.
func TestArchivePrintsAttributeNumbersAsDigits(t *testing.T) {
	e := newSendsEnv(t, 0)
	e.setStatus("draft")
	m := e.startManager(e.store, &bodyMessenger{}, io.Discard)
	a := &App{manager: m, log: log.New(io.Discard, "", 0)}

	camp := models.Campaign{
		UUID: "campaign", ContentType: models.CampaignContentTypeHTML, Messenger: "email",
		Subject:     "{{ .Subscriber.Attribs.follower }} followers",
		Body:        "<p>{{ .Subscriber.Attribs.follower }}</p>",
		ArchiveMeta: json.RawMessage(`{"email": "archive@example.invalid", "name": "Archive", "attribs": {"follower": 1234567}}`),
	}
	out, err := a.compileArchiveCampaigns([]models.Campaign{camp})
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].Campaign.Subject; got != "1234567 followers" {
		t.Errorf("archive subject = %q", got)
	}
	msg, err := m.NewCampaignMessage(out[0].Campaign, out[0].Subscriber)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%s", msg.Body()); got != "<p>1234567</p>" {
		t.Errorf("archive body = %q", got)
	}
}
