package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// LST-56 step 2: what the gate lets in. The tests use a fake resolver, never real DNS:
// ok.test and mailinator.com have a mail server, aonly.test an address record only, null.test
// a null MX, slow.test does not answer, anything else does not exist.

type gateEnv struct {
	*boundaryEnv
	dns *addrFakeDNS
}

func newGateEnv(t *testing.T) *gateEnv {
	t.Helper()
	e := &gateEnv{boundaryEnv: newRulesEnv(t), dns: &addrFakeDNS{}}
	e.a.addrCheck = addrcheck.NewChecker(addrcheck.Options{Resolver: e.dns})
	e.a.importer.SetAxmVerify(e.a.importVerify)
	// A core that knows the bounce actions: one hard bounce deletes the subscriber, a
	// complaint blocklists, three soft bounces do nothing yet.
	type act = struct {
		Count  int
		Action string
	}
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]act{"hard": {1, "delete"}, "soft": {3, "none"}, "complaint": {1, "blocklist"}},
	}}, nil)
	return e
}

type sent struct {
	code int
	msg  string
	rule string
	warn string
}

// do runs a subscriber handler as the super admin and keeps the address headers.
func (e *gateEnv) do(h echo.HandlerFunc, method string, id int, body string) sent {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/subscribers", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if id != 0 {
		c.Set("id", id)
	}
	out := sent{}
	if err := h(c); err != nil {
		he, ok := err.(*echo.HTTPError)
		if !ok {
			e.t.Fatal(err)
		}
		out.code = he.Code
		out.msg, _ = he.Message.(string)
	} else {
		out.code = rec.Code
	}
	out.rule = rec.Header().Get("X-Axm-Address-Rule")
	out.warn = rec.Header().Get("X-Axm-Address-Warn")
	return out
}

func (e *gateEnv) add(email string, status ...string) sent {
	st := "enabled"
	if len(status) > 0 {
		st = status[0]
	}
	return e.do(e.a.CreateSubscriber, "POST", 0, `{"email":"`+email+`","name":"Synthetic","status":"`+st+`","lists":[`+itoa(e.aList)+`]}`)
}

func (e *gateEnv) bounce(email, typ string) {
	e.t.Helper()
	if err := e.a.core.RecordBounce(models.Bounce{Email: email, Type: typ, Source: "test", Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *gateEnv) bouncedRows() int {
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_bounced_emails`)
	return n
}

func (e *gateEnv) isBounced(email string) bool {
	var b bool
	e.get(&b, `SELECT EXISTS (SELECT 1 FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1))`, email)
	return b
}

func TestGateSingleAddReasons(t *testing.T) {
	e := newGateEnv(t)
	for _, c := range []struct {
		email, rule, in string
	}{
		{"kim@nowhere.test", "dead", "does not exist"},
		{"kim@null.test", "nullmx", "accepts no mail"},
		{"kim@mailinator.com", "disposable", "disposable"},
		{"kim@gmail.con", "typo", "Did you mean"},
	} {
		r := e.add(c.email)
		if r.code != http.StatusBadRequest || r.rule != c.rule || !strings.Contains(r.msg, c.in) {
			t.Errorf("%s: %+v, want 400 %s", c.email, r, c.rule)
		}
		if e.count(c.email) != 0 {
			t.Errorf("%s was stored", c.email)
		}
	}
	for _, c := range []struct{ email, warn string }{
		{"kim@ok.test", ""},
		{"kim@aonly.test", ""},       // an address record is enough
		{"info@ok.test", "role"},     // a role address comes in, with a warning
		{"kim@slow.test", "unknown"}, // DNS did not answer: in, with a warning
		{"info@slow.test", "unknown"},
	} {
		r := e.add(c.email)
		if r.code != http.StatusOK || r.warn != c.warn || r.rule != "" {
			t.Errorf("%s: %+v, want 200 with warn %q", c.email, r, c.warn)
		}
		if e.count(c.email) != 1 {
			t.Errorf("%s was not stored", c.email)
		}
	}
}

// An address that is added as blocklisted may be anything.
func TestGateBlocklistedAddTakesAnyAddress(t *testing.T) {
	e := newGateEnv(t)
	e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('known@nowhere.test'))`)
	if r := e.add("known@nowhere.test"); r.code != http.StatusBadRequest || r.rule != "suppressed" {
		t.Fatalf("%+v", r)
	}
	for _, email := range []string{"known@nowhere.test", "kim@mailinator.com", "x@nowhere.test"} {
		if r := e.add(email, "blocklisted"); r.code != http.StatusOK {
			t.Errorf("%s as blocklisted: %+v", email, r)
		}
	}
	// The DNS was not asked for a blocklisted address.
	if e.dns.calls != 0 {
		t.Fatalf("%d DNS lookups", e.dns.calls)
	}
}

// The blocklist and the bounce record come before DNS: nothing is waited for.
func TestGateDatabaseFactsBeforeDNS(t *testing.T) {
	e := newGateEnv(t)
	e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('known@nowhere.test'))`)
	e.db.MustExec(`INSERT INTO axm_bounced_emails (email_hash, kind) VALUES (axm_email_hash('Bounced@nowhere.test'), 'hard')`)
	if r := e.add("Known@nowhere.test"); r.code != 400 || r.rule != "suppressed" || !strings.Contains(r.msg, "blocklisted before") {
		t.Fatalf("%+v", r)
	}
	if r := e.add("bounced@nowhere.test"); r.code != 400 || r.rule != "bounced" || !strings.Contains(r.msg, "bounced for good") {
		t.Fatalf("%+v", r)
	}
	if e.dns.calls != 0 {
		t.Fatalf("%d DNS lookups", e.dns.calls)
	}
	// A deleted blocklisted subscriber stays refused (the hash outlives the row).
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'was@ok.test', 'W', 'blocklisted') RETURNING id`)
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, id)
	if r := e.add("was@ok.test"); r.code != 400 || r.rule != "suppressed" {
		t.Fatalf("%+v", r)
	}
}

