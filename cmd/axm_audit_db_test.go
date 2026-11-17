package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// ctx is a request context of a signed-in user, for the helpers that take one.
func (e *teamsEnv) ctx(user auth.User) echo.Context {
	c := echo.New().NewContext(httptest.NewRequest("GET", "/", nil), httptest.NewRecorder())
	c.Set(auth.UserHTTPCtxKey, user)
	return c
}

// LST-27: who did what is written to axm_audit_log, without subscriber e-mail addresses.

func TestScrubAuditText(t *testing.T) {
	for in, want := range map[string]string{
		"alice@example.com":                       "[email]",
		"email = 'bob@example.com' OR name = 'x'": "email = '[email]' OR name = 'x'",
		"subscribers.email LIKE '%@example.com'":  "subscribers.email LIKE '[email]'",
		"cho@a.kr, kim@b.kr":                      "[email], [email]",
		"@":                                       "(at)",
		"plain search":                            "plain search",
	} {
		if got := scrubAuditText(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("가", 600)
	if got := scrubAuditText(long); len([]rune(got)) != auditMaxText+1 {
		t.Errorf("long text is %d characters", len([]rune(got)))
	}
	d := scrubAuditDetail(map[string]any{"q": "a@b.c", "n": 3, "l": []string{"x@y.z"}, "m": map[string]any{"k": "p@q.r"}})
	if b, _ := json.Marshal(d); strings.Contains(string(b), "@") {
		t.Errorf("an @ is left: %s", b)
	}
}

func TestChangedSettingKeys(t *testing.T) {
	a := models.Settings{AppRootURL: "https://a.example", AppLang: "en"}
	b := a
	b.AppRootURL = "https://b.example"
	b.SMTP = nil
	keys := changedSettingKeys(a, b)
	if len(keys) != 1 || keys[0] != "app.root_url" {
		t.Fatalf("keys: %v", keys)
	}
	if got := changedSettingKeys(a, a); len(got) != 0 {
		t.Fatalf("no change: %v", got)
	}
}

// Lists: create, save (rename and team change), delete, restore; a refused request writes nothing.
func TestAuditListActions(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)

	code, data := e.call(e.guarded(e.a.CreateList), admin, "POST", "/api/lists", "", `{"name":"Fresh","tags":["팀:가"]}`)
	if code != 200 {
		t.Fatal(code)
	}
	var l models.List
	json.Unmarshal(data, &l)
	r := e.oneAudit(auditListCreate)
	if r.Username != "admin" || r.UserType != "user" || r.TargetType != "list" || *r.TargetID != l.ID || r.TargetName != "Fresh" || r.UserID == nil || *r.UserID != e.admin {
		t.Fatalf("create row: %+v", r)
	}

	code, _ = e.call(e.a.UpdateList, admin, "PUT", "/api/lists/x", itoa(l.ID), `{"name":"Renamed","type":"private","optin":"single","tags":["팀:나"]}`)
	if code != 200 {
		t.Fatal(code)
	}
	up := e.oneAudit(auditListUpdate)
	var d struct {
		Changed map[string]map[string]string `json:"changed"`
	}
	json.Unmarshal(up.Detail, &d)
	if d.Changed["name"]["from"] != "Fresh" || d.Changed["name"]["to"] != "Renamed" || up.TargetName != "Renamed" {
		t.Fatalf("update row: %s", up.Detail)
	}
	tc := e.oneAudit(auditListTeamChange)
	var td struct{ From, To []string }
	json.Unmarshal(tc.Detail, &td)
	if len(td.From) != 1 || td.From[0] != "가" || len(td.To) != 1 || td.To[0] != "나" {
		t.Fatalf("team change row: %s", tc.Detail)
	}

	// A save that changes nothing writes nothing.
	n := len(e.audits(""))
	e.call(e.a.UpdateList, admin, "PUT", "/api/lists/x", itoa(l.ID), `{"name":"Renamed","type":"private","optin":"single","tags":["팀:나"]}`)
	if len(e.audits("")) != n {
		t.Fatal("an unchanged save was written")
	}

	// A team member cannot touch another team's list: no row.
	n = len(e.audits(""))
	if code, _ := e.call(e.a.DeleteList, e.as(e.ua), "DELETE", "/api/lists/x", itoa(e.bList), ""); code != http.StatusForbidden {
		t.Fatalf("%d", code)
	}
	if code, _ := e.call(e.a.UpdateList, e.as(e.ua), "PUT", "/api/lists/x", itoa(e.bList), `{"name":"x"}`); code != http.StatusForbidden {
		t.Fatalf("%d", code)
	}
	if len(e.audits("")) != n {
		t.Fatal("a refused request was written")
	}

	e.call(e.a.DeleteList, admin, "DELETE", "/api/lists/x", itoa(l.ID), "")
	e.restore(l.ID, e.admin)
	for _, a := range []string{auditListDelete, auditListRestore} {
		if r := e.oneAudit(a); r.Username != "admin" || *r.TargetID != l.ID {
			t.Fatalf("%s row: %+v", a, r)
		}
	}
}

// Subscribers: by ID, by query and in bulk. A search or SQL condition that holds an e-mail
// address never reaches the log.
func TestAuditSubscribersHaveNoAddresses(t *testing.T) {
	e := newAuditEnv(t)
	e.db.MustExec(`UPDATE roles SET permissions = permissions || '{subscribers:sql_query}' WHERE id = 1`)
	sa := e.as(e.superAdmin)

	var ids []int
	for i := 0; i < 6; i++ {
		ids = append(ids, e.addSub("enabled", "confirmed"))
	}
	e.db.MustExec(`UPDATE subscribers SET email = 'alice@example.invalid' WHERE id = $1`, ids[0])
	e.db.MustExec(`UPDATE subscribers SET email = 'bob.kim@example.invalid', name = 'Bob Kim' WHERE id = $1`, ids[1])

	// By ID: one is a target, several are a count.
	if code, _ := e.call(e.a.DeleteSubscriber, sa, "DELETE", "/api/subscribers/x", itoa(ids[5]), ""); code != 200 {
		t.Fatal(code)
	}
	one := e.oneAudit(auditSubDelete)
	if one.TargetType != "subscriber" || one.TargetID == nil || *one.TargetID != ids[5] || one.Count != nil || one.TargetName != "" {
		t.Fatalf("one subscriber: %+v", one)
	}
	q := url.Values{"id": {itoa(ids[3]), itoa(ids[4])}}
	if code, _ := e.call(e.a.DeleteSubscribers, sa, "DELETE", "/api/subscribers?"+q.Encode(), "", ""); code != 200 {
		t.Fatal(code)
	}
	if rows := e.audits("action = 'subscriber.delete' AND count = 2"); len(rows) != 1 {
		t.Fatalf("several: %+v", e.audits(""))
	}

	// Blocklist by ID and through a list change.
	if code, _ := e.call(e.a.BlocklistSubscriber, sa, "PUT", "/api/subscribers/x/blocklist", itoa(ids[2]), ""); code != 200 {
		t.Fatal(code)
	}
	if r := e.oneAudit(auditSubBlocklist); *r.TargetID != ids[2] {
		t.Fatalf("blocklist: %+v", r)
	}
	body := fmt.Sprintf(`{"ids":[%d],"action":"remove","target_list_ids":[%d]}`, ids[0], e.listID)
	if code, _ := e.call(e.a.ManageSubscriberLists, sa, "PUT", "/api/subscribers/lists", "", body); code != 200 {
		t.Fatal(code)
	}
	if r := e.oneAudit(auditSubListsRemove); *r.TargetID != ids[0] || !strings.Contains(string(r.Detail), "Synthetic") {
		t.Fatalf("remove from list: %+v %s", r, r.Detail)
	}

	// By query: the search and the SQL condition carry addresses.
	body = fmt.Sprintf(`{"search":"alice@example.invalid","query":"subscribers.email = 'bob.kim@example.invalid' OR subscribers.email LIKE '%%@example.invalid'","list_ids":[%d]}`, e.listID)
	if code, _ := e.call(e.a.BlocklistSubscribersByQuery, sa, "PUT", "/api/subscribers/query/blocklist", "", body); code != 200 {
		t.Fatalf("by query: %d", code)
	}
	rows := e.audits("action = 'subscriber.blocklist' AND detail->>'by' = 'query'")
	if len(rows) != 1 || rows[0].Count == nil {
		t.Fatalf("by query row: %+v", rows)
	}
	var det map[string]any
	json.Unmarshal(rows[0].Detail, &det)
	if det["counted"] != "before" || det["query"] == nil || det["search"] == nil {
		t.Fatalf("by query detail: %v", det)
	}
	body = fmt.Sprintf(`{"query":"subscribers.email LIKE 'alice@%%'","list_ids":[%d]}`, e.listID)
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, sa, "POST", "/api/subscribers/query/delete", "", body); code != 200 {
		t.Fatalf("delete by query: %d", code)
	}
	body = fmt.Sprintf(`{"all":true,"list_ids":[%d]}`, e.listID)
	if code, _ := e.call(e.a.DeleteSubscribersByQuery, sa, "POST", "/api/subscribers/query/delete", "", body); code != 200 {
		t.Fatalf("delete all: %d", code)
	}

	// Nothing in the log holds an @ or a subscriber's name.
	var bad int
	e.get(&bad, `SELECT COUNT(*) FROM axm_audit_log WHERE detail::TEXT LIKE '%@%' OR target_name LIKE '%@%'
		OR username LIKE '%@%' OR detail::TEXT LIKE '%Bob Kim%' OR detail::TEXT LIKE '%alice%' OR detail::TEXT LIKE '%bob.kim%'`)
	if bad != 0 {
		var dump []string
		e.db.Select(&dump, `SELECT action || ' ' || detail::TEXT FROM axm_audit_log`)
		t.Fatalf("%d rows hold an address or a name: %v", bad, dump)
	}
}

