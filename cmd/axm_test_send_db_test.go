package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// LST-41: campaign test messages to addresses that are not subscribers, with the real
// handlers, queries and manager against the disposable Postgres of AXM_TEST_DB_URL.

// listMessenger keeps every message it is given.
type listMessenger struct {
	mu   sync.Mutex
	sent []models.Message
	// fail makes every push an error.
	fail bool
}

func (l *listMessenger) Name() string { return "email" }
func (l *listMessenger) Flush() error { return nil }
func (l *listMessenger) Close() error { return nil }

func (l *listMessenger) Push(m models.Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return errors.New("synthetic mail server error")
	}
	l.sent = append(l.sent, m)
	return nil
}

func (l *listMessenger) all() []models.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]models.Message{}, l.sent...)
}

// to returns the messages that went to an address.
func (l *listMessenger) to(email string) []models.Message {
	out := []models.Message{}
	for _, m := range l.all() {
		if len(m.To) == 1 && m.To[0] == email {
			out = append(out, m)
		}
	}
	return out
}

const (
	testSendDomain = "corp.example.invalid"
	testSendBody   = `<p>{{ .Subscriber.Name }} / {{ .Subscriber.Attribs.company }} / {{ .Subscriber.Email }}</p>` +
		`<a href="{{ TrackLink "https://example.invalid/offer" }}">offer</a>{{ TrackView }}<a href="{{ UnsubscribeURL }}">unsub</a>`
)

// testSendEnv is teamsEnv with a manager that sends to a listMessenger, a draft campaign on
// team 가's list and three subscribers: one on that list, one on team 나's list and one on both.
type testSendEnv struct {
	*teamsEnv
	msgr *listMessenger
	sys  *listMessenger
	now  time.Time
	// The body the next test request sends.
	body string

	camp     int
	campUUID string
	// Subscribers and their addresses.
	mine, foreign           int
	mineEmail, foreignEmail string
}

func newTestSendEnv(t *testing.T) *testSendEnv { return newTestSendEnvTracking(t, true) }

// newTestSendEnvTracking chooses the individual tracking of both the manager, which writes
// the links into a message, and the handlers that receive them.
func newTestSendEnvTracking(t *testing.T, individual bool) *testSendEnv {
	e := &testSendEnv{teamsEnv: newTeamsEnv(t), msgr: &listMessenger{}, now: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC), body: testSendBody}
	m := e.startManagerWith(e.store, e.msgr, io.Discard, func(c *manager.Config) {
		c.ScanCampaigns = false
		c.IndividualTracking = individual
	})
	t.Cleanup(m.Close)
	e.a.manager = m
	e.a.log = log.New(io.Discard, "", 0)
	// System mails (the own address confirmation) go to a messenger of their own.
	e.sys = &listMessenger{}
	e.a.emailMsgr = e.sys
	e.a.urlCfg = &UrlConfig{RootURL: "https://lists.example.invalid"}
	e.a.cfg = &Config{TestDomains: []string{testSendDomain}, FromEmail: "Sender <sender@example.invalid>"}
	e.a.cfg.Privacy.IndividualTracking = individual
	e.a.testLimiter.now = func() time.Time { return e.now }

	e.camp, e.campUUID = e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.camp, e.aList)
	e.mine, e.foreign = e.addSub("enabled", "confirmed"), e.addSub("enabled", "confirmed")
	e.db.MustExec(`UPDATE subscriber_lists SET list_id=$2 WHERE subscriber_id=$1`, e.mine, e.aList)
	e.db.MustExec(`UPDATE subscriber_lists SET list_id=$2 WHERE subscriber_id=$1`, e.foreign, e.bList)
	e.db.MustExec(`UPDATE subscribers SET name='김하늘', attribs='{"company":"하늘상사"}' WHERE id=$1`, e.mine)
	e.db.MustExec(`UPDATE subscribers SET name='이바다', attribs='{"company":"바다물산"}' WHERE id=$1`, e.foreign)
	e.get(&e.mineEmail, `SELECT email FROM subscribers WHERE id=$1`, e.mine)
	e.get(&e.foreignEmail, `SELECT email FROM subscribers WHERE id=$1`, e.foreign)
	// The member's own mailbox has confirmed its address.
	e.confirmOwn(e.ua, "ua@example.invalid")
	return e
}

// do runs a handler as a user. For an error answer it returns the echo error, whose message
// is what the client would get as JSON.
func (e *testSendEnv) do(h echo.HandlerFunc, user auth.User, req *http.Request, id int, params map[string]string) (*httptest.ResponseRecorder, *echo.HTTPError) {
	e.t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, user)
	if id > 0 {
		c.Set("id", id)
	}
	names, values := []string{}, []string{}
	for k, v := range params {
		names, values = append(names, k), append(values, v)
	}
	c.SetParamNames(names...)
	c.SetParamValues(values...)
	if err := h(c); err != nil {
		he, ok := err.(*echo.HTTPError)
		if !ok {
			e.t.Fatal(err)
		}
		return rec, he
	}
	return rec, nil
}