func TestGateBouncedAddressIsKept(t *testing.T) {
	e := newGateEnv(t)
	person := func(email string) int {
		var id int
		e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), $1, 'P', 'enabled') RETURNING id`, email)
		e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, id, e.aList)
		return id
	}
	person("hard@ok.test")
	person("soft@ok.test")
	person("comp@ok.test")

	// A hard bounce deletes the subscriber (the action of this test), and the address is still refused.
	e.bounce("hard@ok.test", "hard")
	if e.count("hard@ok.test") != 0 {
		t.Fatal("the subscriber should be deleted by the bounce action")
	}
	if r := e.add("hard@ok.test"); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("a deleted hard bounce: %+v", r)
	}
	// A soft bounce is not a reason.
	e.bounce("soft@ok.test", "soft")
	if e.isBounced("soft@ok.test") {
		t.Fatal("a soft bounce was remembered")
	}
	// A complaint is.
	e.bounce("comp@ok.test", "complaint")
	if !e.isBounced("comp@ok.test") {
		t.Fatal("a complaint was not remembered")
	}
	// Another address edited to a bounced one is refused; an unchanged address is not looked at.
	other := person("other@ok.test")
	if r := e.do(e.a.UpdateSubscriber, "PUT", other, `{"email":"hard@ok.test","name":"P","status":"enabled","lists":[`+itoa(e.aList)+`]}`); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("put to a bounced address: %+v", r)
	}
	if r := e.do(e.a.PatchSubscriber, "PATCH", other, `{"email":"hard@ok.test"}`); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("patch to a bounced address: %+v", r)
	}
	// The address API shows the same classes to the column mapping.
	req := httptest.NewRequest("POST", "/api/subscribers/address-check", strings.NewReader(`{"emails":["hard@ok.test","fine@ok.test","info@ok.test"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if err := e.a.CheckImportAddresses(c); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Data struct {
			Issues    map[string][]string `json:"issues"`
			Confirmed int                 `json:"confirmed"`
			Warning   int                 `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resp.Data.Issues["0"], ","); got != "bounced" {
		t.Fatalf("issues of the bounced address: %v", resp.Data.Issues)
	}
	if got := strings.Join(resp.Data.Issues["2"], ","); got != "role" || len(resp.Data.Issues["1"]) != 0 {
		t.Fatalf("issues: %v", resp.Data.Issues)
	}
	// The numbers of the summary are not changed by the new classes.
	if resp.Data.Confirmed != 0 || resp.Data.Warning != 1 {
		t.Fatalf("summary %+v", resp.Data)
	}
}

// A person who deletes the bounce records of a subscriber releases the address; one bounce
// less of two does not; deleting the subscriber (the bounce rows go with it) does not.
func TestGateBouncedRelease(t *testing.T) {
	e := newGateEnv(t)
	person := func(email string) int {
		var id int
		e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), $1, 'P', 'enabled') RETURNING id`, email)
		return id
	}
	// No action on a bounce for this test: the subscriber stays.
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]struct {
			Count  int
			Action string
		}{"hard": {100, "none"}, "soft": {100, "none"}, "complaint": {100, "none"}},
	}}, nil)
	bounceIDs := func(id int) []int {
		var ids []int
		if err := e.db.Select(&ids, `SELECT id FROM bounces WHERE subscriber_id = $1 ORDER BY id`, id); err != nil {
			t.Fatal(err)
		}
		return ids
	}

	a := person("rel@ok.test")
	e.bounce("rel@ok.test", "hard")
	e.bounce("rel@ok.test", "hard")
	e.bounce("rel@ok.test", "soft")
	ids := bounceIDs(a)
	if len(ids) != 3 || !e.isBounced("rel@ok.test") {
		t.Fatalf("ids %v bounced %v", ids, e.isBounced("rel@ok.test"))
	}
	// One of the two hard bounces deleted: the other remains, so does the address.
	if err := e.a.core.DeleteBounces([]int{ids[0]}, false); err != nil {
		t.Fatal(err)
	}
	if !e.isBounced("rel@ok.test") {
		t.Fatal("released with a hard bounce left")
	}
	// The second hard bounce and the soft one: only the hard ones count.
	if err := e.a.core.DeleteBounces([]int{ids[1]}, false); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("rel@ok.test") {
		t.Fatal("not released after the last hard bounce was deleted")
	}
	if r := e.add("rel@ok.test"); r.rule == "bounced" {
		t.Fatalf("still refused: %+v", r)
	}

	// The same through the call that deletes all the bounces of a subscriber.
	b := person("rel2@ok.test")
	e.bounce("rel2@ok.test", "complaint")
	if !e.isBounced("rel2@ok.test") {
		t.Fatal("complaint not remembered")
	}
	if err := e.a.core.DeleteSubscriberBounces(b, ""); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("rel2@ok.test") {
		t.Fatal("not released by DeleteSubscriberBounces")
	}

	// Deleting the subscriber deletes the bounce rows too, and the address stays refused.
	c := person("rel3@ok.test")
	e.bounce("rel3@ok.test", "hard")
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, c)
	var left int
	e.get(&left, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1`, c)
	if left != 0 || !e.isBounced("rel3@ok.test") {
		t.Fatalf("bounce rows left %d, bounced %v", left, e.isBounced("rel3@ok.test"))
	}
	// The same when the bounces are deleted in bulk (all = true) of a subscriber that exists.
	d := person("rel4@ok.test")
	e.bounce("rel4@ok.test", "hard")
	if err := e.a.core.DeleteBounces(nil, true); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("rel4@ok.test") {
		t.Fatal("not released by deleting all bounces")
	}
	_ = d
}

func (e *gateEnv) uuidOf(id int) string {
	var u string
	e.get(&u, `SELECT uuid::TEXT FROM subscribers WHERE id = $1`, id)
	return u
}

func (e *gateEnv) noActionCore() {
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]struct {
			Count  int
			Action string
		}{"hard": {100, "none"}, "soft": {100, "none"}, "complaint": {100, "none"}},
	}}, nil)
}

func (e *gateEnv) hardBounce(email, uuid string) {
	e.t.Helper()
	if err := e.a.core.RecordBounce(models.Bounce{Email: email, SubscriberUUID: uuid, Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *gateEnv) person2(email string) int {
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), $1, 'P', 'enabled') RETURNING id`, email)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, id, e.aList)
	return id
}

// sentTo records a campaign mail to the subscriber at the given time.
func (e *gateEnv) sentTo(id int, at string) {
	var camp int
	e.get(&camp, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'Synthetic','S','test@example.invalid','b','email') RETURNING id`)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, `+at+`)`, camp, id)
}

// The address a bounce is for is the one the bounce names, not the subscriber's current one: a
// subscriber edited since the mail was sent bounced at the old address. A bounce that names none
// of its address is not kept for a subscriber whose address changed lately: it may be the old one.
func TestGateBounceOfAnEditedSubscriberIsForTheOldAddress(t *testing.T) {
	e := newGateEnv(t)
	e.noActionCore()
	id := e.person2("was@ok.test")
	e.sentTo(id, "NOW() - INTERVAL '1 hour'")
	if r := e.do(e.a.PatchSubscriber, "PATCH", id, `{"email":"now@ok.test"}`); r.code != 200 {
		t.Fatalf("%+v", r)
	}
	// Address and UUID: the old address.
	e.hardBounce("was@ok.test", e.uuidOf(id))
	if !e.isBounced("was@ok.test") || e.isBounced("now@ok.test") {
		t.Fatalf("old bounced %v, new bounced %v", e.isBounced("was@ok.test"), e.isBounced("now@ok.test"))
	}
	// UUID only, and the subscriber was edited since the last mail: nothing is known of the
	// address, so nothing is kept. Not for the first 14 days only: for good.
	e.hardBounce("", e.uuidOf(id))
	if e.isBounced("now@ok.test") {
		t.Fatal("a bounce with a UUID only marked the new address")
	}
	// The address alone, which no subscriber has any more: kept all the same.
	e.hardBounce("gone@ok.test", "")
	if !e.isBounced("gone@ok.test") {
		t.Fatal("a bounce for an address that was changed was not kept")
	}
	if r := e.add("gone@ok.test"); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("%+v", r)
	}
	// A subscriber whose address did not change: the UUID is enough.
	other := e.person2("steady@ok.test")
	e.sentTo(other, "NOW() + INTERVAL '1 minute'")
	e.hardBounce("", e.uuidOf(other))
	if !e.isBounced("steady@ok.test") {
		t.Fatal("a bounce with a UUID only was not kept for an unchanged address")
	}
	// Deleting the bounce records of the edited subscriber releases the address they were for.
	if err := e.a.core.DeleteSubscriberBounces(id, ""); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("was@ok.test") || !e.isBounced("gone@ok.test") {
		t.Fatal("release of the edited subscriber's address")
	}
}

// One bounce record, one address: another subscriber's bounce for the same address, and another
// address of the same subscriber, keep their own.
func TestGateBouncedReleaseIsPerBounce(t *testing.T) {
	e := newGateEnv(t)
	e.noActionCore()
	ids := func(id int) []int {
		var out []int
		if err := e.db.Select(&out, `SELECT id FROM bounces WHERE subscriber_id = $1 ORDER BY id`, id); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Two subscribers with a hard bounce for the same address.
	a := e.person2("a@ok.test")
	e.hardBounce("shared@ok.test", e.uuidOf(a))
	b := e.person2("b@ok.test")
	e.hardBounce("shared@ok.test", e.uuidOf(b))
	if err := e.a.core.DeleteBounces(ids(b), false); err != nil {
		t.Fatal(err)
	}
	if !e.isBounced("shared@ok.test") {
		t.Fatal("the other subscriber's bounce was lost")
	}
	// One subscriber, two addresses.
	c := e.person2("first@ok.test")
	e.hardBounce("first@ok.test", e.uuidOf(c))
	e.db.MustExec(`UPDATE subscribers SET email = 'second@ok.test' WHERE id = $1`, c)
	e.hardBounce("second@ok.test", e.uuidOf(c))
	first := ids(c)[0]
	if err := e.a.core.DeleteBounces([]int{first}, false); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("first@ok.test") || !e.isBounced("second@ok.test") {
		t.Fatalf("first %v second %v", e.isBounced("first@ok.test"), e.isBounced("second@ok.test"))
	}
	if r := e.add("first@ok.test"); r.code != 200 {
		t.Fatalf("%+v", r)
	}
	// A subscriber who is already blocklisted (under another address) gets no bounce record, but the
	// address the bounce names is kept all the same.
	d := e.person2("blocked.now@ok.test")
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = $1`, d)
	e.hardBounce("blocked.old@ok.test", e.uuidOf(d))
	if !e.isBounced("blocked.old@ok.test") {
		t.Fatal("the address of a bounce for a blocklisted subscriber was not kept")
	}
	if r := e.add("blocked.old@ok.test"); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("%+v", r)
	}
}

// Failing to keep the address never loses the bounce.
func TestGateBounceSurvivesAFailureToKeepTheAddress(t *testing.T) {
	e := newGateEnv(t)
	e.noActionCore()
	id := e.person2("keep@ok.test")
	e.db.MustExec(`ALTER TABLE axm_bounced_emails ADD CONSTRAINT axm_test_refuse CHECK (email_hash <> axm_email_hash('keep@ok.test'))`)
	e.hardBounce("keep@ok.test", e.uuidOf(id))
	var n int
	e.get(&n, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1 AND type = 'hard'`, id)
	if n != 1 || e.isBounced("keep@ok.test") {
		t.Fatalf("bounce rows %d, bounced %v", n, e.isBounced("keep@ok.test"))
	}
}

