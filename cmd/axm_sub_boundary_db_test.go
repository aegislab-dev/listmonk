package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// LST-42: a team member changes a subscriber only when every subscription of that subscriber
// is on a list the member may manage.

// boundaryEnv is the teams database with one subscriber per case:
//
//	s1: A1 only                       s4: A1 + B1 (unsubscribed there)
//	s2: A1 + B1 (the other team)      s5: A1 + Both (both teams own it)
//	s3: A1 + Plain (no team)          s6: Both only
type boundaryEnv struct {
	*teamsEnv
	s1, s2, s3, s4, s5, s6 int
}

func newBoundaryEnv(t *testing.T) *boundaryEnv {
	e := &boundaryEnv{teamsEnv: newAuditEnv(t)}
	e.a.importer = subimporter.New(subimporter.Options{}, e.db.DB, e.a.i18n)
	// The synthetic subscribers and their list are nobody's.
	e.db.MustExec(`DELETE FROM subscribers`)
	e.s1 = e.person("s1", e.aList, "confirmed")
	e.s2 = e.person("s2", e.aList, "confirmed", e.bList, "confirmed")
	e.s3 = e.person("s3", e.aList, "confirmed", e.plainList, "confirmed")
	e.s4 = e.person("s4", e.aList, "confirmed", e.bList, "unsubscribed")
	e.s5 = e.person("s5", e.aList, "confirmed", e.bothList, "confirmed")
	e.s6 = e.person("s6", e.bothList, "confirmed")
	return e
}

// person adds a subscriber with subscriptions given as list ID, status pairs.
func (e *teamsEnv) person(name string, rows ...any) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status, attribs)
		VALUES (gen_random_uuid(), $1 || '@example.invalid', $1, 'enabled', '{"city":"Seoul","n":3}') RETURNING id`, name)
	for i := 0; i+1 < len(rows); i += 2 {
		e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, $3::subscription_status)`, id, rows[i], rows[i+1])
	}
	return id
}

// subRow is what the database holds of a subscriber: the profile and "list:status" pairs.
type subRow struct {
	Name, Email, Status, Attribs string
	Lists                        []string
	Exists                       bool
}

