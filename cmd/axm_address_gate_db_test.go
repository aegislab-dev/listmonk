package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
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
		{"kim@aonly.test", ""},      // an address record is enough
		{"info@ok.test", "role"},    // a role address comes in, with a warning
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
	var hits int
	e.get(&hits, `SELECT hits FROM axm_bounced_emails WHERE email_hash = axm_email_hash('hard@ok.test')`)
	if hits != 1 {
		t.Fatalf("hits %d", hits)
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
	var hits int
	e.get(&hits, `SELECT hits FROM axm_bounced_emails WHERE email_hash = axm_email_hash('rel@ok.test')`)
	if hits != 2 {
		t.Fatalf("hits %d", hits)
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

// The migration can run twice, and an upgrade fills the table from the bounce records there are.
func TestGateMigrationTwice(t *testing.T) {
	e := newGateEnv(t)
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'Old.Hard@ok.test', 'P', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source) VALUES ($1, 'hard', 't'), ($1, 'hard', 't'), ($1, 'soft', 't')`, id)
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'only.soft@ok.test', 'P', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source) VALUES ($1, 'soft', 't')`, id)
	e.db.MustExec(`DELETE FROM axm_bounced_emails`)

	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_16(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if n := e.bouncedRows(); n != 1 || !e.isBounced("old.hard@ok.test") || e.isBounced("only.soft@ok.test") {
			t.Fatalf("run %d: %d rows", i+1, n)
		}
	}
	var hits int
	e.get(&hits, `SELECT hits FROM axm_bounced_emails`)
	if hits != 2 {
		t.Fatalf("hits %d", hits)
	}
	var trg int
	e.get(&trg, `SELECT COUNT(*) FROM pg_trigger WHERE tgrelid = 'bounces'::regclass AND tgname = 'axm_bounced_release' AND NOT tgisinternal`)
	if trg != 1 {
		t.Fatalf("%d triggers", trg)
	}
}