// The audit screen's API: filters and the page counts.
func TestAuditQueryFilters(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	insert := func(user int, name, action, target string, ago time.Duration) {
		e.db.MustExec(`INSERT INTO axm_audit_log (created_at, user_id, username, user_name, user_type, action, target_type, target_name)
			VALUES (NOW() - $1 * INTERVAL '1 second', $2, $3, $3, 'user', $4, 'x', $5)`, int(ago.Seconds()), user, name, action, target)
	}
	insert(e.admin, "admin", "list.delete", "Alpha 리스트", 3*24*time.Hour)
	insert(e.admin, "admin", "campaign.start", "Beta", 2*24*time.Hour)
	insert(e.ua, "ua", "list.update", "alpha_2", 24*time.Hour)
	insert(e.ua, "ua", "user.update", "ub", time.Hour)
	insert(e.ua, "ua", "settings.update", "settings", time.Minute)

	get := func(q string) (int, []string) {
		code, data := e.call(e.a.GetAudit, admin, "GET", "/api/audit?"+q, "", "")
		if code != 200 {
			t.Fatalf("%s: %d", q, code)
		}
		var page struct {
			Total   int                 `json:"total"`
			Results []models.AuditEntry `json:"results"`
		}
		json.Unmarshal(data, &page)
		var out []string
		for _, r := range page.Results {
			out = append(out, r.Action)
		}
		return page.Total, out
	}
	if n, got := get(""); n != 5 || got[0] != "settings.update" || got[4] != "list.delete" {
		t.Fatalf("all, newest first: %d %v", n, got)
	}
	if n, got := get("user_id=" + itoa(e.ua)); n != 3 || len(got) != 3 {
		t.Fatalf("by user: %d %v", n, got)
	}
	if n, _ := get("area=list"); n != 2 {
		t.Fatalf("area list: %d", n)
	}
	if n, _ := get("area=access&area=settings"); n != 2 {
		t.Fatalf("area access+settings: %d", n)
	}
	if n, got := get("action=campaign.start&action=list.update"); n != 2 || len(got) != 2 {
		t.Fatalf("actions: %d %v", n, got)
	}
	if n, _ := get("query=ALPHA"); n != 2 {
		t.Fatalf("name search: %d", n)
	}
	if n, _ := get("query=" + url.QueryEscape("alpha_")); n != 1 {
		t.Fatalf("an underscore is plain text: %d", n)
	}
	from := time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	if n, _ := get("from=" + url.QueryEscape(from)); n != 3 {
		t.Fatalf("from: %d", n)
	}
	to := time.Now().Add(-49 * time.Hour).UTC().Format(time.RFC3339)
	if n, _ := get("to=" + url.QueryEscape(to)); n != 1 {
		t.Fatalf("to: %d", n)
	}
	if n, got := get("per_page=2&page=2"); n != 5 || len(got) != 2 {
		t.Fatalf("paging: %d %v", n, got)
	}
	if code, _ := e.call(e.a.GetAudit, admin, "GET", "/api/audit?area=nope", "", ""); code != http.StatusBadRequest {
		t.Fatalf("a bad area: %d", code)
	}
	code, data := e.call(e.a.GetAuditUsers, admin, "GET", "/api/audit/users", "", "")
	var users []models.AuditUser
	json.Unmarshal(data, &users)
	if code != 200 || len(users) != 2 {
		t.Fatalf("users: %d %v", code, users)
	}
}