func (e *teamsEnv) row(id int) subRow {
	e.t.Helper()
	var out subRow
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE id = $1`, id)
	if n == 0 {
		return out
	}
	out.Exists = true
	if err := e.db.QueryRow(`SELECT name, email, status::TEXT, attribs::TEXT FROM subscribers WHERE id = $1`, id).
		Scan(&out.Name, &out.Email, &out.Status, &out.Attribs); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Select(&out.Lists, `SELECT l.name || ':' || sl.status FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id WHERE sl.subscriber_id = $1 ORDER BY l.name`, id); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *teamsEnv) wantLists(id int, what string, want ...string) {
	e.t.Helper()
	got := e.row(id).Lists
	if len(got) == 0 && len(want) == 0 {
		return
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		e.t.Fatalf("%s: lists %v, want %v", what, got, want)
	}
}

// putBody is the edit form's request for a subscriber: the whole profile and the lists.
func (e *teamsEnv) putBody(id int, change func(m map[string]any), lists ...int) string {
	e.t.Helper()
	r := e.row(id)
	var attribs map[string]any
	json.Unmarshal([]byte(r.Attribs), &attribs)
	m := map[string]any{"id": id, "email": r.Email, "name": r.Name, "status": r.Status, "attribs": attribs,
		"lists": append([]int{}, lists...), "preconfirm_subscriptions": true}
	if change != nil {
		change(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func lockedIn(data json.RawMessage) bool {
	var s models.Subscriber
	json.Unmarshal(data, &s)
	return s.EditLocked
}

// Reading stays as it was. The answer says which subscribers the user cannot change.
func TestBoundaryReadMarksLocked(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	for id, want := range map[int]bool{e.s1: false, e.s2: true, e.s3: true, e.s4: true, e.s5: false, e.s6: false} {
		code, data := e.call(e.a.GetSubscriber, ua, "GET", "/x", itoa(id), "")
		if code != 200 || lockedIn(data) != want {
			t.Fatalf("subscriber %d: %d, edit_locked %v, want %v", id, code, lockedIn(data), want)
		}
		if !want && strings.Contains(string(data), "edit_locked") {
			t.Fatalf("a subscriber that is not locked carries no mark: %s", data)
		}
	}
	// s4 is locked by an unsubscribed row only: no "other teams" mark, but locked.
	_, data := e.call(e.a.GetSubscriber, ua, "GET", "/x", itoa(e.s4), "")
	if strings.Contains(string(data), "other_teams") {
		t.Fatalf("an unsubscribed row is not an active use: %s", data)
	}

	_, data = e.call(e.a.QuerySubscribers, ua, "GET", "/api/subscribers", "", "")
	var page struct {
		Results []models.Subscriber `json:"results"`
		Total   int                 `json:"total"`
	}
	json.Unmarshal(data, &page)
	locked := []string{}
	for _, s := range page.Results {
		if s.EditLocked {
			locked = append(locked, s.Name)
		}
	}
	sort.Strings(locked)
	if strings.Join(locked, ",") != "s2,s3,s4" {
		t.Fatalf("locked in the list: %v", locked)
	}

	// Whoever manages all lists gets no mark.
	for _, u := range []int{e.admin, e.superAdmin} {
		_, data = e.call(e.a.GetSubscriber, e.as(u), "GET", "/x", itoa(e.s2), "")
		if strings.Contains(string(data), "edit_locked") {
			t.Fatalf("%s", data)
		}
		_, data = e.call(e.a.QuerySubscribers, e.as(u), "GET", "/api/subscribers", "", "")
		if strings.Contains(string(data), "edit_locked") {
			t.Fatalf("%s", data)
		}
	}
}

// The edit form: a locked subscriber keeps their profile, and the member's own lists change.
func TestBoundaryEditKeepsProfile(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	put := func(id int, body string) int {
		code, _ := e.call(e.a.UpdateSubscriber, ua, "PUT", "/x", itoa(id), body)
		return code
	}
	patch := func(id int, body string) int {
		code, _ := e.call(e.a.PatchSubscriber, ua, "PATCH", "/x", itoa(id), body)
		return code
	}
	before := e.row(e.s2)

	for what, change := range map[string]func(m map[string]any){
		"name":    func(m map[string]any) { m["name"] = "renamed" },
		"email":   func(m map[string]any) { m["email"] = "other@example.invalid" },
		"status":  func(m map[string]any) { m["status"] = "blocklisted" },
		"attribs": func(m map[string]any) { m["attribs"] = map[string]any{"city": "Busan", "n": 3} },
		"attribs dropped": func(m map[string]any) { delete(m, "attribs") },
	} {
		if code := put(e.s2, e.putBody(e.s2, change, e.aList)); code != http.StatusForbidden {
			t.Fatalf("PUT changing %s of a locked subscriber: %d", what, code)
		}
	}
	for _, body := range []string{`{"name":"renamed"}`, `{"email":"other@example.invalid"}`, `{"status":"blocklisted"}`, `{"attribs":{"city":"Busan"}}`} {
		if code := patch(e.s2, body); code != http.StatusForbidden {
			t.Fatalf("PATCH %s of a locked subscriber: %d", body, code)
		}
	}
	if got := e.row(e.s2); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Fatalf("a refused save changed the subscriber: %+v", got)
	}

	// The same profile with other lists: the member's list goes, the other team's row stays.
	if code := put(e.s2, e.putBody(e.s2, nil)); code != 200 {
		t.Fatalf("PUT with the profile as it is: %d", code)
	}
	e.wantLists(e.s2, "leaving the member's list", "B1:confirmed")
	if got := e.row(e.s2); got.Name != before.Name || got.Email != before.Email || got.Status != before.Status || got.Attribs != before.Attribs {
		t.Fatalf("the profile changed: %+v", got)
	}
	// s2 is now on no list of the member: it is out of reach altogether.
	if code := put(e.s2, e.putBody(e.s2, nil, e.aList)); code != http.StatusForbidden {
		t.Fatalf("a subscriber on no list of the member: %d", code)
	}

	// The e-mail in other letters and the name with spaces around it are the same profile.
	if code := put(e.s3, e.putBody(e.s3, func(m map[string]any) {
		m["email"] = "S3@Example.Invalid"
		m["name"] = " s3 "
	}, e.aList, e.bothList)); code != 200 {
		t.Fatalf("the same profile written differently: %d", code)
	}
	e.wantLists(e.s3, "joining another list of the member", "A1:confirmed", "Both:confirmed", "Plain:confirmed")
	if got := e.row(e.s3); got.Email != "s3@example.invalid" || got.Name != "s3" {
		t.Fatalf("%+v", got)
	}

	// PATCH: lists only, or the profile as it is.
	if code := patch(e.s4, `{"lists":[`+itoa(e.aList)+`,`+itoa(e.bothList)+`]}`); code != 200 {
		t.Fatalf("PATCH lists of a locked subscriber: %d", code)
	}
	e.wantLists(e.s4, "PATCH lists", "A1:confirmed", "B1:unsubscribed", "Both:unconfirmed")
	if code := patch(e.s4, `{"name":"s4","attribs":{"city":"Seoul"}}`); code != 200 {
		t.Fatalf("PATCH with the values it has: %d", code)
	}
	if got := e.row(e.s4); !strings.Contains(got.Attribs, `"n": 3`) {
		t.Fatalf("attributes: %s", got.Attribs)
	}

	// A subscriber of the member's lists only is edited as before.
	if code := put(e.s1, e.putBody(e.s1, func(m map[string]any) { m["name"] = "s1 renamed" }, e.aList)); code != 200 {
		t.Fatalf("own subscriber: %d", code)
	}
	if code := patch(e.s5, `{"name":"s5 renamed"}`); code != 200 {
		t.Fatalf("a subscriber on the member's lists only (one shared by two teams): %d", code)
	}
	if e.row(e.s1).Name != "s1 renamed" || e.row(e.s5).Name != "s5 renamed" {
		t.Fatal("the edit did not reach the database")
	}

	// An admin edits anybody.
	if code, _ := e.call(e.a.UpdateSubscriber, e.as(e.admin), "PUT", "/x", itoa(e.s3),
		e.putBody(e.s3, func(m map[string]any) { m["name"] = "by admin" }, e.aList, e.plainList)); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if e.row(e.s3).Name != "by admin" {
		t.Fatal("the admin's edit did not reach the database")
	}
}

// Blocklist, delete and bounce records by ID.
func TestBoundaryBlockDeleteByID(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	for _, id := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source) VALUES ($1, 'hard', 'test')`, id)
	}
	bounces := func(id int) int {
		var n int
		e.get(&n, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1`, id)
		return n
	}
	locked := map[int]bool{e.s2: true, e.s3: true, e.s4: true}

	for _, id := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6} {
		want := 200
		if locked[id] {
			want = http.StatusForbidden
		}
		if code, _ := e.call(e.a.DeleteSubscriberBounces, ua, "DELETE", "/x", itoa(id), ""); code != want {
			t.Fatalf("bounces of %d: %d", id, code)
		}
		if got := bounces(id); (got == 0) == locked[id] {
			t.Fatalf("bounces of %d left: %d", id, got)
		}
		if code, _ := e.call(e.a.BlocklistSubscriber, ua, "PUT", "/x", itoa(id), ""); code != want {
			t.Fatalf("blocklist %d: %d", id, code)
		}
		if got := e.row(id).Status; (got == "blocklisted") == locked[id] {
			t.Fatalf("status of %d: %s", id, got)
		}
		if code, _ := e.call(e.a.DeleteSubscriber, ua, "DELETE", "/x", itoa(id), ""); code != want {
			t.Fatalf("delete %d: %d", id, code)
		}
		if e.row(id).Exists != locked[id] {
			t.Fatalf("subscriber %d exists: %v", id, e.row(id).Exists)
		}
	}
	e.wantLists(e.s2, "the other team's subscription", "A1:confirmed", "B1:confirmed")
	e.wantLists(e.s4, "the other team's unsubscribe record", "A1:confirmed", "B1:unsubscribed")
}

// One locked subscriber in a selection refuses the whole request.
func TestBoundaryBulkByIDsIsAllOrNothing(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	ids := fmt.Sprintf(`{"ids":[%d,%d]}`, e.s1, e.s2)
	if code, _ := e.call(e.a.BlocklistSubscribers, ua, "PUT", "/x", "", ids); code != http.StatusForbidden {
		t.Fatalf("blocklist with a locked subscriber: %d", code)
	}
	if e.row(e.s1).Status != "enabled" || e.row(e.s2).Status != "enabled" {
		t.Fatal("somebody was blocklisted")
	}
	if code, _ := e.call(e.a.DeleteSubscribers, ua, "DELETE", fmt.Sprintf("/api/subscribers?id=%d&id=%d", e.s1, e.s2), "", ""); code != http.StatusForbidden {
		t.Fatalf("delete with a locked subscriber: %d", code)
	}
	if !e.row(e.s1).Exists || !e.row(e.s2).Exists {
		t.Fatal("somebody was deleted")
	}
	if n := len(e.audits("action IN ($1, $2)", auditSubBlocklist, auditSubDelete)); n != 0 {
		t.Fatalf("a refused action left %d audit rows", n)
	}
	// The error names the locked subscriber, not the others.
	err := e.a.hasSubWritePerm(ua, []int{e.s1, e.s2, e.s3})
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("%v", err)
	}

	// Without the locked one both go through.
	ok2 := fmt.Sprintf(`{"ids":[%d,%d]}`, e.s1, e.s5)
	if code, _ := e.call(e.a.BlocklistSubscribers, ua, "PUT", "/x", "", ok2); code != 200 {
		t.Fatalf("%d", code)
	}
	if code, _ := e.call(e.a.DeleteSubscribers, ua, "DELETE", fmt.Sprintf("/api/subscribers?id=%d&id=%d", e.s1, e.s6), "", ""); code != 200 {
		t.Fatalf("%d", code)
	}
	if e.row(e.s1).Exists || e.row(e.s6).Exists || e.row(e.s5).Status != "blocklisted" {
		t.Fatal("the allowed actions did not reach the database")
	}
	// An admin is not held back.
	if code, _ := e.call(e.a.BlocklistSubscribers, e.as(e.admin), "PUT", "/x", "", fmt.Sprintf(`{"ids":[%d,%d]}`, e.s2, e.s3)); code != 200 {
		t.Fatalf("admin: %d", code)
	}
}

func (e *teamsEnv) names(where string) string {
	e.t.Helper()
	out := []string{}
	if err := e.db.Select(&out, `SELECT name FROM subscribers WHERE `+where+` ORDER BY name`); err != nil {
		e.t.Fatal(err)
	}
	return strings.Join(out, ",")
}

func skippedShared(t *testing.T, r auditRow) (int, bool) {
	t.Helper()
	var d map[string]any
	json.Unmarshal(r.Detail, &d)
	v, ok := d["skipped_shared"].(float64)
	return int(v), ok
}

// "Select all" on a list, then delete or blocklist: the people who are on other lists too are
// left out, and the audit row says how many.
func TestBoundaryBulkByQuerySkipsShared(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	last := func(action string) auditRow {
		t.Helper()
		rows := e.audits("action = $1", action)
		if len(rows) == 0 {
			t.Fatalf("no %s row", action)
		}
		return rows[len(rows)-1]
	}
	body := fmt.Sprintf(`{"all":true,"list_ids":[%d]}`, e.aList)

	if code, _ := e.call(e.a.BlocklistSubscribersByQuery, ua, "PUT", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	if got := e.names(`status = 'blocklisted'`); got != "s1,s5" {
		t.Fatalf("blocklisted: %s", got)
	}
	e.wantLists(e.s2, "a skipped subscriber", "A1:confirmed", "B1:confirmed")
	r := last(auditSubBlocklist)
	if n, ok := skippedShared(t, r); !ok || n != 3 || r.Count == nil || *r.Count != 2 {
		t.Fatalf("audit of the blocklist: count %v, detail %s", r.Count, r.Detail)
	}
	if strings.Contains(string(r.Detail), "axm_lk") {
		t.Fatalf("the added condition is not the user's query: %s", r.Detail)
	}

	if code, _ := e.call(e.a.DeleteSubscribersByQuery, ua, "POST", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	if got := e.names(`TRUE`); got != "s2,s3,s4,s6" {
		t.Fatalf("left after the delete: %s", got)
	}
	r = last(auditSubDelete)
	if n, ok := skippedShared(t, r); !ok || n != 3 || *r.Count != 2 {
		t.Fatalf("audit of the delete: count %v, detail %s", r.Count, r.Detail)
	}

	// A search works the same way.
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, ua, "POST", "/x", "", fmt.Sprintf(`{"search":"s2","list_ids":[%d]}`, e.aList)); code != 200 {
		t.Fatal(code)
	}
	if !e.row(e.s2).Exists {
		t.Fatal("a search deleted a shared subscriber")
	}
	r = last(auditSubDelete)
	if n, _ := skippedShared(t, r); n != 1 || *r.Count != 0 {
		t.Fatalf("audit of the search: count %v, detail %s", r.Count, r.Detail)
	}

	// A list both teams own: its people who are only there are the member's to delete.
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, ua, "POST", "/x", "", fmt.Sprintf(`{"all":true,"list_ids":[%d]}`, e.bothList)); code != 200 {
		t.Fatal(code)
	}
	if e.row(e.s6).Exists {
		t.Fatal("s6 is on a list of the member only")
	}

	// An admin reaches everybody, and the row has no skipped number.
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, e.as(e.admin), "POST", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	if got := e.names(`TRUE`); got != "" {
		t.Fatalf("left after the admin's delete: %s", got)
	}
	r = last(auditSubDelete)
	if _, ok := skippedShared(t, r); ok || *r.Count != 3 {
		t.Fatalf("audit of the admin's delete: count %v, detail %s", r.Count, r.Detail)
	}
}

// The user's own SQL condition is joined with the added one, not replaced by it.
func TestBoundaryBulkByQueryWithSQL(t *testing.T) {
	e := newBoundaryEnv(t)
	e.db.MustExec(`UPDATE roles SET permissions = permissions || '{subscribers:sql_query}' WHERE id = $1`, e.memberRole)
	ua := e.as(e.ua)
	body := fmt.Sprintf(`{"query":"subscribers.name IN ('s1','s2') OR subscribers.name = 's3'","list_ids":[%d]}`, e.aList)
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, ua, "POST", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	if got := e.names(`TRUE`); got != "s2,s3,s4,s5,s6" {
		t.Fatalf("left: %s", got)
	}
}

// Changing the subscriptions on the member's own lists reaches a locked subscriber too.
func TestBoundaryOwnListChangesStayOpen(t *testing.T) {
	e := newBoundaryEnv(t)
	ua := e.as(e.ua)
	lists := func(action string, ids ...int) string {
		b, _ := json.Marshal(map[string]any{"ids": ids, "action": action, "target_list_ids": []int{e.aList}, "status": "confirmed"})
		return string(b)
	}
	if code, _ := e.call(e.a.ManageSubscriberLists, ua, "PUT", "/x", "", lists("unsubscribe", e.s2, e.s3)); code != 200 {
		t.Fatal(code)
	}
	e.wantLists(e.s2, "unsubscribe from the member's list", "A1:unsubscribed", "B1:confirmed")
	if code, _ := e.call(e.a.ManageSubscriberLists, ua, "PUT", "/x", "", lists("remove", e.s2)); code != 200 {
		t.Fatal(code)
	}
	e.wantLists(e.s2, "remove from the member's list", "B1:confirmed")
	e.wantLists(e.s3, "the other one", "A1:unsubscribed", "Plain:confirmed")

	// By query: the member's list as the source, a list both teams own as the target.
	body := fmt.Sprintf(`{"all":true,"list_ids":[%d],"action":"add","target_list_ids":[%d,%d],"status":"confirmed"}`, e.aList, e.bothList, e.bList)
	if code, _ := e.call(e.a.ManageSubscriberListsByQuery, ua, "PUT", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	e.wantLists(e.s4, "add by query", "A1:confirmed", "B1:unsubscribed", "Both:confirmed")
	body = fmt.Sprintf(`{"all":true,"list_ids":[%d],"action":"unsubscribe","target_list_ids":[%d,%d]}`, e.aList, e.aList, e.bList)
	if code, _ := e.call(e.a.ManageSubscriberListsByQuery, ua, "PUT", "/x", "", body); code != 200 {
		t.Fatal(code)
	}
	e.wantLists(e.s4, "unsubscribe by query", "A1:unsubscribed", "B1:unsubscribed", "Both:confirmed")
	if e.row(e.s4).Status != "enabled" {
		t.Fatal("the subscriber itself changed")
	}
}

// The other team, the people who manage everything, and roles that read all lists but manage
// only some.
func TestBoundaryOtherRoles(t *testing.T) {
	e := newBoundaryEnv(t)
	ub := e.as(e.ub)
	// s5 is on A1 (team 가) and on the shared list: locked for team 나.
	if code, _ := e.call(e.a.BlocklistSubscriber, ub, "PUT", "/x", itoa(e.s5), ""); code != http.StatusForbidden {
		t.Fatalf("the other team on s5: %d", code)
	}
	if code, _ := e.call(e.a.BlocklistSubscriber, ub, "PUT", "/x", itoa(e.s6), ""); code != 200 {
		t.Fatalf("the other team on s6: %d", code)
	}
	if code, _ := e.call(e.a.DeleteSubscriber, ub, "DELETE", "/x", itoa(e.s1), ""); code != http.StatusForbidden {
		t.Fatalf("a subscriber the other team cannot read: %d", code)
	}

	// An ordinary list role on A1 follows the same rule.
	plain := e.as(e.plain)
	if code, _ := e.call(e.a.DeleteSubscriber, plain, "DELETE", "/x", itoa(e.s5), ""); code != http.StatusForbidden {
		t.Fatalf("a list role with one list, on a subscriber of two: %d", code)
	}
	if code, _ := e.call(e.a.DeleteSubscriber, plain, "DELETE", "/x", itoa(e.s1), ""); code != 200 {
		t.Fatalf("a list role with one list, on its own subscriber: %d", code)
	}

	// A list the user may only read does not open writing.
	var readRole, reader int
	e.get(&readRole, `INSERT INTO roles(type,name) VALUES('list','Read A1') RETURNING id`)
	e.db.MustExec(`INSERT INTO roles(parent_id,list_id,type,permissions) VALUES($1,$2,'list','{list:get}')`, readRole, e.aList)
	e.get(&reader, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES('reader','reader@example.invalid','reader',$1,$2,'enabled','user') RETURNING id`, e.memberRole, readRole)
	only := e.person("only-a", e.aList, "confirmed")
	if code, data := e.call(e.a.GetSubscriber, e.as(reader), "GET", "/x", itoa(only), ""); code != 200 || !lockedIn(data) {
		t.Fatalf("a reader sees the subscriber, locked: %d %s", code, data)
	}
	if code, _ := e.call(e.a.DeleteSubscriber, e.as(reader), "DELETE", "/x", itoa(only), ""); code != http.StatusForbidden {
		t.Fatalf("a reader deleting: %d", code)
	}
	if code, _ := e.call(e.a.UpdateSubscriber, e.as(reader), "PUT", "/x", itoa(only), e.putBody(only, nil)); code != 200 {
		t.Fatalf("a reader saving the form unchanged: %d", code)
	}
	e.wantLists(only, "a reader has no list to take a subscriber off", "A1:confirmed")

	// lists:get_all without lists:manage_all (D5): reads everybody, changes nobody's row.
	var role, user int
	e.get(&role, `INSERT INTO roles(type,name,permissions) VALUES('user','reads-all','{lists:get_all,subscribers:get_all,subscribers:manage}') RETURNING id`)
	e.get(&user, `INSERT INTO users(username,email,name,user_role_id,status,type) VALUES('reads-all','reads-all@example.invalid','r',$1,'enabled','user') RETURNING id`, role)
	ra := e.as(user)
	if code, data := e.call(e.a.GetSubscriber, ra, "GET", "/x", itoa(only), ""); code != 200 || !lockedIn(data) {
		t.Fatalf("%d %s", code, data)
	}
	if code, _ := e.call(e.a.UpdateSubscriber, ra, "PUT", "/x", itoa(only), e.putBody(only, func(m map[string]any) { m["name"] = "x" }, e.aList)); code != http.StatusForbidden {
		t.Fatalf("get_all only, renaming: %d", code)
	}
	if code, _ := e.call(e.a.BlocklistSubscriber, ra, "PUT", "/x", itoa(only), ""); code != http.StatusForbidden {
		t.Fatalf("get_all only, blocklisting: %d", code)
	}
	if code, _ := e.call(e.a.UpdateSubscriber, ra, "PUT", "/x", itoa(e.s2), e.putBody(e.s2, nil)); code != 200 {
		t.Fatalf("get_all only, saving the form unchanged: %d", code)
	}
	e.wantLists(e.s2, "nobody's subscription is removed by a user who manages no list", "A1:confirmed", "B1:confirmed")
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, ra, "POST", "/x", "", `{"all":true}`); code != 200 {
		t.Fatal(code)
	}
	if got := e.names(`TRUE`); !strings.Contains(got, "s2") || !strings.Contains(got, "only-a") || !strings.Contains(got, "s6") {
		t.Fatalf("get_all only, delete all: left %s", got)
	}

	// Admin and Super Admin are not held back.
	if code, _ := e.call(e.a.BlocklistSubscriber, e.as(e.admin), "PUT", "/x", itoa(e.s2), ""); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if code, _ := e.call(e.a.DeleteSubscriber, e.as(e.superAdmin), "DELETE", "/x", itoa(e.s3), ""); code != 200 {
		t.Fatalf("super admin: %d", code)
	}
}