// The address is remembered only when the bounce is recorded.
func TestGateBouncedAddressNeedsTheBounceRecord(t *testing.T) {
	e := newGateEnv(t)
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]struct {
			Count  int
			Action string
		}{"hard": {100, "none"}, "soft": {100, "none"}, "complaint": {100, "none"}},
	}}, nil)
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'fail@ok.test', 'P', 'enabled') RETURNING id`)
	// A campaign UUID that is not one: record-bounce fails.
	err := e.a.core.RecordBounce(models.Bounce{Email: "fail@ok.test", CampaignUUID: "not-a-uuid", Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("the bounce should have failed")
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1`, id)
	if n != 0 || e.isBounced("fail@ok.test") {
		t.Fatalf("bounce rows %d, bounced %v", n, e.isBounced("fail@ok.test"))
	}
}

// The public form is not a way to find out who is blocklisted or bounced.
func TestGatePublicFormDoesNotRevealDatabaseFacts(t *testing.T) {
	e := newGateEnv(t)
	e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('known@ok.test'))`)
	e.db.MustExec(`INSERT INTO axm_bounced_emails (email_hash, kind) VALUES (axm_email_hash('bnc@ok.test'), 'hard')`)
	pub := gateOpt{Budget: time.Second}
	for _, email := range []string{"known@ok.test", "bnc@ok.test", "fine@ok.test"} {
		if r := e.a.gateAddresses(context.Background(), []string{email}, pub)[0]; r.Reject != "" {
			t.Errorf("%s: %+v", email, r)
		}
	}
	for email, want := range map[string]string{"kim@nowhere.test": "dead", "kim@mailinator.com": "disposable", "kim@null.test": "nullmx", "a@gmail.con": "typo"} {
		if r := e.a.gateAddresses(context.Background(), []string{email}, pub)[0]; r.Reject != want {
			t.Errorf("%s: %+v, want %s", email, r, want)
		}
	}
}

// DNS that does not answer within the budget lets the address in, with the warning.
func TestGatePendingLookupIsAWarning(t *testing.T) {
	e := newGateEnv(t)
	e.dns.hold = make(chan struct{})
	res := e.a.gateAddresses(context.Background(), []string{"kim@pending.test", "kim@mailinator.com"}, gateOpt{Budget: 50 * time.Millisecond})
	close(e.dns.hold)
	if res[0].Reject != "" || res[0].Warn != "unknown" || res[1].Reject != "disposable" {
		t.Fatalf("%+v", res)
	}
}

func TestGateEditedAddressOnly(t *testing.T) {
	e := newGateEnv(t)
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'old@nowhere.test', 'Old', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, id, e.aList)
	body := func(email, name string) string {
		return `{"email":"` + email + `","name":"` + name + `","status":"enabled","lists":[` + itoa(e.aList) + `]}`
	}
	// The address is dead, but the person did not change it: no check, no lookup.
	if r := e.do(e.a.UpdateSubscriber, "PUT", id, body("old@nowhere.test", "Renamed")); r.code != 200 || r.warn != "" {
		t.Fatalf("PUT with the same address: %+v", r)
	}
	if r := e.do(e.a.PatchSubscriber, "PATCH", id, `{"name":"Again","email":"OLD@nowhere.test"}`); r.code != 200 {
		t.Fatalf("PATCH with the same address: %+v", r)
	}
	if e.dns.calls != 0 {
		t.Fatalf("%d DNS lookups for an address that did not change", e.dns.calls)
	}
	// A new address is held to the gate, in both.
	if r := e.do(e.a.UpdateSubscriber, "PUT", id, body("new@nowhere.test", "Renamed")); r.code != 400 || r.rule != "dead" {
		t.Fatalf("PUT: %+v", r)
	}
	if r := e.do(e.a.PatchSubscriber, "PATCH", id, `{"email":"new@mailinator.com"}`); r.code != 400 || r.rule != "disposable" {
		t.Fatalf("PATCH: %+v", r)
	}
	if r := e.do(e.a.PatchSubscriber, "PATCH", id, `{"email":"info@ok.test"}`); r.code != 200 || r.warn != "role" {
		t.Fatalf("PATCH to a role address: %+v", r)
	}
	if r := e.do(e.a.UpdateSubscriber, "PUT", id, body("later@slow.test", "Renamed")); r.code != 200 || r.warn != "unknown" {
		t.Fatalf("PUT to a silent domain: %+v", r)
	}
}

func TestGateStrength(t *testing.T) {
	e := newGateEnv(t)
	e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('known@ok.test'))`)

	e.a.addrGate = "nodns"
	if r := e.add("kim@nowhere.test"); r.code != 200 || r.warn != "" {
		t.Errorf("nodns, dead domain: %+v", r)
	}
	if r := e.add("kim@slow.test"); r.code != 200 || r.warn != "" {
		t.Errorf("nodns, silent domain: %+v", r)
	}
	if r := e.add("kim@mailinator.com"); r.code != 400 || r.rule != "disposable" {
		t.Errorf("nodns, disposable: %+v", r)
	}
	if r := e.add("known@ok.test"); r.code != 400 || r.rule != "suppressed" {
		t.Errorf("nodns, suppressed: %+v", r)
	}
	// Only a domain on the list itself is refused, not a subdomain of one; the review agrees.
	if r := e.add("kim@unlisted-child.mailinator.com"); r.code != 200 || r.warn != "" {
		t.Errorf("nodns, subdomain of a disposable domain: %+v", r)
	}
	if issues := e.reviewIssues("kim@unlisted-child.mailinator.com", "kim2@mailinator.com"); len(issues["0"]) != 0 || strings.Join(issues["1"], ",") != "disposable" {
		t.Errorf("review issues %v", issues)
	}
	if r := e.add("info@ok.test"); r.code != 200 || r.warn != "role" {
		t.Errorf("nodns, role: %+v", r)
	}
	if e.dns.calls != 0 {
		t.Errorf("%d DNS lookups with nodns", e.dns.calls)
	}

	e.a.addrGate = "basic"
	for _, email := range []string{"kim@mailinator.com", "known@ok.test", "info2@ok.test"} {
		if r := e.add(email); r.code != 200 || r.warn != "" {
			t.Errorf("basic, %s: %+v", email, r)
		}
	}
	if r := e.add("kim@gmail.con"); r.code != 400 || r.rule != "typo" {
		t.Errorf("basic, typo: %+v", r)
	}

	// An unknown value is the full gate.
	e.a.addrGate = "strict-ish"
	if r := e.add("kim2@nowhere.test"); r.code != 400 || r.rule != "dead" {
		t.Errorf("unknown value: %+v", r)
	}
}