// send asks for a test of the campaign and returns the status and, when addresses were
// rejected, those addresses.
func (e *testSendEnv) send(user auth.User, emails []string, sample string) (int, []string) {
	e.t.Helper()
	body := map[string]any{
		"name": "시험 캠페인", "subject": "{{ .Subscriber.Name }} 님께", "lists": []int{e.aList},
		"from_email": "Sender <sender@example.invalid>", "messenger": "email", "content_type": "html",
		"body": e.body, "subscribers": emails,
	}
	if sample != "" {
		body["sample"] = json.RawMessage(sample)
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(string(b)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec, he := e.do(e.a.TestCampaign, user, req, e.camp, nil)
	if he == nil {
		return rec.Code, nil
	}
	if m, ok := he.Message.(map[string]any); ok {
		data, _ := m["data"].(map[string]any)
		rejected, _ := data["rejected"].([]string)
		if m["message"] == nil || rejected == nil {
			e.t.Fatalf("a rejection without message or addresses: %v", he.Message)
		}
		return he.Code, rejected
	}
	return he.Code, nil
}

// confirmOwn records that the mailbox email confirmed it as the user's own test address.
func (e *testSendEnv) confirmOwn(userID int, email string) {
	e.t.Helper()
	e.db.MustExec(`INSERT INTO axm_test_addresses (user_id, email, verified_at) VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET email=$2, verified_at=NOW(), token_hash=NULL`, userID, email)
}

func (e *testSendEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	e.get(&n, query, args...)
	return n
}

// waitSent waits until the messenger holds n messages, and fails when more arrive.
func (e *testSendEnv) waitSent(n int) {
	e.t.Helper()
	waitFor(e.t, fmt.Sprintf("%d messages", n), func() bool { return len(e.msgr.all()) >= n })
	time.Sleep(30 * time.Millisecond)
	if got := len(e.msgr.all()); got != n {
		e.t.Fatalf("%d messages were sent, want %d", got, n)
	}
}

// An address in a test domain and the user's own address receive a test without becoming
// subscribers, and the test is on the audit log without any address.
func TestTestSendToNonSubscribers(t *testing.T) {
	e := newTestSendEnv(t)
	member := e.as(e.ua)
	subsBefore, linksBefore := e.count(`SELECT COUNT(*) FROM subscribers`), e.count(`SELECT COUNT(*) FROM subscriber_lists`)

	internal := "design.team@" + testSendDomain
	if code, rejected := e.send(member, []string{" Design.Team@" + strings.ToUpper(testSendDomain) + " "}, ""); code != 200 || rejected != nil {
		t.Fatalf("test domain address: %d %v", code, rejected)
	}
	e.waitSent(1)
	got := e.msgr.to(internal)
	if len(got) != 1 {
		t.Fatalf("messages to %s: %d", internal, len(got))
	}
	// The default sample: the part before the "@" as the name, the demo attributes.
	if body := string(got[0].Body); !strings.HasPrefix(body, "<p>design.team /  / "+internal+"</p>") || got[0].Subject != "design.team 님께" {
		t.Errorf("subject %q body %q", got[0].Subject, body)
	}
	if got[0].Subscriber.ID != 0 || got[0].Subscriber.UUID != dummyUUID {
		t.Errorf("rendered for subscriber %d %s", got[0].Subscriber.ID, got[0].Subscriber.UUID)
	}
	// Every link of the message carries the dummy campaign and subscriber UUIDs, and nothing
	// in it or in its headers names the real campaign.
	if strings.Count(string(got[0].Body), "/"+dummyUUID+"/"+dummyUUID) != 3 || strings.Contains(string(got[0].Body), e.campUUID) ||
		got[0].Headers.Get(models.EmailHeaderCampaignUUID) != dummyUUID {
		t.Errorf("tracking links: %s", got[0].Body)
	}

	// The user's own address, the test domain and a subscriber in one request.
	own := "ua@example.invalid"
	if code, rejected := e.send(member, []string{own, internal, e.mineEmail}, ""); code != 200 || rejected != nil {
		t.Fatalf("own, internal and subscriber: %d %v", code, rejected)
	}
	e.waitSent(4)
	if m := e.msgr.to(own); len(m) != 1 || !strings.HasPrefix(string(m[0].Body), "<p>ua /  / "+own+"</p>") || m[0].Subscriber.UUID != dummyUUID {
		t.Errorf("own address: %+v", m)
	}
	// With the default sample a subscriber gets their own values and links.
	if m := e.msgr.to(e.mineEmail); len(m) != 1 || !strings.HasPrefix(string(m[0].Body), "<p>김하늘 / 하늘상사 / "+e.mineEmail+"</p>") ||
		m[0].Subscriber.ID != e.mine || m[0].Subscriber.UUID != e.subUUID(e.mine) ||
		strings.Count(string(m[0].Body), "/"+e.campUUID+"/"+e.subUUID(e.mine)) != 3 {
		t.Errorf("subscriber: %+v", m)
	}

	// Nobody became a subscriber.
	if a, b := e.count(`SELECT COUNT(*) FROM subscribers`), e.count(`SELECT COUNT(*) FROM subscriber_lists`); a != subsBefore || b != linksBefore {
		t.Errorf("subscribers %d → %d, subscriptions %d → %d", subsBefore, a, linksBefore, b)
	}
	// No test message is a send record of the campaign.
	if n := e.count(`SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id=$1`, e.camp); n != 0 {
		t.Errorf("%d send records", n)
	}

	// The audit log: one row per request, the count and the kinds, and no address anywhere.
	var rows []struct {
		Count  int    `db:"count"`
		Detail string `db:"detail"`
		Name   string `db:"target_name"`
		UserID int    `db:"user_id"`
	}
	if err := e.db.Select(&rows, `SELECT count, detail::TEXT AS detail, target_name, user_id FROM axm_audit_log
		WHERE action='campaign.test' AND target_type='campaign' AND target_id=$1 ORDER BY id`, e.camp); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d audit rows, want 2", len(rows))
	}
	wantDetail := []map[string]any{
		{"subscribers": 0.0, "own": 0.0, "internal": 1.0, "domains": []any{testSendDomain}, "sample": "default"},
		{"subscribers": 1.0, "own": 1.0, "internal": 1.0, "domains": []any{testSendDomain, "example.invalid"}, "sample": "default"},
	}
	for i, r := range rows {
		var d map[string]any
		json.Unmarshal([]byte(r.Detail), &d)
		if r.Count != []int{1, 3}[i] || !reflect.DeepEqual(d, wantDetail[i]) || r.UserID != member.ID || !strings.HasPrefix(r.Name, "Synthetic ") {
			t.Errorf("audit row %d: count %d detail %s name %q user %d", i, r.Count, r.Detail, r.Name, r.UserID)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM axm_audit_log l WHERE action='campaign.test' AND row_to_json(l)::TEXT LIKE '%@%'`); n != 0 {
		t.Errorf("%d audit rows hold an address", n)
	}
}

// One address that may not receive a test stops the whole request and is named. Upstream
// dropped such an address silently and sent to the others.
func TestTestSendRejectsOtherAddresses(t *testing.T) {
	e := newTestSendEnv(t)
	member := e.as(e.ua)
	internal := "design@" + testSendDomain

	for name, tc := range map[string]struct {
		emails []string
		want   []string
	}{
		"an outside address":             {[]string{internal, "stranger@outside.invalid"}, []string{"stranger@outside.invalid"}},
		"a subscriber of another team":   {[]string{e.mineEmail, e.foreignEmail}, []string{e.foreignEmail}},
		"a subdomain":                    {[]string{"a@sub." + testSendDomain}, []string{"a@sub." + testSendDomain}},
		"another user's address":         {[]string{"ub@example.invalid"}, []string{"ub@example.invalid"}},
		"several, in the order given":    {[]string{"x@outside.invalid", internal, "y@outside.invalid"}, []string{"x@outside.invalid", "y@outside.invalid"}},
		"only an outside address":        {[]string{"stranger@outside.invalid"}, []string{"stranger@outside.invalid"}},
		"an address with a display name": {[]string{"Name <design@" + testSendDomain + ">"}, []string{"name <design@" + testSendDomain + ">"}},
	} {
		if code, rejected := e.send(member, tc.emails, ""); code != http.StatusBadRequest || !reflect.DeepEqual(rejected, tc.want) {
			t.Errorf("%s: %d %v, want 400 %v", name, code, rejected, tc.want)
		}
	}
	// No addresses at all, and too many.
	if code, rejected := e.send(member, []string{" ", ""}, ""); code != http.StatusBadRequest || rejected != nil {
		t.Errorf("no addresses: %d %v", code, rejected)
	}
	many := []string{}
	for i := 0; i <= testMaxRecipients; i++ {
		many = append(many, fmt.Sprintf("u%d@%s", i, testSendDomain))
	}
	if code, rejected := e.send(member, many, ""); code != http.StatusBadRequest || rejected != nil {
		t.Errorf("%d addresses: %d %v", len(many), code, rejected)
	}
	// A user of the other team may not test this campaign at all.
	if code, _ := e.send(e.as(e.ub), []string{internal}, ""); code != http.StatusForbidden {
		t.Errorf("other team: %d", code)
	}

	time.Sleep(50 * time.Millisecond)
	if n := len(e.msgr.all()); n != 0 {
		t.Errorf("%d messages were sent", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM axm_audit_log WHERE action='campaign.test'`); n != 0 {
		t.Errorf("%d audit rows", n)
	}
	// A refused request does not use up the limit: thirty messages still fit.
	for i := 0; i < testRateLimit/testMaxRecipients; i++ {
		if code, _ := e.send(member, many[:testMaxRecipients], ""); code != 200 {
			t.Fatalf("request %d after the refusals: %d", i, code)
		}
	}
	e.waitSent(testRateLimit)

	// Without test domains only the own address and subscribers are left.
	e.a.cfg.TestDomains = nil
	e.now = e.now.Add(time.Hour)
	if code, rejected := e.send(member, []string{"ua@example.invalid", internal}, ""); code != http.StatusBadRequest || !reflect.DeepEqual(rejected, []string{internal}) {
		t.Errorf("no test domains: %d %v", code, rejected)
	}
	if code, _ := e.send(member, []string{"ua@example.invalid", e.mineEmail}, ""); code != 200 {
		t.Errorf("no test domains, own address and subscriber: %d", code)
	}
	e.waitSent(testRateLimit + 2)
}

// A chosen sample goes to every recipient, a subscriber's values can only be borrowed by
// someone who may open that subscriber, and the sample is on the audit log.
func TestTestSendSamples(t *testing.T) {
	e := newTestSendEnv(t)
	member := e.as(e.ua)
	internal := "design@" + testSendDomain

	// Typed-in values: also the subscriber among the recipients gets them, as a stand-in.
	if code, _ := e.send(member, []string{internal, e.mineEmail}, `{"name":"홍길동","attribs":{"company":"크레바스"}}`); code != 200 {
		t.Fatalf("custom sample: %d", code)
	}
	e.waitSent(2)
	for _, to := range []string{internal, e.mineEmail} {
		m := e.msgr.to(to)
		if len(m) != 1 || !strings.HasPrefix(string(m[0].Body), "<p>홍길동 / 크레바스 / "+to+"</p>") || m[0].Subject != "홍길동 님께" ||
			m[0].Subscriber.ID != 0 || m[0].Subscriber.UUID != dummyUUID || strings.Contains(string(m[0].Body), e.subUUID(e.mine)) {
			t.Errorf("custom sample to %s: %+v", to, m)
		}
	}

	// A subscriber's values, borrowed: they go to the address asked for and not to the subscriber.
	if code, _ := e.send(member, []string{internal}, fmt.Sprintf(`{"subscriber_id":%d}`, e.mine)); code != 200 {
		t.Fatalf("borrowed sample: %d", code)
	}
	e.waitSent(3)
	if m := e.msgr.to(internal); len(m) != 2 || !strings.HasPrefix(string(m[1].Body), "<p>김하늘 / 하늘상사 / "+internal+"</p>") || m[1].Subscriber.UUID != dummyUUID {
		t.Errorf("borrowed sample: %+v", m)
	}
	if len(e.msgr.to(e.mineEmail)) != 1 {
		t.Error("the subscriber whose values were borrowed got a message")
	}

	// Not allowed: a subscriber of another team, one that does not exist, a user who may
	// not read subscribers, and values that are not a sample.
	noSubs := e.as(e.ua)
	delete(noSubs.PermissionsMap, auth.PermSubscribersGet)
	for name, tc := range map[string]struct {
		user   auth.User
		sample string
		want   int
	}{
		"another team's subscriber": {member, fmt.Sprintf(`{"subscriber_id":%d}`, e.foreign), http.StatusForbidden},
		"no subscribers:get":        {noSubs, fmt.Sprintf(`{"subscriber_id":%d}`, e.mine), http.StatusForbidden},
		"a negative ID":             {member, `{"subscriber_id":-1}`, http.StatusBadRequest},
		"attribs that are a list":   {member, `{"attribs":[1]}`, http.StatusBadRequest},
		"a name that is too long":   {member, `{"name":"` + strings.Repeat("가", testSampleMaxName+1) + `"}`, http.StatusBadRequest},
		"attribs that are too long": {member, `{"attribs":{"a":"` + strings.Repeat("x", testSampleMaxAttribs) + `"}}`, http.StatusBadRequest},
	} {
		if code, _ := e.send(tc.user, []string{internal}, tc.sample); code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}
	// The user without subscribers:get can still test with typed-in values.
	if code, _ := e.send(noSubs, []string{internal}, `{"name":"홍길동"}`); code != 200 {
		t.Errorf("custom sample without subscribers:get: %d", code)
	}
	e.waitSent(4)

	var details []string
	if err := e.db.Select(&details, `SELECT detail::TEXT FROM axm_audit_log WHERE action='campaign.test' ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"subscribers": 1.0, "own": 0.0, "internal": 1.0, "domains": []any{testSendDomain}, "sample": "custom"},
		{"subscribers": 0.0, "own": 0.0, "internal": 1.0, "domains": []any{testSendDomain}, "sample": "subscriber", "sample_subscriber_id": float64(e.mine)},
		{"subscribers": 0.0, "own": 0.0, "internal": 1.0, "domains": []any{testSendDomain}, "sample": "custom"},
	}
	if len(details) != len(want) {
		t.Fatalf("%d audit rows, want %d: %v", len(details), len(want), details)
	}
	for i, raw := range details {
		var d map[string]any
		json.Unmarshal([]byte(raw), &d)
		if !reflect.DeepEqual(d, want[i]) {
			t.Errorf("audit row %d: %s", i, raw)
		}
		// Neither the typed-in values nor the borrowed ones are on the log.
		if strings.Contains(raw, "홍길동") || strings.Contains(raw, "김하늘") || strings.Contains(raw, "@") {
			t.Errorf("audit row %d holds sample values: %s", i, raw)
		}
	}
}

// One user sends at most testRateLimit test messages in testRateWindow. A refused request
// sends nothing and is not on the audit log.
func TestTestSendRateLimit(t *testing.T) {
	e := newTestSendEnv(t)
	member, other := e.as(e.ua), e.as(e.superAdmin)
	internal := "design@" + testSendDomain

	for i := 0; i < testRateLimit; i++ {
		if code, _ := e.send(member, []string{internal}, ""); code != 200 {
			t.Fatalf("message %d: %d", i+1, code)
		}
		e.now = e.now.Add(time.Second)
	}
	e.waitSent(testRateLimit)

	b, _ := json.Marshal(map[string]any{"name": "n", "subject": "s", "lists": []int{e.aList}, "messenger": "email",
		"content_type": "html", "body": "<p>x</p>", "subscribers": []string{internal}})
	req := httptest.NewRequest("POST", "/x", strings.NewReader(string(b)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec, he := e.do(e.a.TestCampaign, member, req, e.camp, nil)
	if he == nil || he.Code != http.StatusTooManyRequests {
		t.Fatalf("message %d: %v", testRateLimit+1, he)
	}
	// The first message was sent 30 s ago: it leaves the window in 9 min 30 s.
	if got := rec.Header().Get("Retry-After"); got != "570" {
		t.Errorf("Retry-After %q, want 570", got)
	}
	// Another user is not held back, and the first one is let through when the window moved.
	if code, _ := e.send(other, []string{internal}, ""); code != 200 {
		t.Errorf("another user: %d", code)
	}
	e.now = e.now.Add(testRateWindow - 30*time.Second)
	if code, _ := e.send(member, []string{internal}, ""); code != 200 {
		t.Errorf("after the window: %d", code)
	}
	e.waitSent(testRateLimit + 2)
	if n := e.count(`SELECT COUNT(*) FROM axm_audit_log WHERE action='campaign.test'`); n != testRateLimit+2 {
		t.Errorf("%d audit rows, want %d", n, testRateLimit+2)
	}
}

// The preview renders with the sample of the test box, under the same rules.
func TestPreviewWithSample(t *testing.T) {
	e := newTestSendEnv(t)
	member := e.as(e.ua)
	preview := func(user auth.User, sample *string) (int, string) {
		form := url.Values{"content_type": {"html"}, "body": {testSendBody}}
		if sample != nil {
			form.Set("sample", *sample)
		}
		req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		rec, he := e.do(e.a.PreviewCampaign, user, req, e.camp, nil)
		if he != nil {
			return he.Code, ""
		}
		return rec.Code, rec.Body.String()
	}
	s := func(v string) *string { return &v }
	demo := "<p>Demo Subscriber /  / demo@listmonk.app</p>"

	for name, tc := range map[string]struct {
		sample *string
		want   string
	}{
		"no sample":          {nil, demo},
		"an empty sample":    {s(""), demo},
		"the default sample": {s("{}"), demo},
		"typed-in values":    {s(`{"name":"홍길동","attribs":{"company":"크레바스"}}`), "<p>홍길동 / 크레바스 / demo@listmonk.app</p>"},
		// Typed in exactly: no name is no name, as in the message that is sent.
		"typed-in attribs only": {s(`{"attribs":{"company":"크레바스"}}`), "<p> / 크레바스 / demo@listmonk.app</p>"},
		"typed in, all empty":   {s(`{"name":"","attribs":{}}`), "<p> /  / demo@listmonk.app</p>"},
		"a borrowed subscriber": {s(fmt.Sprintf(`{"subscriber_id":%d}`, e.mine)), "<p>김하늘 / 하늘상사 / demo@listmonk.app</p>"},
	} {
		code, body := preview(member, tc.sample)
		if code != 200 || !strings.HasPrefix(body, tc.want) {
			t.Errorf("%s: %d %q, want %q", name, code, body, tc.want)
		}
		// A preview never carries a real UUID: neither the campaign's nor a subscriber's.
		if strings.Contains(body, e.campUUID) || strings.Contains(body, e.subUUID(e.mine)) || strings.Count(body, dummyUUID) != 6 {
			t.Errorf("%s: UUIDs in %q", name, body)
		}
	}
	noSubs := e.as(e.ua)
	delete(noSubs.PermissionsMap, auth.PermSubscribersGet)
	for name, tc := range map[string]struct {
		user   auth.User
		sample string
		want   int
	}{
		"another team's subscriber": {member, fmt.Sprintf(`{"subscriber_id":%d}`, e.foreign), http.StatusForbidden},
		"no subscribers:get":        {noSubs, fmt.Sprintf(`{"subscriber_id":%d}`, e.mine), http.StatusForbidden},
		"not JSON":                  {member, `{name}`, http.StatusBadRequest},
		"attribs that are a text":   {member, `{"attribs":"x"}`, http.StatusBadRequest},
	} {
		if code, _ := preview(tc.user, &tc.sample); code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}
	// A preview is neither a send nor on the audit log.
	if n := len(e.msgr.all()) + e.count(`SELECT COUNT(*) FROM axm_audit_log`); n != 0 {
		t.Errorf("%d messages and audit rows", n)
	}
}

var reTestSendLink = regexp.MustCompile(`/link/([0-9a-f-]{36})/([0-9a-f-]{36})/([0-9a-f-]{36})`)

// The links and the pixel of a sample test message record nothing: no click, no view, no
// send record. A real subscriber's message is counted as before, also with individual
// tracking off, when it carries the same dummy subscriber UUID as a sample does. The links
// are taken from messages made under the setting that is tested.
func TestSampleMessageIsNotTracked(t *testing.T) {
	for _, individual := range []bool{true, false} {
		name := fmt.Sprintf("individual tracking %v", individual)
		e := newTestSendEnvTracking(t, individual)
		member := e.as(e.ua)
		internal := "design@" + testSendDomain
		if code, _ := e.send(member, []string{internal, e.mineEmail}, ""); code != 200 {
			t.Fatal(code)
		}
		e.waitSent(2)
		link := func(email string) (linkUUID, campUUID, subUUID string) {
			m := reTestSendLink.FindStringSubmatch(string(e.msgr.to(email)[0].Body))
			if m == nil {
				t.Fatalf("%s: no tracked link in the message to %s", name, email)
			}
			return m[1], m[2], m[3]
		}
		linkUUID, sampleCamp, sampleSub := link(internal)
		_, realCamp, realSub := link(e.mineEmail)
		wantRealSub := e.subUUID(e.mine)
		if !individual {
			// What the manager writes for every recipient of a real campaign too.
			wantRealSub = dummyUUID
		}
		if sampleCamp != dummyUUID || sampleSub != dummyUUID || realCamp != e.campUUID || realSub != wantRealSub {
			t.Fatalf("%s: UUIDs in the links: sample %s/%s real %s/%s", name, sampleCamp, sampleSub, realCamp, realSub)
		}

		click := func(camp, sub string) int {
			rec, he := e.do(e.a.LinkRedirect, auth.User{}, httptest.NewRequest("GET", "/x", nil), 0,
				map[string]string{"linkUUID": linkUUID, "campUUID": camp, "subUUID": sub})
			if he != nil {
				t.Fatalf("%s: link: %v", name, he)
			}
			if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "https://example.invalid/offer" {
				t.Fatalf("%s: link: %d %q", name, rec.Code, rec.Header().Get("Location"))
			}
			return e.count(`SELECT COUNT(*) FROM link_clicks`)
		}
		view := func(camp, sub string) int {
			rec, he := e.do(e.a.RegisterCampaignView, auth.User{}, httptest.NewRequest("GET", "/x", nil), 0,
				map[string]string{"campUUID": camp, "subUUID": sub})
			if he != nil || rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("%s: pixel: %v %d", name, he, rec.Code)
			}
			return e.count(`SELECT COUNT(*) FROM campaign_views`)
		}

		// The sample message.
		if n := click(sampleCamp, sampleSub); n != 0 {
			t.Errorf("%s: a click on the sample message made %d click rows", name, n)
		}
		if n := view(sampleCamp, sampleSub); n != 0 {
			t.Errorf("%s: the pixel of the sample message made %d view rows", name, n)
		}
		// The real subscriber's message: one click and one view on the campaign.
		if n := click(realCamp, realSub); n != 1 {
			t.Errorf("%s: a real click made %d click rows", name, n)
		}
		if n := view(realCamp, realSub); n != 1 {
			t.Errorf("%s: a real view made %d view rows", name, n)
		}
		if n := e.count(`SELECT COUNT(*) FROM link_clicks WHERE campaign_id=$1`, e.camp) + e.count(`SELECT COUNT(*) FROM campaign_views WHERE campaign_id=$1`, e.camp); n != 2 {
			t.Errorf("%s: %d clicks and views on the campaign, want 2", name, n)
		}
		want := 0
		if individual {
			want = 1
		}
		if n := e.count(`SELECT COUNT(*) FROM link_clicks WHERE subscriber_id=$1`, e.mine); n != want {
			t.Errorf("%s: %d clicks with the subscriber, want %d", name, n, want)
		}
		if n := e.count(`SELECT COUNT(*) FROM axm_campaign_sends`); n != 0 {
			t.Errorf("%s: %d send records", name, n)
		}

		// With tracking off entirely the link still resolves and nothing is added.
		e.a.cfg.Privacy.DisableTracking = true
		if n := click(realCamp, realSub); n != 1 {
			t.Errorf("%s, tracking disabled: %d click rows", name, n)
		}
	}
}

var reTestAddrLink = regexp.MustCompile(`/test-address/verify/([0-9A-Za-z]{32})`)

// A profile address is no reason to send it a test: a password account types it in freely
// and an identity provider may pass on an address nobody checked. The first test to it is
// refused and mails that address a link; once the mailbox has confirmed through the link,
// the address counts. This works for every kind of account and with every sender setting
// (the test environment has no sender domains and, below, no test domains: a new install).
func TestTestSendOwnAddressNeedsAConfirmedMailbox(t *testing.T) {
	e := newTestSendEnv(t)
	e.a.cfg.TestDomains = nil
	e.db.MustExec(`DELETE FROM axm_test_addresses`)
	e.db.MustExec(`UPDATE users SET password_login=true, password=CRYPT('synthetic-password', GEN_SALT('bf')), email='me@outside.invalid' WHERE id=$1`, e.ua)
	own := "me@outside.invalid"
	// send returns the status, the rejected addresses and whether the answer says that the
	// own address awaits confirmation.
	send := func(to ...string) (int, []string, bool) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"name": "n", "subject": "s", "lists": []int{e.aList}, "messenger": "email",
			"content_type": "html", "body": testSendBody, "subscribers": to})
		req := httptest.NewRequest("POST", "/x", strings.NewReader(string(b)))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		_, he := e.do(e.a.TestCampaign, e.as(e.ua), req, e.camp, nil)
		if he == nil {
			return 200, nil, false
		}
		m, _ := he.Message.(map[string]any)
		data, _ := m["data"].(map[string]any)
		rejected, _ := data["rejected"].([]string)
		pending, _ := data["own_unverified"].(bool)
		return he.Code, rejected, pending
	}
	verify := func(method, token string, form url.Values) int {
		req := httptest.NewRequest(method, "/test-address/verify/"+token, strings.NewReader(form.Encode()))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("token")
		c.SetParamValues(token)
		// The handler renders a page; only its status matters here.
		c.Echo().Renderer = nopRenderer{}
		if err := e.a.TestAddressVerify(c); err != nil {
			t.Fatal(err)
		}
		return rec.Code
	}

	// Not confirmed: refused, and the mailbox gets the link. Nothing else is sent.
	code, rejected, pending := send(own, e.mineEmail)
	if code != 400 || !reflect.DeepEqual(rejected, []string{own}) || !pending {
		t.Fatalf("first test to the own address: %d %v pending %v", code, rejected, pending)
	}
	mails := e.sys.to(own)
	if len(mails) != 1 || len(e.msgr.all()) != 0 {
		t.Fatalf("%d confirmation mails, %d test messages", len(mails), len(e.msgr.all()))
	}
	m := reTestAddrLink.FindStringSubmatch(string(mails[0].Body))
	if m == nil || !strings.Contains(string(mails[0].Body), "https://lists.example.invalid/test-address/verify/") {
		t.Fatalf("no link in the confirmation mail: %s", mails[0].Body)
	}
	token := m[1]
	// A mail that cannot be sent is an error, and does not make the next try wait.
	e.sys.fail = true
	e.db.MustExec(`UPDATE axm_test_addresses SET last_sent_at = NOW() - INTERVAL '2 minutes'`)
	if code, _, _ := send(own); code != 500 {
		t.Errorf("mail server down: %d", code)
	}
	e.sys.fail = false
	if code, _, pending := send(own); code != 400 || !pending || len(e.sys.to(own)) != 2 {
		t.Errorf("after the mail server came back: %d pending %v, %d mails", code, pending, len(e.sys.to(own)))
	}
	if m2 := reTestAddrLink.FindStringSubmatch(string(e.sys.to(own)[1].Body)); m2 == nil || m2[1] == token {
		t.Errorf("the new mail has no new link")
	} else {
		token = m2[1]
	}
	// Asking again within a minute sends no second mail; it is still refused.
	if code, _, pending := send(own); code != 400 || !pending || len(e.sys.to(own)) != 2 {
		t.Errorf("second test within a minute: %d pending %v, %d mails", code, pending, len(e.sys.to(own)))
	}
	if got := e.a.ownTestAddress(e.as(e.ua)); got != "" {
		t.Errorf("own address before confirmation: %q", got)
	}
	// Opening the link confirms nothing; a wrong token and a POST without the form value neither.
	if code := verify("GET", token, nil); code != 200 {
		t.Errorf("opening the link: %d", code)
	}
	if code := verify("POST", token, url.Values{}); code != 400 {
		t.Errorf("POST without confirm: %d", code)
	}
	if code := verify("POST", strings.Repeat("x", 32), url.Values{"confirm": {"yes"}}); code != 400 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _, _ := send(own); code != 400 {
		t.Errorf("after only opening the link: %d", code)
	}
	// The mailbox confirms: the address counts, and the link is used up.
	if code := verify("POST", token, url.Values{"confirm": {"yes"}}); code != 200 {
		t.Fatalf("confirming: %d", code)
	}
	if code := verify("POST", token, url.Values{"confirm": {"yes"}}); code != 400 {
		t.Errorf("the link worked twice: %d", code)
	}
	if code, rejected, _ := send(own, e.mineEmail); code != 200 || rejected != nil {
		t.Fatalf("after confirmation: %d %v", code, rejected)
	}
	e.waitSent(2)
	if got := e.a.ownTestAddress(e.as(e.ua)); got != own {
		t.Errorf("own address after confirmation: %q", got)
	}

	// Another address typed into the profile is not confirmed, and the old one is no longer
	// the user's own. The confirmation of one user says nothing about another user.
	e.db.MustExec(`UPDATE users SET email='victim@outside.invalid' WHERE id=$1`, e.ua)
	e.db.MustExec(`UPDATE axm_test_addresses SET last_sent_at = NOW() - INTERVAL '2 minutes'`)
	if code, rejected, pending := send("victim@outside.invalid"); code != 400 || !pending || !reflect.DeepEqual(rejected, []string{"victim@outside.invalid"}) {
		t.Errorf("a new profile address: %d %v pending %v", code, rejected, pending)
	}
	if code, _, pending := send(own); code != 400 || pending {
		t.Errorf("the former own address: %d pending %v", code, pending)
	}
	if len(e.sys.to("victim@outside.invalid")) != 1 {
		t.Errorf("%d confirmation mails to the new address", len(e.sys.to("victim@outside.invalid")))
	}
	e.confirmOwn(e.ub, "victim@outside.invalid")
	if code, _, _ := send("victim@outside.invalid"); code != 400 {
		t.Errorf("confirmed for another user only: %d", code)
	}

	// An account without a password (as the identity provider makes them): the same rule.
	e.db.MustExec(`UPDATE users SET password_login=false, password=NULL, email='sso@outside.invalid' WHERE id=$1`, e.ua)
	if code, _, pending := send("sso@outside.invalid"); code != 400 || !pending {
		t.Errorf("unconfirmed address of an account without a password: %d pending %v", code, pending)
	}
	e.confirmOwn(e.ua, "sso@outside.invalid")
	if code, _, _ := send("SSO@outside.invalid"); code != 200 {
		t.Errorf("confirmed address of an account without a password: %d", code)
	}
	// An API user has no own address, and nothing is mailed for it.
	e.db.MustExec(`UPDATE users SET type='api' WHERE id=$1`, e.ua)
	before := len(e.sys.all())
	if code, _, pending := send("sso@outside.invalid"); code != 400 || pending || len(e.sys.all()) != before {
		t.Errorf("API user: %d pending %v", code, pending)
	}
	// A user that is deleted takes the confirmation along.
	e.db.MustExec(`DELETE FROM users WHERE id=$1`, e.ub)
	if n := e.count(`SELECT COUNT(*) FROM axm_test_addresses WHERE user_id=$1`, e.ub); n != 0 {
		t.Errorf("%d rows of a deleted user", n)
	}
	// No confirmation mail and no token is on the audit log.
	if n := e.count(`SELECT COUNT(*) FROM axm_audit_log l WHERE row_to_json(l)::TEXT LIKE '%@%'`); n != 0 {
		t.Errorf("%d audit rows hold an address", n)
	}
}

// nopRenderer stands in for the public page templates.
type nopRenderer struct{}

func (nopRenderer) Render(io.Writer, string, interface{}, echo.Context) error { return nil }

// The login name of an account made by the identity provider is its e-mail address. A test
// to that address must not put it on the audit log through the user columns.
func TestTestSendAuditHidesEmailLoginName(t *testing.T) {
	e := newTestSendEnv(t)
	e.db.MustExec(`UPDATE users SET username='ua@example.invalid', name='ua@example.invalid' WHERE id=$1`, e.ua)
	e.db.MustExec(`UPDATE campaigns SET name='To ua@example.invalid' WHERE id=$1`, e.camp)
	user := e.as(e.ua)
	if code, _ := e.send(user, []string{"ua@example.invalid"}, ""); code != 200 {
		t.Fatal(code)
	}
	e.waitSent(1)
	var row struct {
		JSON   string `db:"j"`
		UserID int    `db:"user_id"`
	}
	e.get(&row, `SELECT row_to_json(l)::TEXT AS j, user_id FROM axm_audit_log l WHERE action='campaign.test'`)
	if strings.Contains(row.JSON, "@") || strings.Contains(row.JSON, "ua") || row.UserID != user.ID {
		t.Errorf("audit row: %s", row.JSON)
	}
	if !strings.Contains(row.JSON, `"domains": ["example.invalid"]`) && !strings.Contains(row.JSON, `"domains":["example.invalid"]`) {
		t.Errorf("the domain is missing: %s", row.JSON)
	}
}

// A message that could not be rendered was not sent and does not count towards the limit.
func TestTestSendFailedMessagesDoNotUseTheLimit(t *testing.T) {
	e := newTestSendEnv(t)
	member := e.as(e.ua)
	many := []string{}
	for i := 0; i < testMaxRecipients; i++ {
		many = append(many, fmt.Sprintf("u%d@%s", i, testSendDomain))
	}
	// The default sample has no tags, so this body fails for the first recipient.
	e.body = `<p>{{ index .Subscriber.Attribs.tags 0 }}</p>`
	for i := 0; i < 5; i++ {
		if code, _ := e.send(member, many, ""); code != http.StatusInternalServerError {
			t.Fatalf("request %d with a body that cannot be rendered: %d", i, code)
		}
	}
	// It renders for a subscriber with such a value and fails for the sample after it: the
	// one message that went out counts, and is on the audit log.
	e.db.MustExec(`UPDATE subscribers SET attribs='{"tags":["a"]}' WHERE id=$1`, e.mine)
	if code, _ := e.send(member, []string{e.mineEmail, many[0], many[1]}, ""); code != http.StatusInternalServerError {
		t.Fatalf("a request that fails at the second recipient: %d", code)
	}
	e.waitSent(1)
	if n := e.count(`SELECT COALESCE(SUM(count), 0) FROM axm_audit_log WHERE action='campaign.test'`); n != 1 {
		t.Errorf("audit count %d, want 1", n)
	}
	// 29 more fit, the 30th after them does not.
	e.body = testSendBody
	for i := 0; i < 29; i++ {
		if code, _ := e.send(member, many[:1], ""); code != 200 {
			t.Fatalf("message %d after the failures: %d", i+2, code)
		}
	}
	if code, _ := e.send(member, many[:1], ""); code != http.StatusTooManyRequests {
		t.Fatalf("message 31: %d", code)
	}
	e.waitSent(30)
}

// The migration gives a database the test domains setting as a copy of its sender domains,
// once: a later run leaves a changed value alone.
func TestTestDomainsMigration(t *testing.T) {
	e := newSendsEnv(t, 0)
	value := func() string {
		var v string
		e.get(&v, `SELECT value::TEXT FROM settings WHERE key='axm.test_domains'`)
		return v
	}
	// A new install has the key, empty.
	if v := value(); v != "[]" {
		t.Fatalf("new install: %s", v)
	}
	run := func() {
		t.Helper()
		if err := migrations.V6_2_1_axm_14(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A database from before: no key, sender domains set.
	e.db.MustExec(`DELETE FROM settings WHERE key='axm.test_domains'`)
	e.db.MustExec(`UPDATE settings SET value='["corp.example.invalid", "other.example.invalid"]' WHERE key='axm.sender_domains'`)
	run()
	run()
	if v := value(); v != `["corp.example.invalid", "other.example.invalid"]` {
		t.Fatalf("copied: %s", v)
	}
	// Changed afterwards (here: emptied), then migrated again by a later release.
	e.db.MustExec(`UPDATE settings SET value='[]' WHERE key='axm.test_domains'`)
	run()
	if v := value(); v != "[]" {
		t.Fatalf("a changed value was overwritten: %s", v)
	}
	// A database that never had sender domains (official v6.2.0).
	e.db.MustExec(`DELETE FROM settings WHERE key IN ('axm.test_domains', 'axm.sender_domains')`)
	run()
	if v := value(); v != "[]" {
		t.Fatalf("without sender domains: %s", v)
	}
	// The table of confirmed own addresses: the same SQL in schema.sql and in the migration,
	// made again by the migration when it is missing, and reset by a new install.
	root := repoRoot(t)
	schema, _ := os.ReadFile(filepath.Join(root, "schema.sql"))
	mig, _ := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.14.go"))
	const start, end = "-- AXM test addresses (LST-41). Keep this block identical to V6_2_1_axm_14.", "-- End of AXM test addresses."
	_, tail, ok := strings.Cut(string(schema), start)
	block, _, endOK := strings.Cut(tail, end)
	if !ok || !endOK || !strings.Contains(string(mig), "`"+start+block+end+"`") {
		t.Fatal("schema and migration SQL of axm_test_addresses differ")
	}
	e.db.MustExec(`DROP TABLE axm_test_addresses`)
	run()
	run()
	var uid int
	e.db.MustExec(`INSERT INTO roles(id,type,name) VALUES(1,'user','Super Admin')`)
	e.get(&uid, `INSERT INTO users(username,email,name,status,type,user_role_id) VALUES('m','m@example.invalid','m','enabled','user',1) RETURNING id`)
	e.db.MustExec(`INSERT INTO axm_test_addresses (user_id, email, verified_at) VALUES ($1, 'm@example.invalid', NOW())`, uid)
	e.reinstall()
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_test_addresses`)
	if n != 0 {
		t.Fatal("a new install kept confirmed addresses")
	}

	// The app reads the setting as a list.
	set, err := e.core.GetSettings()
	if err != nil || set.AxmTestDomains == nil || len(set.AxmTestDomains) != 0 {
		t.Fatalf("settings: %v %v", set.AxmTestDomains, err)
	}
}
