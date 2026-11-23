package main

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/labstack/echo/v4"
)

// LST-53 step 1: the address rules (Gmail mailbox names, misspelled domains) refuse an
// address where a subscriber is added, and the import says what it left out and why.

func newRulesEnv(t *testing.T) *boundaryEnv {
	t.Helper()
	e := newBoundaryEnv(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "i18n", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.a.i18n = lang
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: lang}, nil)
	e.a.importer = subimporter.New(subimporter.Options{
		UpsertStmt:         e.q.UpsertSubscriber.Stmt,
		BlocklistStmt:      e.q.UpsertBlocklistSubscriber.Stmt,
		UpdateListDateStmt: e.q.UpdateListsDate.Stmt,
		PostCB:             func(string, any) error { return nil },
	}, e.db.DB, lang)
	return e
}

func (e *boundaryEnv) count(email string) int {
	e.t.Helper()
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE email = $1`, email)
	return n
}

// save runs a handler as the super admin and returns the HTTP status and the message of its error.
func (e *boundaryEnv) save(h echo.HandlerFunc, method string, id int, body string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/subscribers", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if id != 0 {
		c.Set("id", id)
	}
	if err := h(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			e.lastSuggest = rec.Header().Get("X-Axm-Suggest")
			e.lastRule = rec.Header().Get("X-Axm-Address-Rule")
			msg, _ := he.Message.(string)
			return he.Code, msg
		}
		e.t.Fatal(err)
	}
	return rec.Code, ""
}

func TestAddressRulesSingleAdd(t *testing.T) {
	e := newRulesEnv(t)
	list := e.aList
	add := func(email string) (int, string) {
		return e.callAdd(`{"email":"` + email + `","name":"Synthetic","status":"enabled","lists":[` + itoa(list) + `]}`)
	}

	// The wrong addresses of the production review: misspelled domains and Gmail names that cannot exist.
	for email, want := range map[string]string{
		"kimlee@gmail.con":   "kimlee@gmail.com",
		"kimlee@gmaii.com":   "kimlee@gmail.com",
		"kimlee@gmal.com":    "kimlee@gmail.com",
		"kimlee@gmail.co":    "kimlee@gmail.com",
		"kimlee@company.con": "kimlee@company.com",
		"Kim.Lee@Gmail.Con":  "kim.lee@gmail.com",
	} {
		code, msg := add(email)
		if code != http.StatusBadRequest || !strings.Contains(msg, "Did you mean "+want+"?") {
			t.Errorf("%s: want 400 with the suggestion %s, got %d %q", email, want, code, msg)
		}
		if e.lastSuggest != want {
			t.Errorf("%s: header suggestion %q, want %q", email, e.lastSuggest, want)
		}
		if e.count(strings.ToLower(email)) != 0 {
			t.Errorf("%s: must not be stored", email)
		}
	}
	for _, email := range []string{"abc@gmail.com", "john_smith77@gmail.com", "john-smith77@gmail.com", "abc12@googlemail.com"} {
		code, msg := add(email)
		if code != http.StatusBadRequest || !strings.Contains(msg, "Gmail names are 6–30") {
			t.Errorf("%s: want 400 provider rule, got %d %q", email, code, msg)
		}
		if e.count(email) != 0 {
			t.Errorf("%s: must not be stored", email)
		}
	}

	// A domain that breaks the label syntax is refused as malformed, and a real company's
	// domain is not taken for a misspelling.
	for _, email := range []string{"review@bad_domain.example", "review@-bad.example", "review@bad-.example"} {
		if code, _ := add(email); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", email, code)
		}
	}
	if code, msg := add("naber@naber.com"); code != http.StatusOK {
		t.Errorf("naber.com is a real domain: %d %q", code, msg)
	}

	// Good addresses go in; a name with a tag and Workspace domains too.
	for _, email := range []string{"kim.lee@gmail.com", "kimlee+news@gmail.com", "ab@workspace-customer.example", "kim_lee@company.co"} {
		if code, msg := add(email); code != http.StatusOK {
			t.Errorf("%s: want 200, got %d %q", email, code, msg)
		}
	}
}

func (e *boundaryEnv) callAdd(body string) (int, string) {
	return e.save(e.a.CreateSubscriber, "POST", 0, body)
}

// An address is held to the rules when it changes, not when the person edits something else.
func TestAddressRulesEdit(t *testing.T) {
	e := newRulesEnv(t)
	// An old subscriber whose address would not pass today.
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'old@gmail.con', 'Old', 'enabled') RETURNING id`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, id, e.aList)

	body := func(email, name string) string {
		return `{"email":"` + email + `","name":"` + name + `","status":"enabled","lists":[` + itoa(e.aList) + `]}`
	}
	// PUT: the name changes, the address does not: allowed.
	if code, msg := e.save(e.a.UpdateSubscriber, "PUT", id, body("old@gmail.con", "Old Renamed")); code != http.StatusOK {
		t.Fatalf("an edit that keeps the address: %d %q", code, msg)
	}
	// PUT: a new address with a typo: refused, and the address stays.
	if code, msg := e.save(e.a.UpdateSubscriber, "PUT", id, body("new@gmail.con", "Old Renamed")); code != http.StatusBadRequest || !strings.Contains(msg, "new@gmail.com") {
		t.Fatalf("a new address with a typo: %d %q", code, msg)
	}
	if r := e.row(id); r.Email != "old@gmail.con" {
		t.Fatalf("the address changed: %+v", r)
	}
	// An error that has nothing to do with the address carries no address header.
	if code, _ := e.save(e.a.PatchSubscriber, "PATCH", id, `{"name":"`+strings.Repeat("x", 2500)+`"}`); code != http.StatusBadRequest || e.lastRule != "" || e.lastSuggest != "" {
		t.Fatalf("a long name on an old address: %d rule %q suggest %q", code, e.lastRule, e.lastSuggest)
	}
	// PATCH: only the name: allowed. A new bad address: refused. A good new address: allowed.
	if code, msg := e.save(e.a.PatchSubscriber, "PATCH", id, `{"name":"Patched"}`); code != http.StatusOK {
		t.Fatalf("patch of the name: %d %q", code, msg)
	}
	if code, _ := e.save(e.a.PatchSubscriber, "PATCH", id, `{"email":"abc@gmail.com"}`); code != http.StatusBadRequest {
		t.Fatalf("patch to a Gmail name under 6: %d", code)
	}
	if code, msg := e.save(e.a.PatchSubscriber, "PATCH", id, `{"email":"fixed.name@gmail.com"}`); code != http.StatusOK {
		t.Fatalf("patch to a good address: %d %q", code, msg)
	}
}