func TestGateImport(t *testing.T) {
	e := newGateEnv(t)
	e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('sup@nowhere.test'))`)
	e.db.MustExec(`INSERT INTO axm_bounced_emails (email_hash, kind) VALUES (axm_email_hash('bnc@ok.test'), 'hard')`)
	csvIn := "email,name\n" + // row 1
		"good@ok.test,Good\n" + // 2 in
		"info@ok.test,Role\n" + // 3 in, role warning
		"slow@slow.test,Slow\n" + // 4 in, unknown warning
		"dead@nowhere.test,Dead\n" + // 5 dead
		"null@null.test,Null\n" + // 6 nullmx
		"x@mailinator.com,Disp\n" + // 7 disposable
		"sup@nowhere.test,Sup\n" + // 8 suppressed (before DNS)
		"bnc@ok.test,Bounced\n" + // 9 bounced
		"kimlee@gmail.con,Typo\n" + // 10 typo
		"nothing,Broken\n" // 11 format
	params := `{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[` + itoa(e.aList) + `],"axm_excluded":[{"row":50,"email":"u@z.test","reason":"disposable"},{"row":51,"email":"b@z.test","reason":"bounced"},{"row":52,"email":"s@z.test","reason":"suppressed"}]}`
	if code := e.importAs(e.as(e.superAdmin), params, csvIn); code != 200 {
		t.Fatal(code)
	}
	st := e.lastImport
	if st.Imported != 3 || st.AxmRejectedTotal != 10 || st.AxmExcluded != 3 {
		t.Fatalf("stats %+v", st)
	}
	want := map[string]int{"dead": 1, "nullmx": 1, "disposable": 2, "suppressed": 2, "bounced": 2, "typo": 1, "format": 1}
	for k, v := range want {
		if st.AxmRejected[k] != v {
			t.Errorf("rejected[%s] = %d, want %d (%v)", k, st.AxmRejected[k], v, st.AxmRejected)
		}
	}
	if st.AxmWarnedTotal != 2 || st.AxmWarned["role"] != 1 || st.AxmWarned["unknown"] != 1 {
		t.Fatalf("warned %+v", st.AxmWarned)
	}
	for _, in := range []string{"good@ok.test", "info@ok.test", "slow@slow.test"} {
		if e.count(in) != 1 {
			t.Errorf("%s was not imported", in)
		}
	}
	for _, out := range []string{"dead@nowhere.test", "null@null.test", "x@mailinator.com", "sup@nowhere.test", "bnc@ok.test"} {
		if e.count(out) != 0 {
			t.Errorf("%s was imported", out)
		}
	}
	// The CSV of the refused rows has the file rows, in the words of the reasons.
	req := httptest.NewRequest("GET", "/api/import/subscribers/rejected?id="+itoa(st.AxmImportID), nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if err := e.a.GetImportRejected(c); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows[1:] {
		got[r[0]] = r[2]
	}
	for row, reason := range map[string]string{"5": "No domain or mail server", "6": "Domain accepts no mail", "7": "Disposable address",
		"8": "Blocklisted before", "9": "Bounced before", "10": "Misspelled domain", "11": "Malformed address", "50": "Disposable address"} {
		if got[row] != reason {
			t.Errorf("row %s: %q, want %q", row, got[row], reason)
		}
	}
	// The log has the reason codes and no addresses.
	if log := string(e.a.importer.GetLogs()); strings.Contains(log, "nowhere.test") || strings.Contains(log, "sup@") {
		t.Errorf("the import log names an address:\n%s", log)
	}

	// Blocklisting takes any address, whatever the gate knows of it.
	e.a.importer.Stop()
	time.Sleep(20 * time.Millisecond)
	blk := `{"mode":"blocklist","delim":",","lists":[]}`
	if code := e.importAs(e.as(e.superAdmin), blk, "email,name\ndead@nowhere.test,X\nx@mailinator.com,Y\nsup@nowhere.test,Z\n"); code != 200 {
		t.Fatal(code)
	}
	if st := e.lastImport; st.Imported != 3 || st.AxmRejectedTotal != 0 || st.AxmWarnedTotal != 0 {
		t.Fatalf("blocklist import %+v", st)
	}
}

// The migration can run twice. It takes in the bounces whose payload names the address (the
// webhooks'), and not the others: those do not say which address they were for.
func TestGateMigrationTwice(t *testing.T) {
	e := newGateEnv(t)
	// A subscriber whose address changed after the bounces, and one whose address never changed.
	var id, steady int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'was.address@ok.test', 'P', 'enabled') RETURNING id`)
	e.db.MustExec(`UPDATE subscribers SET email = 'now.address@ok.test' WHERE id = $1`, id)
	e.get(&steady, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'steady.address@ok.test', 'P', 'enabled') RETURNING id`)
	bounce := func(sub int, typ, source, meta, at string) int {
		var bid int
		e.get(&bid, `INSERT INTO bounces (subscriber_id, type, source, meta, created_at) VALUES ($1, $2, $3, $4, `+at+`) RETURNING id`, sub, typ, source, meta)
		return bid
	}
	ts := "1700000000"
	at := "to_timestamp(" + ts + ")"
	bounce(id, "hard", "postmark", `{"Email":"Legacy.Old@ok.test"}`, "NOW()")
	bounce(id, "hard", "forwardemail", `{"recipient":"legacy.three@ok.test"}`, "NOW()")
	bounce(id, "hard", "lettermint", `{"data":{"recipient":"legacy.six@ok.test"}}`, "NOW()")
	bounce(id, "complaint", "azure", `{"data":{"recipient":"legacy.seven@ok.test"}}`, "NOW()")
	bounce(id, "hard", "ses", `{"mail":{"destination":["legacy.five@ok.test"]}}`, "NOW()")
	// SendGrid keeps the whole batch with each bounce: the bounce is its own event, not the first.
	bounce(id, "hard", "sendgrid", `[{"email":"delivered@ok.test","event":"delivered","timestamp":`+ts+`},`+
		`{"email":"Legacy.Four@ok.test","event":"bounce","timestamp":`+ts+`,"bounce_classification":"invalid"},`+
		`{"email":"legacy.soft2@ok.test","event":"bounce","timestamp":`+ts+`,"bounce_classification":"technical"},`+
		`{"email":"later@ok.test","event":"bounce","timestamp":1700000099}]`, at)
	// Two bounces of the same time and type in one batch: which one is not known.
	bounce(id, "hard", "sendgrid", `[{"email":"either.one@ok.test","event":"bounce","timestamp":1700000500},`+
		`{"email":"either.two@ok.test","event":"bounce","timestamp":1700000500}]`, "to_timestamp(1700000500)")
	// SES: a mail to two recipients that bounced for the second only.
	bounce(id, "hard", "ses", `{"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"SES-Bad@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-bad@ok.test"]}}`, "NOW()")
	// SendGrid: two hard bounces of the same second for two campaigns, each record knows its campaign.
	var campA, campB int
	var uuidA, uuidB string
	e.get(&campA, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'A','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&campB, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'B','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&uuidA, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, campA)
	e.get(&uuidB, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, campB)
	batch := `[{"email":"sg.delivered@ok.test","event":"delivered","timestamp":1700001000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"sg1@ok.test","event":"bounce","timestamp":1700001000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"sg2@ok.test","event":"bounce","timestamp":1700001000,"XListmonkCampaign":"` + uuidB + `"}]`
	for _, c := range []int{campA, campB} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700001000))`, id, c, batch)
	}
	// Two addresses of the same time without a campaign: the one of the subscriber, whose address
	// did not change since.
	var own int
	e.get(&own, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'sg.own@ok.test', 'P', 'enabled') RETURNING id`)
	bounce(own, "hard", "sendgrid", `[{"email":"sg.other@ok.test","event":"bounce","timestamp":1700002000},`+
		`{"email":"sg.own@ok.test","event":"bounce","timestamp":1700002000}]`, "to_timestamp(1700002000)")
	// SES: one notification that names two bounced recipients, neither the mail's first one.
	bounce(id, "hard", "ses", `{"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"multi-bad1@ok.test","status":"5.1.1"},{"emailAddress":"multi-bad2@ok.test","status":"5.1.1"}]},"mail":{"destination":["multi-fine@ok.test","multi-bad1@ok.test","multi-bad2@ok.test"]}}`, "NOW()")
	// SES: the collector before recorded the bounce of single-bad for single-first's subscriber.
	var singleFirst, singleBad int
	e.get(&singleFirst, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'single-first@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&singleBad, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'single-bad@ok.test', 'P', 'enabled') RETURNING id`)
	bounce(singleFirst, "hard", "ses", `{"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"single-bad@ok.test"}]},"mail":{"destination":["single-first@ok.test","single-bad@ok.test"]}}`, "NOW()")
	// SendGrid: two subscribers bounced in the same second for the same campaign, and then changed
	// their addresses. The two records of the batch are in the order of its bounce events.
	var same1, same2 int
	e.get(&same1, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'same1@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&same2, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'same2@ok.test', 'P', 'enabled') RETURNING id`)
	sameBatch := `[{"email":"same.delivered@ok.test","event":"delivered","timestamp":1700003000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"same1@ok.test","event":"bounce","timestamp":1700003000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"same2@ok.test","event":"bounce","timestamp":1700003000,"XListmonkCampaign":"` + uuidA + `"}]`
	for _, sub := range []int{same1, same2} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700003000))`, sub, campA, sameBatch)
	}
	e.db.MustExec(`UPDATE subscribers SET email = 'same1.now@ok.test' WHERE id = $1`, same1)
	e.db.MustExec(`UPDATE subscribers SET email = 'same2.now@ok.test' WHERE id = $1`, same2)
	// SES: a Permanent bounce with a failed and a delayed recipient: only the failed one is hard.
	bounce(id, "hard", "ses", `{"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"failed@ok.test","action":"failed","status":"5.0.0"},{"emailAddress":"delayed@ok.test","action":"delayed","status":"4.0.0"}]},"mail":{"destination":["failed@ok.test","delayed@ok.test"]}}`, "NOW()")
	// SES: a complaint that names two recipients who may have complained: nobody is accused.
	var maybe1 int
	e.get(&maybe1, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'maybe1@ok.test', 'P', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'maybe2@ok.test', 'P', 'enabled')`)
	bounce(maybe1, "complaint", "ses", `{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"maybe1@ok.test"},{"emailAddress":"maybe2@ok.test"}]},"mail":{"destination":["maybe1@ok.test","maybe2@ok.test"]}}`, "NOW()")
	// SES: two different bounces of the same recipient sent at the same moment: two records of its own.
	var twiceFirst, twiceBad int
	e.get(&twiceFirst, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'twice-first@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&twiceBad, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'twice-bad@ok.test', 'P', 'enabled') RETURNING id`)
	for _, f := range []string{"feedback-1", "feedback-2"} {
		bounce(twiceFirst, "hard", "ses", `{"bounce":{"feedbackId":"`+f+`","bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"twice-bad@ok.test","action":"failed","status":"5.1.1"}]},"mail":{"messageId":"`+f+`","timestamp":"2026-11-01T00:00:00.123Z","destination":["twice-first@ok.test","twice-bad@ok.test"]}}`, "'2026-11-01 00:00:00.123+00'")
	}
	// SendGrid: of two hard bounces of one batch, the subscriber of one was deleted before.
	var keptSub, gone int
	e.get(&keptSub, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'kept@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&gone, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'deleted@ok.test', 'P', 'enabled') RETURNING id`)
	keptBatch := `[{"email":"kept.delivered@ok.test","event":"delivered","timestamp":1700004000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"kept@ok.test","event":"bounce","timestamp":1700004000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"deleted@ok.test","event":"bounce","timestamp":1700004000,"XListmonkCampaign":"` + uuidA + `"}]`
	for _, sub := range []int{keptSub, gone} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700004000))`, sub, campA, keptBatch)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, gone)
	// SES: the collector before typed a Transient notification by its first recipient (4.2.2, soft),
	// and the second one had 5.4.4 (no such domain, hard): that one is kept, for nobody when nobody
	// has the address, with a hard record of its own for the subscriber who has it.
	var transFirst, transOwned int
	e.get(&transFirst, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'trans-full@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&transOwned, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'hard-owned@ok.test', 'P', 'enabled') RETURNING id`)
	bounce(transFirst, "soft", "ses", `{"bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"trans-full@ok.test","status":"4.2.2"},{"emailAddress":"hard-second@ok.test","status":"5.4.4"},{"emailAddress":"hard-owned@ok.test","status":"5.4.4"}]},"mail":{"destination":["trans-full@ok.test","hard-second@ok.test","hard-owned@ok.test"]}}`, "NOW()")
	// SendGrid: the subscriber of a hard bounce was deleted and the address added again (after the
	// bounce) before the upgrade: the bounce was the deleted subscriber's, and is kept.
	var recreatedOld, recreatedOther int
	e.get(&recreatedOld, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'recreated@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&recreatedOther, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'recreated.other@ok.test', 'P', 'enabled') RETURNING id`)
	recreatedBatch := `[{"email":"recreated@ok.test","event":"bounce","timestamp":1700006000,"bounce_classification":"invalid"},` +
		`{"email":"recreated.other@ok.test","event":"bounce","timestamp":1700006000,"bounce_classification":"invalid"}]`
	for _, sub := range []int{recreatedOld, recreatedOther} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source, meta, created_at) VALUES ($1, 'hard', 'sendgrid', $2, to_timestamp(1700006000))`, sub, recreatedBatch)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, recreatedOld)
	e.db.MustExec(`INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'recreated@ok.test', 'P', 'enabled')`)
	// SendGrid: a person deleted a subscriber's hard bounce record (to let the address go) and then
	// changed their address. Nothing before the migration tells this from a deleted subscriber, and
	// the old address is kept (decided 2026-11-23; let go by SQL, FORK.md). A subscriber of the same
	// campaign whose name only was edited changes nothing.
	var cleared, clearedOther, campC int
	e.get(&campC, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'C','S','test@example.invalid','b','email') RETURNING id`)
	var uuidC string
	e.get(&uuidC, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, campC)
	e.get(&cleared, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), 'cleared-old@ok.test', 'P', 'enabled', to_timestamp(1700004500), to_timestamp(1700004500)) RETURNING id`)
	e.get(&clearedOther, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'cleared.other@ok.test', 'P', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, to_timestamp(1700004900)), ($1, $3, to_timestamp(1700004900))`, campC, cleared, clearedOther)
	clearedBatch := `[{"email":"cleared-old@ok.test","event":"bounce","timestamp":1700005000,"XListmonkCampaign":"` + uuidC + `"},` +
		`{"email":"cleared.other@ok.test","event":"bounce","timestamp":1700005000,"XListmonkCampaign":"` + uuidC + `"}]`
	var clearedRecord int
	e.get(&clearedRecord, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700005000)) RETURNING id`, cleared, campC, clearedBatch)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700005000))`, clearedOther, campC, clearedBatch)
	e.db.MustExec(`DELETE FROM bounces WHERE id = $1`, clearedRecord)
	e.db.MustExec(`UPDATE subscribers SET email = 'cleared-new@ok.test' WHERE id = $1`, cleared)
	e.db.MustExec(`UPDATE subscribers SET name = 'Edited', updated_at = NOW() WHERE id = $1`, clearedOther)
	// SendGrid: the same batch delivered twice left two records per bounce event; then one of the
	// two subscribers was deleted. The two records left are the remaining subscriber's own, not
	// one per event: the deleted subscriber's address is kept apart from them.
	var replayKept, replayGone int
	e.get(&replayKept, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'replayed-kept@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&replayGone, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'replayed-gone@ok.test', 'P', 'enabled') RETURNING id`)
	replayBatch := `[{"email":"replayed-kept@ok.test","event":"bounce","timestamp":1700007000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"replayed-gone@ok.test","event":"bounce","timestamp":1700007000,"XListmonkCampaign":"` + uuidA + `"}]`
	for _, sub := range []int{replayKept, replayGone, replayKept, replayGone} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700007000))`, sub, campA, replayBatch)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, replayGone)
	// SendGrid: a person deleted the hard bounce record of a subscriber who kept their address (to
	// let it go), and the other subscriber of the batch had only their name edited (the edit before
	// the migration counts as a possible change of address). The released address stays let go,
	// as in the batch whose other subscriber was not edited.
	released := func(at int, mine, peer string, editPeer bool) {
		var me, other int
		e.get(&me, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, 'P', 'enabled', to_timestamp($2), to_timestamp($2)) RETURNING id`, mine, at-100)
		e.get(&other, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, 'P', 'enabled', to_timestamp($2), to_timestamp($2)) RETURNING id`, peer, at-100)
		batch := `[{"email":"` + mine + `","event":"bounce","timestamp":` + itoa(at) + `,"XListmonkCampaign":"` + uuidA + `"},` +
			`{"email":"` + peer + `","event":"bounce","timestamp":` + itoa(at) + `,"XListmonkCampaign":"` + uuidA + `"}]`
		var mine1 int
		e.get(&mine1, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp($4)) RETURNING id`, me, campA, batch, at)
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp($4))`, other, campA, batch, at)
		e.db.MustExec(`DELETE FROM bounces WHERE id = $1`, mine1)
		if editPeer {
			e.db.MustExec(`UPDATE subscribers SET name = 'Edited', updated_at = NOW() WHERE id = $1`, other)
		}
	}
	released(1700008000, "released-current@ok.test", "current-peer@ok.test", true)
	released(1700009000, "released-control@ok.test", "control-peer@ok.test", false)
	// SendGrid: the subscriber of a hard bounce was deleted, and another subscriber who got the
	// same campaign but has no record in the batch had only their name edited: the deleted
	// subscriber's address is kept all the same.
	var campE, depHolder, depGone, depPeer int
	var uuidE string
	e.get(&campE, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'E','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&uuidE, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, campE)
	for _, p := range []struct {
		id    *int
		email string
	}{{&depHolder, "dep-holder@ok.test"}, {&depGone, "deleted-edited-peer@ok.test"}, {&depPeer, "dep-peer@ok.test"}} {
		e.get(p.id, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, 'P', 'enabled', to_timestamp(1700009900), to_timestamp(1700009900)) RETURNING id`, p.email)
		e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, to_timestamp(1700009950))`, campE, *p.id)
	}
	depBatch := `[{"email":"deleted-edited-peer@ok.test","event":"bounce","timestamp":1700010000,"XListmonkCampaign":"` + uuidE + `"},` +
		`{"email":"dep-holder@ok.test","event":"bounce","timestamp":1700010000,"XListmonkCampaign":"` + uuidE + `"}]`
	for _, sub := range []int{depGone, depHolder} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700010000))`, sub, campE, depBatch)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, depGone)
	e.db.MustExec(`UPDATE subscribers SET name = 'Edited', updated_at = NOW() WHERE id = $1`, depPeer)
	// SendGrid: A and B bounced in the same second for the same campaign; then B moved to a new
	// address and A took B's old one. Each record is still for its own event: deleting A's record
	// lets A's old address go and not B's, whose record is left.
	var swapA, swapB int
	e.get(&swapA, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'swap-a@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&swapB, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'swap-b@ok.test', 'P', 'enabled') RETURNING id`)
	swapBatch := `[{"email":"swap-a@ok.test","event":"bounce","timestamp":1700011000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"swap-b@ok.test","event":"bounce","timestamp":1700011000,"XListmonkCampaign":"` + uuidA + `"}]`
	var swapARecord int
	e.get(&swapARecord, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700011000)) RETURNING id`, swapA, campA, swapBatch)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700011000))`, swapB, campA, swapBatch)
	e.db.MustExec(`UPDATE subscribers SET email = 'swap-b.new@ok.test' WHERE id = $1`, swapB)
	e.db.MustExec(`UPDATE subscribers SET email = 'swap-b@ok.test' WHERE id = $1`, swapA)
	// SendGrid: the same batch [A, B] delivered twice (A1, B1, A2, B2), then A1 and B2 deleted.
	// Two records for two subscribers are left, but not in the order of the batch: each is still
	// its own subscriber's, whose address did not change.
	var retryA, retryB int
	e.get(&retryA, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'retry-a@ok.test', 'P', 'enabled') RETURNING id`)
	e.get(&retryB, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'retry-b@ok.test', 'P', 'enabled') RETURNING id`)
	retryBatch := `[{"email":"retry-a@ok.test","event":"bounce","timestamp":1700012000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"retry-b@ok.test","event":"bounce","timestamp":1700012000,"XListmonkCampaign":"` + uuidA + `"}]`
	var retryIDs []int
	for _, sub := range []int{retryA, retryB, retryA, retryB} {
		var rid int
		e.get(&rid, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700012000)) RETURNING id`, sub, campA, retryBatch)
		retryIDs = append(retryIDs, rid)
	}
	e.db.MustExec(`DELETE FROM bounces WHERE id = ANY($1)`, pq.Array([]int{retryIDs[0], retryIDs[3]}))
	// The same, and B's name was edited before the upgrade (an edit then is not told apart from a
	// change of address): A's record left is A's by its unchanged address, which shows that the
	// records left are not in the order of the batch; B's record is B's by B's address now.
	var editedA, editedB int
	e.get(&editedA, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), 'edited-a@ok.test', 'P', 'enabled', to_timestamp(1700012900), to_timestamp(1700012900)) RETURNING id`)
	e.get(&editedB, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), 'edited-b@ok.test', 'P', 'enabled', to_timestamp(1700012900), to_timestamp(1700012900)) RETURNING id`)
	editedBatch := `[{"email":"edited-a@ok.test","event":"bounce","timestamp":1700013000,"XListmonkCampaign":"` + uuidA + `"},` +
		`{"email":"edited-b@ok.test","event":"bounce","timestamp":1700013000,"XListmonkCampaign":"` + uuidA + `"}]`
	var editedIDs []int
	for _, sub := range []int{editedA, editedB, editedA, editedB} {
		var rid int
		e.get(&rid, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, 'hard', 'sendgrid', $3, to_timestamp(1700013000)) RETURNING id`, sub, campA, editedBatch)
		editedIDs = append(editedIDs, rid)
	}
	e.db.MustExec(`DELETE FROM bounces WHERE id = ANY($1)`, pq.Array([]int{editedIDs[0], editedIDs[3]}))
	e.db.MustExec(`UPDATE subscribers SET name = 'Edited', updated_at = NOW() WHERE id = $1`, editedB)
	// The mailbox collector's bounces say no address: the current one only when it never changed.
	edited := bounce(id, "hard", "mailbox", `{"from":"mailer-daemon"}`, "NOW()")
	kept := bounce(steady, "hard", "mailbox", `{"from":"mailer-daemon"}`, "NOW()")
	bounce(id, "soft", "postmark", `{"Email":"legacy.soft@ok.test"}`, "NOW()")
	e.db.MustExec(`DELETE FROM axm_bounced_emails`)
	var records0 int
	e.get(&records0, `SELECT COUNT(*) FROM bounces`)
	var rows1 int
	for i := 0; i < 2; i++ {
		var logged bytes.Buffer
		if err := migrations.V6_2_1_axm_16(e.db, nil, nil, log.New(&logged, "", 0)); err != nil {
			t.Fatal(err)
		}
		// SES and SendGrid (decided 2026-11-24): every address a notification names as hard bounced
		// or complaining is kept, whatever became of the records and subscribers since: deleted,
		// added again, edited, addresses changed or swapped, batches delivered twice.
		for _, a := range []string{"legacy.old", "legacy.three", "legacy.four", "legacy.five", "legacy.six", "legacy.seven", "steady.address", "ses-bad", "sg1", "sg2", "sg.own",
			"multi-bad1", "multi-bad2", "single-bad", "same1", "same2", "either.one", "either.two",
			"later", "sg.other", "deleted", "kept", "failed", "twice-bad",
			"hard-second", "hard-owned", "recreated", "recreated.other", "cleared.other", "cleared-old",
			"replayed-kept", "replayed-gone", "current-peer", "control-peer", "deleted-edited-peer", "dep-holder",
			"swap-a", "swap-b", "retry-a", "retry-b", "edited-a", "edited-b", "released-current", "released-control"} {
			if !e.isBounced(a + "@ok.test") {
				t.Fatalf("run %d: %s not taken in", i+1, a)
			}
		}
		// Not hard bounced or complaining in any notification: delivered, soft, delayed, the mail's
		// other recipients, a complaint that names several who may have complained.
		for _, a := range []string{"delivered", "legacy.soft", "legacy.soft2", "now.address", "was.address", "ses-first", "sg.delivered",
			"multi-fine", "delayed", "maybe1", "maybe2", "kept.delivered", "trans-full", "cleared-new", "dep-peer", "swap-b.new", "single-first", "same.delivered", "same1.now", "same2.now"} {
			if e.isBounced(a + "@ok.test") {
				t.Fatalf("run %d: %s taken in", i+1, a)
			}
		}
		// The complaint with two who may have complained is the one item not judged; it is counted.
		if i == 0 && !strings.Contains(logged.String(), "1 not judged") {
			t.Fatalf("log: %q", logged.String())
		}
		// The second run adds nothing, and no run makes a bounce record.
		n := e.bouncedRows()
		var records int
		e.get(&records, `SELECT COUNT(*) FROM bounces`)
		if records != records0 {
			t.Fatalf("run %d: %d bounce records, %d before", i+1, records, records0)
		}
		if i == 0 {
			rows1 = n
		} else if n != rows1 {
			t.Fatalf("run 2: %d rows; run 1: %d", n, rows1)
		}
	}
	// An address of an SES or SendGrid notification is one row of its own, tied to no record or
	// subscriber: deleting bounce records of anyone does not let it go.
	for _, a := range []string{"single-bad", "twice-bad", "retry-a", "edited-a", "swap-a", "replayed-kept", "hard-owned"} {
		var all, alone int
		e.get(&all, `SELECT COUNT(*) FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1)`, a+"@ok.test")
		e.get(&alone, `SELECT COUNT(*) FROM axm_bounced_emails WHERE email_hash = axm_email_hash($1) AND bounce_id IS NULL AND subscriber_id IS NULL`, a+"@ok.test")
		if all != 1 || alone != 1 {
			t.Fatalf("%s: %d rows, %d by the address alone", a, all, alone)
		}
	}
	for _, sub := range []int{singleFirst, singleBad, twiceBad, replayKept, transOwned} {
		if err := e.a.core.DeleteSubscriberBounces(sub, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.a.core.DeleteBounces([]int{retryIDs[2], editedIDs[2], swapARecord}, false); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"single-bad", "twice-bad", "retry-a", "retry-b", "edited-a", "edited-b", "swap-a", "swap-b", "replayed-kept", "replayed-gone", "hard-owned"} {
		if !e.isBounced(a + "@ok.test") {
			t.Fatalf("%s let go by deleting bounce records", a)
		}
	}
	// A person lets such an address go by deleting its row (FORK.md); the others stay.
	e.db.MustExec(`DELETE FROM axm_bounced_emails WHERE email_hash = axm_email_hash(' Retry-A@ok.test ')`)
	if e.isBounced("retry-a@ok.test") || !e.isBounced("retry-b@ok.test") {
		t.Fatalf("SQL release: a %v, b %v", e.isBounced("retry-a@ok.test"), e.isBounced("retry-b@ok.test"))
	}
	if issues := e.reviewIssues("retry-a@ok.test", "retry-b@ok.test"); len(issues["0"]) != 0 || strings.Join(issues["1"], ",") != "bounced" {
		t.Fatalf("after the SQL release: %v", issues)
	}
	// No record is made for a recipient: not for a 5.4.4 recipient, not for one who may have complained.
	var made int
	e.get(&made, `SELECT COUNT(*) FROM bounces b JOIN subscribers s ON s.id = b.subscriber_id WHERE s.email IN ('maybe2@ok.test', 'hard-owned@ok.test')`)
	if made != 0 {
		t.Fatalf("%d bounce records made for recipients", made)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_bounced_emails WHERE bounce_id = $1`, edited)
	if n != 0 {
		t.Fatal("a mailbox bounce of an edited subscriber was taken in")
	}
	e.get(&n, `SELECT COUNT(*) FROM axm_bounced_emails WHERE bounce_id = $1 AND subscriber_id = $2`, kept, steady)
	if n != 1 {
		t.Fatal("a mailbox bounce of a subscriber whose address never changed was not taken in")
	}
	var trg int
	e.get(&trg, `SELECT COUNT(*) FROM pg_trigger WHERE tgname IN ('axm_bounced_release', 'axm_bounced_record', 'axm_email_changed') AND NOT tgisinternal
		AND tgrelid IN ('bounces'::regclass, 'subscribers'::regclass)`)
	if trg != 3 {
		t.Fatalf("%d triggers", trg)
	}
}

// The migration counts an edit before it as a change of address: a bounce that says no address
// is then for the current one only when the mail of its campaign was recorded after the edit.
func TestGateMigrationCountsEarlierEdits(t *testing.T) {
	e := newGateEnv(t)
	var id, camp int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status, created_at, updated_at) VALUES (gen_random_uuid(), 'edited@ok.test', 'P', 'enabled', NOW() - INTERVAL '30 days', NOW() - INTERVAL '20 days') RETURNING id`)
	e.get(&camp, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'C','S','test@example.invalid','b','email') RETURNING id`)
	e.db.MustExec(`DELETE FROM axm_email_changes`)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source, meta) VALUES ($1, 'hard', 'mailbox', '{}')`, id)
	if err := migrations.V6_2_1_axm_16(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("edited@ok.test") {
		t.Fatal("a bounce without a campaign was taken in for a subscriber edited before")
	}
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, NOW() - INTERVAL '10 days')`, camp, id)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta) VALUES ($1, $2, 'hard', 'mailbox', '{}')`, id, camp)
	if err := migrations.V6_2_1_axm_16(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !e.isBounced("edited@ok.test") {
		t.Fatal("a bounce of a campaign sent after the last edit was not taken in")
	}
}