// Roles, teams and users: what changed is named, never an e-mail or a password.
func TestAuditAccessChanges(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)

	code, data := e.call(e.a.CreateUserRole, admin, "POST", "/api/roles/users", "", `{"name":"Reader","permissions":["subscribers:get"]}`)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	var role struct{ ID int }
	json.Unmarshal(data, &role)
	if code, _ := e.call(e.a.UpdateUserRole, admin, "PUT", "/api/roles/users/x", itoa(role.ID), `{"name":"Reader","permissions":["subscribers:get","campaigns:get"]}`); code != 200 {
		t.Fatal(code)
	}
	up := e.oneAudit(auditRoleUpdate)
	if !strings.Contains(string(up.Detail), `"permissions_added": ["campaigns:get"]`) && !strings.Contains(string(up.Detail), `"permissions_added":["campaigns:get"]`) {
		t.Fatalf("role update: %s", up.Detail)
	}
	if code, _ := e.call(e.a.DeleteRole, admin, "DELETE", "/api/roles/x", itoa(role.ID), ""); code != 200 {
		t.Fatal(code)
	}
	if r := e.oneAudit(auditRoleDelete); r.TargetName != "Reader" {
		t.Fatalf("role delete: %+v", r)
	}

	// Users: create, change the e-mail and the password, delete.
	body := `{"username":"newbie","type":"user","name":"New","email":"newbie@example.invalid","user_role_id":` + itoa(e.memberRole) + `,"status":"enabled","password_login":true,"password":"longpassword1"}`
	code, data = e.call(e.a.CreateUser, admin, "POST", "/api/users", "", body)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	var u struct{ ID int }
	json.Unmarshal(data, &u)
	body = `{"username":"newbie","type":"user","name":"New","email":"other@example.invalid","user_role_id":` + itoa(e.memberRole) + `,"status":"disabled","password_login":true,"password":"anotherlongpass"}`
	if code, data := e.call(e.a.UpdateUser, admin, "PUT", "/api/users/x", itoa(u.ID), body); code != 200 {
		t.Fatal(code, string(data))
	}
	uu := e.oneAudit(auditUserUpdate)
	var ud map[string]any
	json.Unmarshal(uu.Detail, &ud)
	if ud["email_changed"] != true || ud["password_changed"] != true || ud["status"] == nil {
		t.Fatalf("user update: %s", uu.Detail)
	}
	e.call(e.a.DeleteUser, admin, "DELETE", "/api/users/x", itoa(u.ID), "")
	if r := e.oneAudit(auditUserDelete); r.TargetName != "newbie" {
		t.Fatalf("user delete: %+v", r)
	}

	// Teams.
	if code, _ := e.call(e.a.SetTeamMembers, admin, "PUT", "/api/teams/x/members", itoa(e.team1), fmt.Sprintf(`{"user_ids":[%d,%d]}`, e.ua, e.plain)); code != 200 {
		t.Fatal(code)
	}
	tm := e.oneAudit(auditTeamMembers)
	if !strings.Contains(string(tm.Detail), itoa(e.plain)) || tm.TargetName != "가" {
		t.Fatalf("team members: %s", tm.Detail)
	}

	var bad int
	e.get(&bad, `SELECT COUNT(*) FROM axm_audit_log WHERE detail::TEXT ~ '@|longpassword|anotherlongpass'`)
	if bad != 0 {
		t.Fatal("an address or a password is in the log")
	}
}