// A member who opens the subscribers without a list filter sees each person once.
func TestSubscriberListHasNoDuplicates(t *testing.T) {
	e := newBoundaryEnv(t)
	e.db.MustExec(`UPDATE roles SET permissions = permissions || '{subscribers:sql_query}' WHERE id = $1`, e.memberRole)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, e.s5, e.bothList)
	ua, admin := e.as(e.ua), e.as(e.admin)
	list := func(u auth.User, target string) (string, int) {
		t.Helper()
		code, data := e.call(e.a.QuerySubscribers, u, "GET", target, "", "")
		if code != 200 {
			t.Fatalf("%s: %d", target, code)
		}
		var page struct {
			Results []models.Subscriber `json:"results"`
			Total   int                 `json:"total"`
		}
		json.Unmarshal(data, &page)
		names := []string{}
		for _, s := range page.Results {
			names = append(names, s.Name)
		}
		return strings.Join(names, ","), page.Total
	}
	want := func(u auth.User, target, names string) {
		t.Helper()
		got, total := list(u, target)
		if got != names || total != len(strings.Split(names, ",")) {
			t.Fatalf("%s: %s (total %d), want %s", target, got, total, names)
		}
	}
	both := fmt.Sprintf("list_id=%d&list_id=%d", e.aList, e.bothList)

	// s5 is on A1 and on Both: once, and counted once.
	want(ua, "/api/subscribers?order_by=name&order=asc", "s1,s2,s3,s4,s5,s6")
	want(ua, "/api/subscribers?order_by=name&order=asc&"+both, "s1,s2,s3,s4,s5,s6")
	want(ua, "/api/subscribers?order_by=name&order=desc&search=s5", "s5")
	// The paging is over people.
	want2 := func(target, names string, total int) {
		t.Helper()
		if got, n := list(ua, target); got != names || n != total {
			t.Fatalf("%s: %s (total %d)", target, got, n)
		}
	}
	want2("/api/subscribers?order_by=name&order=asc&per_page=4&page=2", "s5,s6", 6)
	// The subscription status reads the member's lists only.
	want(ua, "/api/subscribers?order_by=name&order=asc&subscription_status=unsubscribed", "s5")
	want(ua, "/api/subscribers?order_by=name&order=asc&subscription_status=confirmed", "s1,s2,s3,s4,s5,s6")
	// An SQL condition on subscriber_lists still reads the joined row.
	q := "&query=" + strings.ReplaceAll("subscriber_lists.status = 'unsubscribed'", " ", "%20")
	want(ua, "/api/subscribers?order_by=name&order=asc"+q, "s5")
	want(ua, "/api/subscribers?order_by=name&order=asc&query=subscribers.name%20IN%20('s5','s6')", "s5,s6")
	// A sort by an attribute.
	e.db.MustExec(`UPDATE subscribers SET attribs = jsonb_build_object('n', id)`)
	want(ua, "/api/subscribers?order=desc&order_by=attribs.n", "s6,s5,s4,s3,s2,s1")

	// Admin (no list IDs) and one chosen list use the upstream query: same people as before.
	want(admin, "/api/subscribers?order_by=name&order=asc", "s1,s2,s3,s4,s5,s6")
	want(ua, fmt.Sprintf("/api/subscribers?order_by=name&order=asc&list_id=%d", e.bothList), "s5,s6")
	want(admin, fmt.Sprintf("/api/subscribers?order_by=name&order=asc&list_id=%d", e.bList), "s2,s4")
	// An admin choosing two lists gets each person once too.
	want(admin, "/api/subscribers?order_by=name&order=asc&"+both, "s1,s2,s3,s4,s5,s6")
}