// A row that is rolled back is not counted as imported with a warning.
func TestGateWarningIsCountedWhenCommitted(t *testing.T) {
	e := newGateEnv(t)
	// A NUL in an attribute cannot be stored in JSONB: the transaction of the batch fails.
	bad := "bad@ok.test,Bad,\"{\"\"a\"\":\"\"\\u0000\"\"}\"\n"
	csvIn := "email,name,attributes\ninfo@ok.test,Role,\n" + bad
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	w.WriteField("params", `{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[`+itoa(e.aList)+`]}`)
	f, _ := w.CreateFormFile("file", "people.csv")
	f.Write([]byte(csvIn))
	w.Close()
	req := httptest.NewRequest("POST", "/api/import/subscribers", body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if err := e.a.ImportSubscribers(c); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the import to end", func() bool {
		s := e.a.importer.GetStats().Status
		return s == subimporter.StatusFinished || s == subimporter.StatusFailed
	})
	st := e.a.importer.GetStats()
	if st.Status != subimporter.StatusFailed || st.Imported != 0 || st.AxmWarnedTotal != 0 || e.count("info@ok.test") != 0 {
		t.Fatalf("status %s imported %d warned %d (%v), stored %d", st.Status, st.Imported, st.AxmWarnedTotal, st.AxmWarned, e.count("info@ok.test"))
	}
	e.a.importer.Stop()
}