func TestAddressRulesImport(t *testing.T) {
	e := newRulesEnv(t)
	csvIn := "email,name\n" +
		"good.one@example.invalid,\"Good\nOne\"\n" + // a cell with a line break is one row
		"\n" + // a blank line: the reader skips it, the file line numbers do not
		"kimlee@gmail.con,Typo One\n" +
		"kimlee@gmaii.com,Typo Two\n" +
		"kimlee@gmal.com,Typo Three\n" +
		"abc@gmail.com,Short\n" +
		"john_smith77@gmail.com,Underscore\n" +
		"not-an-address,Broken\n" +
		"kimlee@gmail.com,Good Two\n"
	params := `{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[` + itoa(e.aList) + `],"axm_excluded":[{"row":50,"email":"x@gone.invalid","reason":"dead"},{"row":51,"email":"y@z","reason":"bogus"}]}`
	if code := e.importAs(e.as(e.superAdmin), params, csvIn); code != 200 {
		t.Fatal(code)
	}
	st := e.lastImport
	if st.Imported != 2 || st.AxmRejectedTotal != 7 || st.AxmExcluded != 1 || st.AxmRejected["dead"] != 1 ||
		st.AxmRejected["typo"] != 3 || st.AxmRejected["provider"] != 2 || st.AxmRejected["format"] != 1 {
		t.Fatalf("stats %+v", st)
	}
	if e.count("kimlee@gmail.con") != 0 || e.count("abc@gmail.com") != 0 || e.count("kimlee@gmail.com") != 1 || e.count("good.one@example.invalid") != 1 {
		t.Fatal("only the good rows are stored")
	}

	// The refused rows come back as CSV with the file line, the reason and the suggestion.
	get := func(u auth.User, id int) (int, string) {
		req := httptest.NewRequest("GET", "/api/import/subscribers/rejected?id="+itoa(id), nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(auth.UserHTTPCtxKey, u)
		if err := e.a.GetImportRejected(c); err != nil {
			he := err.(*echo.HTTPError)
			return he.Code, ""
		}
		return rec.Code, rec.Body.String()
	}
	id := st.AxmImportID
	if id == 0 {
		t.Fatal("the status carries the import number")
	}
	// Only the user who started the import (or one who reads all subscribers) gets the rows;
	// an import that was replaced is gone.
	if code, _ := get(e.as(e.ub), id); code != http.StatusForbidden {
		t.Fatalf("another user: %d", code)
	}
	if code, _ := get(e.as(e.superAdmin), id+1); code != http.StatusGone {
		t.Fatalf("a wrong import number: %d", code)
	}
	code, body := get(e.as(e.superAdmin), id)
	if code != 200 {
		t.Fatal(code)
	}
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 8 || rows[0][0] != "Row (header = 1)" {
		t.Fatalf("csv: %v", rows)
	}
	if got := strings.Join(rows[1], "|"); got != "50|x@gone.invalid|No domain or mail server|" {
		t.Fatalf("the row the column mapping left out comes first: %s", got)
	}
	if got := strings.Join(rows[2], "|"); got != "4|kimlee@gmail.con|Misspelled domain|kimlee@gmail.com" {
		t.Fatalf("first refused row (row 4: the blank line counts, the cell with a line break does not): %s", got)
	}
	if got := strings.Join(rows[7], "|"); got != "9|not-an-address|Malformed address|" {
		t.Fatalf("last refused row: %s", got)
	}
	e.a.importer.Stop()

	// Blocklisting takes any address.
	blk := `{"mode":"blocklist","delim":",","lists":[]}`
	if code := e.importAs(e.as(e.superAdmin), blk, "email,name\nkimlee@gmail.con,X\nabc@gmail.com,Y\n"); code != 200 {
		t.Fatal(code)
	}
	if st := e.lastImport; st.Imported != 2 || st.AxmRejectedTotal != 0 {
		t.Fatalf("blocklist stats %+v", st)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE email IN ('kimlee@gmail.con','abc@gmail.com') AND status = 'blocklisted'`)
	if n != 2 {
		t.Fatalf("blocklisted %d", n)
	}
}