// Campaigns: a status change names the campaign, its lists and the number it goes to;
// deleting writes one row for each campaign. An import is written at its start and its end,
// as an action of the person who started it.
func TestAuditCampaignsAndImports(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	c := e.campID

	camp, err := e.core.GetCampaign(c, "", "")
	if err != nil {
		t.Fatal(err)
	}
	camp.ToSend = 41
	for status, action := range map[string]string{
		models.CampaignStatusRunning: auditCampaignStart, models.CampaignStatusPaused: auditCampaignPause,
		models.CampaignStatusCancelled: auditCampaignCancel, models.CampaignStatusScheduled: auditCampaignSchedule,
		models.CampaignStatusDraft: auditCampaignUnschedule,
	} {
		ctx := e.ctx(admin)
		e.a.auditCampaignStatus(ctx, status, camp)
		r := e.oneAudit(action)
		if r.TargetType != "campaign" || *r.TargetID != c || r.Count == nil || *r.Count != 41 || !strings.Contains(string(r.Detail), "Synthetic") {
			t.Fatalf("%s: %+v %s", action, r, r.Detail)
		}
	}

	second, _ := e.addCampaign("draft")
	if code, _ := e.call(e.a.DeleteCampaign, admin, "DELETE", "/x", itoa(second), ""); code != 200 {
		t.Fatal(code)
	}
	if r := e.oneAudit(auditCampaignDelete); *r.TargetID != second || !strings.HasPrefix(r.TargetName, "Synthetic") {
		t.Fatalf("campaign delete: %+v", r)
	}

	// An import: start by a user, finish by the importer's callback.
	e.a.importStarted(e.ctx(admin), subimporter.SessionOpt{Mode: "subscribe", SubStatus: "confirmed", ListIDs: []int{e.listID}, Filename: "people-list.csv"})
	e.a.auditImportFinish(struct {
		Name, Status    string
		Imported, Total int
	}{"people-list.csv", "finished", 1200, 1210})
	st, fin := e.oneAudit(auditImportStart), e.oneAudit(auditImportFinish)
	if st.Username != "admin" || fin.Username != "admin" || fin.Count == nil || *fin.Count != 1200 {
		t.Fatalf("import rows: %+v %+v", st, fin)
	}
	if strings.Contains(string(st.Detail), "people-list") || strings.Contains(string(fin.Detail), "people-list") {
		t.Fatal("the file name is in the log")
	}
}