// The log of an import never holds an address, also when the attributes of a row the gate
// refuses are malformed.
func TestGateImportLogHasNoAddress(t *testing.T) {
	e := newGateEnv(t)
	csvIn := "email,name,attributes\nsecret.typo@gmail.con,Typo,{not json\nsecret.ok@ok.test,Ok,{not json\n"
	params := `{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[` + itoa(e.aList) + `]}`
	if code := e.importAs(e.as(e.superAdmin), params, csvIn); code != 200 {
		t.Fatal(code)
	}
	if log := string(e.a.importer.GetLogs()); strings.Contains(log, "secret.") {
		t.Fatalf("the log names an address:\n%s", log)
	}
}

// The column mapping asks what the import would do: with the gate at nodns or basic the import
// does not look at DNS, so the check does not either, and says how strict the gate is.
func TestGateAddressCheckFollowsTheStrength(t *testing.T) {
	e := newGateEnv(t)
	ask := func() (map[string][]string, string) {
		req := httptest.NewRequest("POST", "/api/subscribers/address-check", strings.NewReader(`{"emails":["kim@nowhere.test","kim@ok.test","kim@mailinator.com"]}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
		if err := e.a.CheckImportAddresses(c); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Data struct {
				Issues map[string][]string `json:"issues"`
				Gate   string              `json:"gate"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.Issues, resp.Data.Gate
	}
	if issues, gate := ask(); gate != "full" || strings.Join(issues["0"], ",") != "dead" {
		t.Fatalf("full: %v %s", issues, gate)
	}
	for _, mode := range []string{"nodns", "basic"} {
		e.a.addrGate = mode
		e.dns.calls = 0
		issues, gate := ask()
		if gate != mode || len(issues["0"]) != 0 || strings.Join(issues["2"], ",") != "disposable" || e.dns.calls != 0 {
			t.Fatalf("%s: %v %s, %d lookups", mode, issues, gate, e.dns.calls)
		}
	}
}

// A delayed bounce that names its campaign (or none) is for the address the subscriber had when
// that mail was sent: another campaign sent to the new address does not make it the new address's.
func TestGateBounceOfAnotherCampaignDoesNotMarkTheNewAddress(t *testing.T) {
	e := newGateEnv(t)
	e.noActionCore()
	id := e.person2("was@ok.test")
	var campA, campB int
	e.get(&campA, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'A','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&campB, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'B','S','test@example.invalid','b','email') RETURNING id`)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $3, NOW() - INTERVAL '16 days'), ($2, $3, NOW() - INTERVAL '1 day')`, campA, campB, id)
	e.db.MustExec(`UPDATE subscribers SET email = 'now@ok.test', updated_at = NOW() - INTERVAL '15 days' WHERE id = $1`, id)
	var uuidA string
	e.get(&uuidA, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, campA)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(id), CampaignUUID: uuidA, Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now().Add(-16 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e.hardBounce("", e.uuidOf(id))
	if e.isBounced("now@ok.test") {
		t.Fatal("a bounce of the old campaign marked the new address")
	}
}

// The same report delivered twice leaves nothing that outlives the subscriber's own records.
func TestGateRepeatedReportIsReleasedWithTheBounceRecords(t *testing.T) {
	e := newGateEnv(t)
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]struct {
			Count  int
			Action string
		}{"hard": {1, "blocklist"}, "soft": {3, "none"}, "complaint": {1, "blocklist"}},
	}}, nil)
	id := e.person2("twice@ok.test")
	e.hardBounce("twice@ok.test", e.uuidOf(id))
	e.hardBounce("twice@ok.test", e.uuidOf(id))
	if n := e.bouncedRows(); n != 1 {
		t.Fatalf("%d rows for one address", n)
	}
	e.db.MustExec(`UPDATE subscribers SET status = 'enabled' WHERE id = $1`, id)
	if err := e.a.core.DeleteSubscriberBounces(id, ""); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`DELETE FROM axm_suppressed_emails`)
	if e.bouncedRows() != 0 {
		t.Fatalf("%d rows left", e.bouncedRows())
	}
}