// importAs runs an import as the user and waits for it to end.
func (e *teamsEnv) importAs(u auth.User, params, csv string) int {
	e.t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	w.WriteField("params", params)
	f, _ := w.CreateFormFile("file", "people.csv")
	f.Write([]byte(csv))
	w.Close()
	req := httptest.NewRequest("POST", "/api/import/subscribers", body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, u)
	if err := e.a.ImportSubscribers(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he.Code
		}
		e.t.Fatal(err)
	}
	waitFor(e.t, "the import to end", func() bool {
		s := e.a.importer.GetStats().Status
		return s == subimporter.StatusFinished || s == subimporter.StatusFailed
	})
	if s := e.a.importer.GetStats().Status; s != subimporter.StatusFinished {
		e.t.Fatalf("import %s: %s", s, e.a.importer.GetLogs())
	}
	e.a.importer.Stop()
	time.Sleep(50 * time.Millisecond)
	return rec.Code
}

// A member's import never rewrites the name and attributes of somebody who already exists; an
// admin's does when asked to.
func TestBoundaryImportKeepsExistingProfiles(t *testing.T) {
	e := newBoundaryEnv(t)
	e.a.importer = subimporter.New(subimporter.Options{
		UpsertStmt:         e.q.UpsertSubscriber.Stmt,
		BlocklistStmt:      e.q.UpsertBlocklistSubscriber.Stmt,
		UpdateListDateStmt: e.q.UpdateListsDate.Stmt,
		PostCB:             func(string, any) error { return nil },
	}, e.db.DB, e.a.i18n)
	onB := e.person("onb", e.bList, "confirmed")
	csv := "email,name,attributes\n" +
		"s2@example.invalid,Changed Two,\"{\"\"city\"\":\"\"Busan\"\"}\"\n" +
		"s1@example.invalid,Changed One,\"{\"\"city\"\":\"\"Busan\"\"}\"\n" +
		"onb@example.invalid,Changed B,\"{\"\"city\"\":\"\"Busan\"\"}\"\n" +
		"new@example.invalid,New Person,\"{\"\"city\"\":\"\"Busan\"\"}\"\n"
	params := func(extra string) string {
		return fmt.Sprintf(`{"mode":"subscribe","subscription_status":"confirmed","delim":",","lists":[%d,%d]%s}`, e.aList, e.bList, extra)
	}
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, e.s1, e.aList)

	for _, extra := range []string{`,"overwrite":true`, `,"overwrite_userinfo":true,"overwrite_subscription_status":true`} {
		if code := e.importAs(e.as(e.ua), params(extra), csv); code != 200 {
			t.Fatalf("import %s: %d", extra, code)
		}
		for _, id := range []int{e.s1, e.s2, onB} {
			if r := e.row(id); strings.HasPrefix(r.Name, "Changed") || !strings.Contains(r.Attribs, "Seoul") {
				t.Fatalf("a member's import (%s) rewrote %d: %+v", extra, id, r)
			}
		}
		// The member's own list takes the people in; the other team's list is not theirs to fill.
		e.wantLists(e.s2, "s2", "A1:confirmed", "B1:confirmed")
		e.wantLists(onB, "a subscriber of the other team joins the member's list", "A1:confirmed", "B1:confirmed")
		// "Overwrite subscription status" still works on the member's list.
		e.wantLists(e.s1, "s1", "A1:confirmed")
		var fresh subRow
		var id int
		e.get(&id, `SELECT id FROM subscribers WHERE email = 'new@example.invalid'`)
		if fresh = e.row(id); fresh.Name != "New Person" || !strings.Contains(fresh.Attribs, "Busan") {
			t.Fatalf("a new subscriber keeps what the file says: %+v", fresh)
		}
		e.wantLists(id, "the new subscriber", "A1:confirmed")
		e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, e.s1, e.aList)
	}
	rows := e.audits("action = $1", auditImportStart)
	if len(rows) != 2 || !strings.Contains(string(rows[1].Detail), `"overwrite_userinfo": false`) ||
		!strings.Contains(string(rows[1].Detail), `"overwrite_subscription_status": true`) {
		t.Fatalf("the audit row says what the import did: %+v", rows)
	}

	// An admin overwrites when asked to.
	if code := e.importAs(e.as(e.admin), params(`,"overwrite":true`), csv); code != 200 {
		t.Fatal(code)
	}
	if r := e.row(e.s2); r.Name != "Changed Two" || !strings.Contains(r.Attribs, "Busan") {
		t.Fatalf("an admin's import: %+v", r)
	}
}