// A bounce that says no address (the mailbox collector's) is for the current address only when the
// address did not change since the mail it is for. An opt-in mail names no campaign, and a send
// record removed by retention proves nothing: neither marks a changed address, whatever else was
// sent to it since.
func TestGateDelayedBounceNeedsTheSendOfItsMail(t *testing.T) {
	e := newGateEnv(t)
	e.noActionCore()
	id := e.person2("optin.old@ok.test")
	if r := e.do(e.a.PatchSubscriber, "PATCH", id, `{"email":"optin.new@ok.test"}`); r.code != 200 {
		t.Fatalf("%+v", r)
	}
	// The first campaign after the change went to the new address.
	e.sentTo(id, "NOW()")
	// The bounce of the opt-in mail to the old address: subscriber UUID only.
	e.hardBounce("", e.uuidOf(id))
	if e.isBounced("optin.new@ok.test") {
		t.Fatal("a delayed opt-in bounce marked the new address")
	}
	// A campaign sent to the old address whose send record is gone.
	var camp, campUUID = 0, ""
	e.get(&camp, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'Old','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&campUUID, `SELECT uuid::TEXT FROM campaigns WHERE id = $1`, camp)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(id), CampaignUUID: campUUID, Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("optin.new@ok.test") {
		t.Fatal("a bounce of a campaign without a send record marked the new address")
	}
	if r := e.add("optin.new2@ok.test"); r.code != 200 {
		t.Fatalf("%+v", r)
	}
	// A campaign recorded as sent well after the change: the current address.
	e.db.MustExec(`UPDATE axm_email_changes SET changed_at = NOW() - INTERVAL '10 days' WHERE subscriber_id = $1`, id)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, NOW() - INTERVAL '2 days')`, camp, id)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(id), CampaignUUID: campUUID, Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !e.isBounced("optin.new@ok.test") {
		t.Fatal("a bounce of a campaign sent after the change did not mark the current address")
	}
	// Sent within the hour after the change (the send may have read the old address): not known.
	other := e.person2("race.old@ok.test")
	e.db.MustExec(`UPDATE subscribers SET email = 'race.new@ok.test' WHERE id = $1`, other)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at) VALUES ($1, $2, NOW() + INTERVAL '10 minutes')`, camp, other)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(other), CampaignUUID: campUUID, Type: "hard", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("race.new@ok.test") {
		t.Fatal("a send right after the change marked the new address")
	}
}

// A bounce over the count of the bounce action leaves no bounce record. Its address is kept all
// the same, also when it is the subscriber's current one, and it goes with the subscriber's
// bounce records.
func TestGateBounceOverTheCountKeepsTheAddress(t *testing.T) {
	e := newGateEnv(t)
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: e.a.i18n, Constants: core.Constants{
		BounceActions: map[string]struct {
			Count  int
			Action string
		}{"hard": {1, "none"}, "soft": {3, "none"}, "complaint": {1, "none"}},
	}}, nil)
	id := e.person2("cap.first@ok.test")
	e.hardBounce("cap.first@ok.test", e.uuidOf(id))
	e.db.MustExec(`UPDATE subscribers SET email = 'cap.second@ok.test' WHERE id = $1`, id)
	e.hardBounce("cap.second@ok.test", e.uuidOf(id))
	var records int
	e.get(&records, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1`, id)
	if records != 1 || !e.isBounced("cap.first@ok.test") || !e.isBounced("cap.second@ok.test") {
		t.Fatalf("records %d first %v second %v", records, e.isBounced("cap.first@ok.test"), e.isBounced("cap.second@ok.test"))
	}
	e.db.MustExec(`UPDATE subscribers SET email = 'cap.third@ok.test' WHERE id = $1`, id)
	if r := e.add("cap.second@ok.test"); r.code != 400 || r.rule != "bounced" {
		t.Fatalf("%+v", r)
	}
	// The same bounce once more: no second row.
	e.hardBounce("cap.third@ok.test", e.uuidOf(id))
	e.hardBounce("cap.third@ok.test", e.uuidOf(id))
	var rows int
	e.get(&rows, `SELECT COUNT(*) FROM axm_bounced_emails WHERE email_hash = axm_email_hash('cap.third@ok.test')`)
	if rows != 1 {
		t.Fatalf("%d rows for one address", rows)
	}
	// Deleting the subscriber's last bounce record releases them all.
	var bid int
	e.get(&bid, `SELECT id FROM bounces WHERE subscriber_id = $1`, id)
	if err := e.a.core.DeleteBounces([]int{bid}, false); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("cap.first@ok.test") || e.isBounced("cap.second@ok.test") || e.isBounced("cap.third@ok.test") {
		t.Fatal("not released with the last bounce record")
	}

	// A subscriber blocklisted by hand gets no bounce record at all. The blocklist keeps their own
	// address, here and once they are deleted, and lets it go when a person takes them off it: the
	// subscriber screen has no bounce record to delete, so nothing else may hold the address.
	blocked := e.person2("by.hand@ok.test")
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = $1`, blocked)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(blocked), Type: "complaint", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if e.isBounced("by.hand@ok.test") {
		t.Fatal("the complaint of a blocklisted subscriber was kept apart from the blocklist")
	}
	if issues := e.reviewIssues("by.hand@ok.test"); strings.Join(issues["0"], ",") != "suppressed" {
		t.Fatalf("while blocklisted: %v", issues)
	}
	e.db.MustExec(`UPDATE subscribers SET status = 'enabled' WHERE id = $1`, blocked)
	if issues := e.reviewIssues("by.hand@ok.test"); len(issues["0"]) != 0 {
		t.Fatalf("taken off the blocklist, still: %v", issues)
	}
	// Deleted while blocklisted: the blocklist still refuses the address.
	gone := e.person2("by.hand.gone@ok.test")
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = $1`, gone)
	if err := e.a.core.RecordBounce(models.Bounce{SubscriberUUID: e.uuidOf(gone), Type: "complaint", Source: "t",
		Meta: json.RawMessage(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, gone)
	if r := e.add("by.hand.gone@ok.test"); r.code != 400 || r.rule != "suppressed" {
		t.Fatalf("%+v", r)
	}
	// Through the list-bounded call too.
	e.hardBounce("cap.third@ok.test", e.uuidOf(id))
	e.hardBounce("cap.third@ok.test", e.uuidOf(id))
	if locked, err := e.a.core.DeleteSubscriberBouncesBounded(id, []int{e.aList}); err != nil || len(locked) != 0 {
		t.Fatal(locked, err)
	}
	if e.isBounced("cap.third@ok.test") {
		t.Fatal("not released by the bounded delete")
	}
}

// reviewIssues runs the address check of the column mapping for the addresses.
func (e *gateEnv) reviewIssues(emails ...string) map[string][]string {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"emails": emails})
	req := httptest.NewRequest("POST", "/api/subscribers/address-check", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if err := e.a.CheckImportAddresses(c); err != nil {
		e.t.Fatal(err)
	}
	var resp struct {
		Data struct {
			Issues map[string][]string `json:"issues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		e.t.Fatal(err)
	}
	return resp.Data.Issues
}

// An import of several gate batches with a pool of one database connection: the gate's database
// read of each next batch must not wait for the import's own open transaction.
func TestGateImportWithOneConnection(t *testing.T) {
	e := newGateEnv(t)
	e.a.addrGate = "nodns"
	e.db.SetMaxOpenConns(1)
	defer e.db.SetMaxOpenConns(0)
	var b strings.Builder
	b.WriteString("email,name\n")
	for i := 0; i < 2001; i++ {
		b.WriteString("one.conn." + itoa(i) + "@ok.test,P\n")
	}
	params := `{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[` + itoa(e.aList) + `]}`
	if code := e.importAs(e.as(e.superAdmin), params, b.String()); code != 200 {
		t.Fatal(code)
	}
	if st := e.lastImport; st.Imported != 2001 {
		t.Fatalf("stats %+v", st)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE email LIKE 'one.conn.%'`)
	if n != 2001 {
		t.Fatalf("%d subscribers", n)
	}
}