// restrictImportOverwrite takes the old "overwrite" flag apart.
func TestRestrictImportOverwrite(t *testing.T) {
	member := auth.User{}
	member.UserRoleID = 5
	admin := auth.User{PermissionsMap: map[string]struct{}{auth.PermListManageAll: {}}}
	admin.UserRoleID = 5
	sa := auth.User{}
	sa.UserRoleID = auth.SuperAdminRoleID
	for _, c := range []struct {
		u                    auth.User
		in                   subimporter.SessionOpt
		all, info, subStatus bool
	}{
		{member, subimporter.SessionOpt{Overwrite: true}, false, false, true},
		{member, subimporter.SessionOpt{OverwriteUserInfo: true}, false, false, false},
		{member, subimporter.SessionOpt{OverwriteUserInfo: true, OverwriteSubStatus: true}, false, false, true},
		{member, subimporter.SessionOpt{}, false, false, false},
		{admin, subimporter.SessionOpt{Overwrite: true}, true, false, false},
		{admin, subimporter.SessionOpt{OverwriteUserInfo: true}, false, true, false},
		{sa, subimporter.SessionOpt{Overwrite: true, OverwriteUserInfo: true}, true, true, false},
	} {
		opt := c.in
		restrictImportOverwrite(c.u, &opt)
		if opt.Overwrite != c.all || opt.OverwriteUserInfo != c.info || opt.OverwriteSubStatus != c.subStatus {
			t.Fatalf("%+v: %+v", c.in, opt)
		}
	}
}

// profileChanged reads a save the way the update query does.
func TestProfileChanged(t *testing.T) {
	cur := subProfile{Email: "a@example.invalid", Name: "A", Status: "enabled", Attribs: models.JSON{"city": "Seoul", "n": float64(3), "tags": []any{"x"}}}
	same := func() models.Subscriber {
		return models.Subscriber{Email: "a@example.invalid", Name: "A", Status: "enabled", Attribs: models.JSON{"n": 3, "tags": []string{"x"}, "city": "Seoul"}}
	}
	if profileChanged(cur, same()) {
		t.Fatal("the same profile")
	}
	for what, change := range map[string]func(s *models.Subscriber){
		"email":          func(s *models.Subscriber) { s.Email = "b@example.invalid" },
		"name":           func(s *models.Subscriber) { s.Name = "B" },
		"status":         func(s *models.Subscriber) { s.Status = "blocklisted" },
		"attribute":      func(s *models.Subscriber) { s.Attribs["city"] = "Busan" },
		"new attribute":  func(s *models.Subscriber) { s.Attribs["x"] = nil },
		"no attributes":  func(s *models.Subscriber) { s.Attribs = nil },
		"attribute type": func(s *models.Subscriber) { s.Attribs["n"] = "3" },
	} {
		s := same()
		change(&s)
		if !profileChanged(cur, s) {
			t.Fatalf("%s is a change", what)
		}
	}
	for what, change := range map[string]func(s *models.Subscriber){
		"empty e-mail":   func(s *models.Subscriber) { s.Email = "" },
		"empty name":     func(s *models.Subscriber) { s.Name = "" },
		"empty status":   func(s *models.Subscriber) { s.Status = "" },
		"e-mail letters": func(s *models.Subscriber) { s.Email = "A@Example.invalid" },
		"name spaces":    func(s *models.Subscriber) { s.Name = "  A " },
	} {
		s := same()
		change(&s)
		if profileChanged(cur, s) {
			t.Fatalf("%s keeps the profile", what)
		}
	}
	if profileChanged(subProfile{Email: "a@example.invalid", Attribs: models.JSON{}}, models.Subscriber{Email: "a@example.invalid"}) {
		t.Fatal("no attributes on both sides")
	}
	// The copy does not follow the subscriber's map.
	sub := models.Subscriber{Attribs: models.JSON{"k": "v"}}
	p := profileOf(sub)
	sub.Attribs["k"] = "changed"
	if p.Attribs["k"] != "v" {
		t.Fatal("profileOf shares the map")
	}
	if got := ownListsOnly(false, nil); len(got) != 1 || got[0] != -1 {
		t.Fatalf("%v", got)
	}
	if got := ownListsOnly(true, []int{3}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := ownListsOnly(false, []int{3}); len(got) != 1 || got[0] != 3 {
		t.Fatalf("%v", got)
	}
}
