package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/knadh/paginator"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// teamsEnv is a database with two teams, a few lists and people, and the real handlers.
type teamsEnv struct {
	*sendsEnv
	a *App

	team1, team2                      int // team role IDs
	aList, bList, bothList, plainList int
	memberRole                        int
	admin, ua, ub, plain, superAdmin  int // user IDs

	// lastImport is the importer's status when the last importAs import had finished.
	lastImport  subimporter.Status
	lastRule    string
	lastSuggest string // the X-Axm-Suggest header of the last refused save
}

func newTeamsEnv(t *testing.T) *teamsEnv {
	e := &teamsEnv{sendsEnv: newSendsEnv(t, 0)}
	e.a = &App{core: e.core, i18n: &i18n.I18n{}, auth: &auth.Auth{}, pg: paginator.New(paginator.Opt{DefaultPerPage: 20, MaxPerPage: 50, NumPageNums: 10, PageParam: "page", PerPageParam: "per_page", AllowAll: true})}
	e.db.MustExec(`INSERT INTO roles(id,type,name,permissions) VALUES(1,'user','Super Admin','{lists:get_all,lists:manage_all,subscribers:get_all,subscribers:manage,campaigns:get_all,roles:get,roles:manage}')`)
	e.db.MustExec(`SELECT setval(pg_get_serial_sequence('roles','id'), 1)`)
	role := func(name string, perms ...string) int {
		var id int
		e.get(&id, `INSERT INTO roles(type,name,permissions) VALUES('user',$1,$2) RETURNING id`, name, pq.StringArray(perms))
		return id
	}
	user := func(name string, userRole int, listRole any) int {
		var id int
		e.get(&id, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES($1,$1||'@example.invalid',$1,$2,$3,'enabled','user') RETURNING id`, name, userRole, listRole)
		return id
	}
	e.memberRole = role("member", "subscribers:get", "subscribers:manage", "campaigns:get", "campaigns:manage")
	operator := role("operator", "lists:get_all", "lists:manage_all", "subscribers:get_all", "subscribers:manage", "campaigns:get_all", "roles:get", "roles:manage")

	// Lists: one per team, one shared by both, one without a team.
	mk := func(name string, tags ...string) int {
		var id int
		e.get(&id, `INSERT INTO lists(uuid,name,type,optin,tags) VALUES(gen_random_uuid(),$1,'private','single',$2) RETURNING id`, name, pq.StringArray(tags))
		return id
	}
	e.aList, e.bList = mk("A1", "팀:가"), mk("B1", "팀:나")
	e.bothList, e.plainList = mk("Both", "팀:가", "팀:나"), mk("Plain")
	t1, err := e.core.CreateTeam("가", e.memberRole)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := e.core.CreateTeam("나", e.memberRole)
	if err != nil {
		t.Fatal(err)
	}
	e.team1, e.team2 = t1.ID, t2.ID
	e.ua, e.ub = user("ua", e.memberRole, e.team1), user("ub", e.memberRole, e.team2)
	e.admin, e.superAdmin = user("admin", operator, nil), user("sa", 1, nil)
	// An ordinary list role with one list: not a team member.
	var plainRole int
	e.get(&plainRole, `INSERT INTO roles(type,name) VALUES('list','Manual') RETURNING id`)
	e.db.MustExec(`INSERT INTO roles(parent_id,list_id,type,permissions) VALUES($1,$2,'list','{list:get,list:manage}')`, plainRole, e.aList)
	e.plain = user("plain", e.memberRole, plainRole)
	return e
}

func (e *teamsEnv) as(id int) auth.User {
	e.t.Helper()
	u, err := e.core.GetUser(id, "", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// call runs a handler as a user and returns the status and the raw "data" of the answer.
func (e *teamsEnv) call(h echo.HandlerFunc, user auth.User, method, target, id, body string) (int, json.RawMessage) {
	e.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, user)
	if id != "" {
		c.Set("id", atoi(id))
	}
	if err := h(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			e.t.Logf("%s %s: %d %v", method, target, he.Code, he.Message)
			return he.Code, nil
		}
		if err == auth.ErrPermDenied {
			return http.StatusForbidden, nil
		}
		e.t.Fatal(err)
	}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Data
}

// guarded runs a handler behind the same guard the route has.
func (e *teamsEnv) guarded(h echo.HandlerFunc) echo.HandlerFunc { return e.a.listMaker(h) }

func (e *teamsEnv) listNames(user auth.User, target string) []string {
	e.t.Helper()
	code, data := e.call(e.a.GetLists, user, "GET", target, "", "")
	if code != 200 {
		e.t.Fatalf("%s: %d", target, code)
	}
	var page struct {
		Results []models.List `json:"results"`
	}
	json.Unmarshal(data, &page)
	out := []string{}
	for _, l := range page.Results {
		if l.Name == "Synthetic" {
			continue
		}
		out = append(out, l.Name)
	}
	return out
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func (e *teamsEnv) tagsOf(id int) []string {
	var tags pq.StringArray
	e.get(&tags, `SELECT tags FROM lists WHERE id=$1`, id)
	return tags
}

// A member sees the lists of the team only; an admin sees all, and team=none shows the lists
// without a team.
func TestTeamMemberSeesOwnLists(t *testing.T) {
	e := newTeamsEnv(t)
	ua, ub, admin := e.as(e.ua), e.as(e.ub), e.as(e.admin)
	if got := e.listNames(ua, "/api/lists"); !sameSet(got, []string{"A1", "Both"}) {
		t.Fatalf("team 가: %v", got)
	}
	if got := e.listNames(ub, "/api/lists"); !sameSet(got, []string{"B1", "Both"}) {
		t.Fatalf("team 나: %v", got)
	}
	if got := e.listNames(ua, "/api/lists?minimal=true"); !sameSet(got, []string{"A1", "Both"}) {
		t.Fatalf("minimal: %v", got)
	}
	if got := e.listNames(admin, "/api/lists"); !sameSet(got, []string{"A1", "B1", "Both", "Plain"}) {
		t.Fatalf("admin: %v", got)
	}
	if got := e.listNames(admin, "/api/lists?team=none"); !sameSet(got, []string{"Plain"}) {
		t.Fatalf("team=none: %v", got)
	}
	if got := e.listNames(ua, "/api/lists?team=none"); len(got) != 0 {
		t.Fatalf("a member has nothing without a team: %v", got)
	}
	if got := e.listNames(admin, "/api/lists?tag="+"팀:가"); !sameSet(got, []string{"A1", "Both"}) {
		t.Fatalf("tag filter: %v", got)
	}
	if code, _ := e.call(e.a.GetList, ua, "GET", "/api/lists/x", itoa(e.bList), ""); code != http.StatusForbidden {
		t.Fatalf("the other team's list: %d", code)
	}
	if code, _ := e.call(e.a.GetList, ua, "GET", "/api/lists/x", itoa(e.aList), ""); code != 200 {
		t.Fatalf("own list: %d", code)
	}
}

func TestTeamListsTeamsEndpoint(t *testing.T) {
	e := newTeamsEnv(t)
	code, data := e.call(e.a.GetListTeams, e.as(e.ua), "GET", "/api/lists/teams", "", "")
	if code != 200 {
		t.Fatal(code)
	}
	var out struct {
		Teams    []models.TeamCount `json:"teams"`
		Teamless *int               `json:"teamless"`
	}
	json.Unmarshal(data, &out)
	if len(out.Teams) != 2 || out.Teamless != nil || out.Teams[0].RoleID != nil {
		t.Fatalf("a member sees the tags of the lists they read, no role ids, no teamless count: %s", data)
	}
	for _, tc := range out.Teams {
		if !tc.HasRole {
			t.Fatalf("a member sees which teams exist: %+v", tc)
		}
	}
	_, data = e.call(e.a.GetListTeams, e.as(e.admin), "GET", "/api/lists/teams", "", "")
	out.Teamless = nil
	json.Unmarshal(data, &out)
	if out.Teamless == nil || *out.Teamless != 2 /* Plain and the synthetic list */ || out.Teams[0].RoleID == nil {
		t.Fatalf("%s", data)
	}
}

// A member's new list has the member's team; the team tags they send are ignored; they see it
// at once. Admins must name a team.
func TestCreateListTeamRule(t *testing.T) {
	e := newTeamsEnv(t)
	create := e.guarded(e.a.CreateList)
	ua := e.as(e.ua)

	code, data := e.call(create, ua, "POST", "/api/lists", "", `{"name":"Mine","tags":["x","팀:나","팀:가"]}`)
	if code != 200 {
		t.Fatal(code)
	}
	var l models.List
	json.Unmarshal(data, &l)
	if !sameSet(l.Tags, []string{"x", "팀:가"}) {
		t.Fatalf("tags %v", l.Tags)
	}
	if got := e.listNames(e.as(e.ua), "/api/lists"); !sameSet(got, []string{"A1", "Both", "Mine"}) {
		t.Fatalf("the member sees the new list: %v", got)
	}
	if got := e.listNames(e.as(e.ub), "/api/lists"); !sameSet(got, []string{"B1", "Both"}) {
		t.Fatalf("the other team does not: %v", got)
	}
	// A member with no tags sent still gets the team.
	_, data = e.call(create, ua, "POST", "/api/lists", "", `{"name":"Untagged"}`)
	json.Unmarshal(data, &l)
	if !sameSet(l.Tags, []string{"팀:가"}) {
		t.Fatalf("tags %v", l.Tags)
	}

	// A user who is not a team member cannot make lists, as before.
	if code, _ := e.call(create, e.as(e.plain), "POST", "/api/lists", "", `{"name":"Nope"}`); code != http.StatusForbidden {
		t.Fatalf("an ordinary list role: %d", code)
	}

	admin := e.as(e.admin)
	if code, _ := e.call(create, admin, "POST", "/api/lists", "", `{"name":"NoTeam"}`); code != http.StatusBadRequest {
		t.Fatalf("an admin without a team: %d", code)
	}
	if code, _ := e.call(create, admin, "POST", "/api/lists", "", `{"name":"Typo","tags":["팀:없는팀"]}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown team: %d", code)
	}
	if code, data := e.call(create, admin, "POST", "/api/lists", "", `{"name":"Okay","tags":["팀:나"]}`); code != 200 {
		t.Fatalf("an admin with a team: %d %s", code, data)
	}
	if code, data := e.call(create, admin, "POST", "/api/lists", "", `{"name":"Admins only","no_team":true}`); code != 200 {
		t.Fatalf("no team on purpose: %d %s", code, data)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM roles c WHERE c.parent_id = $1`, e.team2)
	if n != 3 { // B1, Both, Okay
		t.Fatalf("team 나 holds %d lists", n)
	}
	if code, _ := e.call(create, e.as(e.superAdmin), "POST", "/api/lists", "", `{"name":"SA no team"}`); code != http.StatusBadRequest {
		t.Fatalf("a super admin follows the same rule: %d", code)
	}
}

// Saving a list: a member cannot change its teams; an admin can, and dropping every team needs
// no_team. A list that never had a team can still be saved.
func TestUpdateListTeamRule(t *testing.T) {
	e := newTeamsEnv(t)
	update := e.a.UpdateList
	ua, admin := e.as(e.ua), e.as(e.admin)
	id := itoa(e.aList)

	if code, _ := e.call(update, ua, "PUT", "/api/lists/x", id, `{"name":"A1","type":"private","optin":"single","tags":["y"]}`); code != 200 {
		t.Fatalf("%d", code)
	}
	if !sameSet(e.tagsOf(e.aList), []string{"y", "팀:가"}) {
		t.Fatalf("a member cannot drop the team: %v", e.tagsOf(e.aList))
	}
	e.call(update, ua, "PUT", "/api/lists/x", id, `{"name":"A1","type":"private","optin":"single","tags":["y","팀:나"]}`)
	if !sameSet(e.tagsOf(e.aList), []string{"y", "팀:가"}) {
		t.Fatalf("a member cannot add another team: %v", e.tagsOf(e.aList))
	}
	// An ordinary list role with manage access is held to the same.
	e.call(update, e.as(e.plain), "PUT", "/api/lists/x", id, `{"name":"A1","type":"private","optin":"single","tags":[]}`)
	if !sameSet(e.tagsOf(e.aList), []string{"팀:가"}) {
		t.Fatalf("%v", e.tagsOf(e.aList))
	}

	body := func(tags string, extra string) string {
		return `{"name":"A1","type":"private","optin":"single","tags":` + tags + extra + `}`
	}
	if code, _ := e.call(update, admin, "PUT", "/api/lists/x", id, body(`["y"]`, "")); code != http.StatusBadRequest {
		t.Fatalf("an admin dropping the team by accident: %d", code)
	}
	if code, _ := e.call(update, admin, "PUT", "/api/lists/x", id, body(`["y"]`, `,"no_team":true`)); code != 200 {
		t.Fatal(code)
	}
	if got := e.listNames(e.as(e.ua), "/api/lists"); !sameSet(got, []string{"Both"}) {
		t.Fatalf("the list left the team's view: %v", got)
	}
	if code, _ := e.call(update, admin, "PUT", "/api/lists/x", id, body(`["팀:나"]`, "")); code != 200 {
		t.Fatal(code)
	}
	if got := e.listNames(e.as(e.ub), "/api/lists"); !sameSet(got, []string{"A1", "B1", "Both"}) {
		t.Fatalf("moved to another team: %v", got)
	}
	// A list that has no team can be saved without choosing one.
	if code, _ := e.call(update, admin, "PUT", "/api/lists/x", itoa(e.plainList), `{"name":"Plain 2","type":"private","optin":"single","tags":[]}`); code != 200 {
		t.Fatalf("%d", code)
	}
}

// A member draws from a list of the team, into a list of the team; not from another team's.
func TestDrawTeamRule(t *testing.T) {
	e := newTeamsEnv(t)
	for i := 0; i < 6; i++ {
		id := e.addSub("enabled", "confirmed")
		e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed') ON CONFLICT DO NOTHING`, id, e.bothList)
		e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed') ON CONFLICT DO NOTHING`, id, e.bList)
		e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed') ON CONFLICT DO NOTHING`, id, e.plainList)
	}
	draw := e.guarded(e.a.DrawList)
	preview := e.guarded(e.a.PreviewListDraw)
	ua := e.as(e.ua)
	body := `{"unit":"count","value":2}`

	if code, _ := e.call(preview, ua, "POST", "/x", itoa(e.bList), body); code != http.StatusForbidden {
		t.Fatalf("preview from the other team's list: %d", code)
	}
	if code, _ := e.call(draw, ua, "POST", "/x", itoa(e.bList), body); code != http.StatusForbidden {
		t.Fatalf("draw from the other team's list: %d", code)
	}
	if code, _ := e.call(draw, e.as(e.plain), "POST", "/x", itoa(e.aList), body); code != http.StatusForbidden {
		t.Fatalf("an ordinary list role cannot draw: %d", code)
	}
	if code, _ := e.call(preview, ua, "POST", "/x", itoa(e.bothList), body); code != 200 {
		t.Fatalf("preview from a shared list: %d", code)
	}
	code, data := e.call(draw, ua, "POST", "/x", itoa(e.bothList), body)
	if code != 200 {
		t.Fatalf("draw from a shared list: %d", code)
	}
	var out struct {
		List models.List `json:"list"`
	}
	json.Unmarshal(data, &out)
	if !sameSet(out.List.Tags, []string{"팀:가", models.DrawListTag}) {
		t.Fatalf("a member's drawn list belongs to their team only: %v", out.List.Tags)
	}
	if got := e.listNames(e.as(e.ua), "/api/lists"); len(got) != 3 {
		t.Fatalf("the member sees the drawn list: %v", got)
	}
	if got := e.listNames(e.as(e.ub), "/api/lists"); len(got) != 2 {
		t.Fatalf("the other team does not: %v", got)
	}

	// An admin: from a shared list the teams come along; from a list without a team a team
	// must be named (or none on purpose).
	admin := e.as(e.admin)
	code, data = e.call(draw, admin, "POST", "/x", itoa(e.bothList), body)
	json.Unmarshal(data, &out)
	if code != 200 || !sameSet(out.List.Tags, []string{"팀:가", "팀:나", models.DrawListTag}) {
		t.Fatalf("%d %v", code, out.List.Tags)
	}
	if code, _ := e.call(draw, admin, "POST", "/x", itoa(e.plainList), body); code != http.StatusBadRequest {
		t.Fatalf("no team to inherit: %d", code)
	}
	code, data = e.call(draw, admin, "POST", "/x", itoa(e.plainList), `{"unit":"count","value":2,"team":"팀:나"}`)
	json.Unmarshal(data, &out)
	if code != 200 || !sameSet(out.List.Tags, []string{"팀:나", models.DrawListTag}) {
		t.Fatalf("%d %v", code, out.List.Tags)
	}
	if code, _ := e.call(draw, admin, "POST", "/x", itoa(e.plainList), `{"unit":"count","value":2,"team":"팀:없음"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown team: %d", code)
	}
	code, data = e.call(draw, admin, "POST", "/x", itoa(e.plainList), `{"unit":"count","value":2,"no_team":true}`)
	json.Unmarshal(data, &out)
	if code != 200 || !sameSet(out.List.Tags, []string{models.DrawListTag}) {
		t.Fatalf("%d %v", code, out.List.Tags)
	}
}

// A dynamic list preview for a new list is open to team members too (it needs the same right
// as making the list).
func TestDynamicPreviewForTeamMembers(t *testing.T) {
	e := newTeamsEnv(t)
	body := `{"filter":{"version":1,"base":{"type":"lists","lists":[` + itoa(e.aList) + `]},"match":"all","conditions":[]}}`
	if code, _ := e.call(e.a.PreviewDynamicList, e.as(e.ua), "POST", "/x", "", body); code == http.StatusForbidden {
		t.Fatalf("a member may preview a dynamic list of their own team: %d", code)
	}
	if code, _ := e.call(e.a.PreviewDynamicList, e.as(e.plain), "POST", "/x", "", body); code != http.StatusForbidden {
		t.Fatalf("an ordinary list role may not: %d", code)
	}
}

// A subscriber on lists of two teams is seen by both, with a mark of the other team that has
// no list name in it.
func TestSubscriberOtherTeams(t *testing.T) {
	e := newTeamsEnv(t)
	shared := e.addSub("enabled", "confirmed")
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed'),($1,$3,'confirmed'),($1,$4,'confirmed')`, shared, e.aList, e.bList, e.plainList)
	only := e.addSub("enabled", "confirmed")
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, only, e.aList)
	e.db.MustExec(`DELETE FROM subscriber_lists WHERE list_id = $1`, e.listID) // the synthetic list is nobody's team`)
	ua := e.as(e.ua)

	code, data := e.call(e.a.GetSubscriber, ua, "GET", "/x", itoa(shared), "")
	if code != 200 {
		t.Fatal(code)
	}
	var sub models.Subscriber
	json.Unmarshal(data, &sub)
	got := map[string]int{}
	for _, o := range sub.OtherTeams {
		got[o.Team] = o.Lists
	}
	if len(got) != 2 || got["나"] != 1 || got[""] != 1 {
		t.Fatalf("%+v", sub.OtherTeams)
	}
	if strings.Contains(string(data), `"B1"`) || strings.Contains(string(data), `"Plain"`) {
		t.Fatalf("a list name of another team is in the answer: %s", data)
	}
	if code, data = e.call(e.a.GetSubscriber, ua, "GET", "/x", itoa(only), ""); code != 200 || strings.Contains(string(data), "other_teams") {
		t.Fatalf("a subscriber of the team only has no mark: %d %s", code, data)
	}

	// The list of subscribers carries the mark too. The other team sees the shared subscriber.
	_, data = e.call(e.a.QuerySubscribers, ua, "GET", "/api/subscribers", "", "")
	var page struct {
		Results []models.Subscriber `json:"results"`
	}
	json.Unmarshal(data, &page)
	marked := 0
	for _, s := range page.Results {
		if len(s.OtherTeams) > 0 {
			marked++
		}
	}
	if len(page.Results) != 2 || marked != 1 {
		t.Fatalf("%d subscribers, %d marked", len(page.Results), marked)
	}
	if code, _ := e.call(e.a.GetSubscriber, e.as(e.ub), "GET", "/x", itoa(shared), ""); code != 200 {
		t.Fatalf("the other team sees the shared subscriber: %d", code)
	}
	if code, _ := e.call(e.a.GetSubscriber, e.as(e.ub), "GET", "/x", itoa(only), ""); code != http.StatusForbidden {
		t.Fatalf("but not the other team's own: %d", code)
	}
	// An admin gets no mark.
	_, data = e.call(e.a.GetSubscriber, e.as(e.admin), "GET", "/x", itoa(shared), "")
	if strings.Contains(string(data), "other_teams") {
		t.Fatalf("%s", data)
	}
}

// Only someone who reads all subscribers takes a subscriber off the blocklist.
func TestUnblockIsAdminOnly(t *testing.T) {
	e := newTeamsEnv(t)
	id := e.addSub("blocklisted", "unsubscribed")
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unsubscribed') ON CONFLICT DO NOTHING`, id, e.aList)
	ua, admin := e.as(e.ua), e.as(e.admin)
	if err := e.a.checkUnblock(ua, id, "enabled"); err == nil {
		t.Fatal("a member unblocked a subscriber")
	}
	if err := e.a.checkUnblock(ua, id, "blocklisted"); err != nil {
		t.Fatal("keeping the status is fine")
	}
	if err := e.a.checkUnblock(ua, id, ""); err != nil {
		t.Fatal("no status is fine")
	}
	if err := e.a.checkUnblock(admin, id, "enabled"); err != nil {
		t.Fatalf("an admin may: %v", err)
	}
	fine := e.addSub("enabled", "confirmed")
	if err := e.a.checkUnblock(ua, fine, "enabled"); err != nil {
		t.Fatalf("an ordinary subscriber keeps being edited: %v", err)
	}
}

// Team roles are changed on the teams screen. A list role in use is not deleted (it would take
// the accounts with it).
func TestRoleGuards(t *testing.T) {
	e := newTeamsEnv(t)
	admin := e.as(e.admin)
	if code, _ := e.call(e.a.UpdateListRole, admin, "PUT", "/x", itoa(e.team1), `{"name":"x","lists":[]}`); code != http.StatusBadRequest {
		t.Fatalf("editing a team role: %d", code)
	}
	if code, _ := e.call(e.a.DeleteRole, admin, "DELETE", "/x", itoa(e.team1), ""); code != http.StatusBadRequest {
		t.Fatalf("deleting a team role: %d", code)
	}
	var plainRole int
	e.get(&plainRole, `SELECT list_role_id FROM users WHERE id=$1`, e.plain)
	if code, _ := e.call(e.a.DeleteRole, admin, "DELETE", "/x", itoa(plainRole), ""); code != http.StatusBadRequest {
		t.Fatalf("deleting a list role in use: %d", code)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM users WHERE id=$1`, e.plain)
	if n != 1 {
		t.Fatal("the account is gone")
	}
	e.db.MustExec(`UPDATE users SET list_role_id=NULL WHERE id=$1`, e.plain)
	if code, _ := e.call(e.a.DeleteRole, admin, "DELETE", "/x", itoa(plainRole), ""); code != 200 {
		t.Fatalf("deleting an unused list role: %d", code)
	}
}

// The team API.
func TestTeamAPI(t *testing.T) {
	e := newTeamsEnv(t)
	admin := e.as(e.admin)
	code, data := e.call(e.a.CreateTeam, admin, "POST", "/api/teams", "", `{"name":"다","user_role_id":`+itoa(e.memberRole)+`}`)
	var team models.Team
	json.Unmarshal(data, &team)
	if code != 200 || team.Tag != "팀:다" || team.Lists != 0 {
		t.Fatalf("%d %s", code, data)
	}
	if code, _ := e.call(e.a.CreateTeam, admin, "POST", "/api/teams", "", `{"name":"다"}`); code != http.StatusBadRequest {
		t.Fatalf("duplicate: %d", code)
	}
	code, data = e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(team.ID), `{"user_ids":[`+itoa(e.ua)+`]}`)
	json.Unmarshal(data, &team)
	if code != 200 || len(team.Members) != 1 {
		t.Fatalf("%d %s", code, data)
	}
	// The member moved: they now see no list of the old team.
	if got := e.listNames(e.as(e.ua), "/api/lists"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if code, _ := e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(team.ID), `{"user_ids":[`+itoa(e.superAdmin)+`]}`); code != http.StatusBadRequest {
		t.Fatalf("super admin: %d", code)
	}
	code, data = e.call(e.a.UpdateTeam, admin, "PUT", "/x", itoa(e.team1), `{"name":"가 새이름"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, data)
	}
	if !sameSet(e.tagsOf(e.aList), []string{"팀:가-새이름"}) {
		t.Fatalf("the lists were retagged: %v", e.tagsOf(e.aList))
	}
	if code, _ := e.call(e.a.DeleteTeam, admin, "DELETE", "/x", itoa(e.team1), ""); code != http.StatusBadRequest {
		t.Fatalf("a team with lists: %d", code)
	}
}

// A plain unsubscribe blocklists; the preferences form does not unless its box is ticked and
// the setting allows it.
func TestUnsubscribeBlocklists(t *testing.T) {
	for _, c := range []struct{ manage, allow, ticked, want bool }{
		{false, false, false, true}, {false, true, false, true}, {false, true, true, true},
		{true, false, false, false}, {true, true, false, false}, {true, false, true, false}, {true, true, true, true},
	} {
		if got := unsubscribeBlocklists(c.manage, c.allow, c.ticked); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}

// An API user in a team has its permissions in memory; a new list of the team reaches it.
func TestAPIUserFollowsTeamLists(t *testing.T) {
	e := newTeamsEnv(t)
	e.db.MustExec(`INSERT INTO users(username,email,name,password,user_role_id,list_role_id,status,type) VALUES('bot','bot@example.invalid','bot',$1,$2,$3,'enabled','api')`,
		auth.HashAPIToken("secret"), e.memberRole, e.team1)
	if _, err := cacheUsers(e.core, e.a.auth); err != nil {
		t.Fatal(err)
	}
	bot, ok := e.a.auth.GetAPIToken("bot", "secret")
	if !ok || len(bot.ListPermissionsMap) != 2 {
		t.Fatalf("%v %d", ok, len(bot.ListPermissionsMap))
	}
	code, data := e.call(e.guarded(e.a.CreateList), e.as(e.admin), "POST", "/api/lists", "", `{"name":"Fresh","tags":["팀:가"]}`)
	if code != 200 {
		t.Fatalf("%d", code)
	}
	var l models.List
	json.Unmarshal(data, &l)
	bot, _ = e.a.auth.GetAPIToken("bot", "secret")
	if _, ok := bot.ListPermissionsMap[l.ID]; !ok || len(bot.ListPermissionsMap) != 3 {
		t.Fatalf("the API user does not know the new list: %v", bot.ListPermissionsMap)
	}
	// And a list that leaves the team leaves the API user.
	e.call(e.a.UpdateList, e.as(e.admin), "PUT", "/x", itoa(l.ID), `{"name":"Fresh","type":"private","optin":"single","tags":["팀:나"]}`)
	bot, _ = e.a.auth.GetAPIToken("bot", "secret")
	if _, ok := bot.ListPermissionsMap[l.ID]; ok {
		t.Fatal("the API user still has the list")
	}
}

// Someone who may make every list but may not manage roles (an operator) still learns which
// teams exist, so the screens can ask for the team of a list; they get no role IDs.
func TestOperatorWithoutRolesSeesTeamsToChoose(t *testing.T) {
	e := newTeamsEnv(t)
	var role, user int
	e.get(&role, `INSERT INTO roles(type,name,permissions) VALUES('user','operator-no-roles','{lists:get_all,lists:manage_all,subscribers:get_all}') RETURNING id`)
	e.get(&user, `INSERT INTO users(username,email,name,user_role_id,status,type) VALUES('op-no-roles','op-no-roles@example.invalid','op',$1,'enabled','user') RETURNING id`, role)
	op := e.as(user)
	code, data := e.call(e.a.GetListTeams, op, "GET", "/api/lists/teams", "", "")
	if code != 200 {
		t.Fatal(code)
	}
	var out struct {
		Teams []models.TeamCount `json:"teams"`
	}
	json.Unmarshal(data, &out)
	if len(out.Teams) != 2 {
		t.Fatalf("%s", data)
	}
	for _, tc := range out.Teams {
		if !tc.HasRole || tc.RoleID != nil {
			t.Fatalf("has_role without a role id: %+v", tc)
		}
	}
	// And their list needs a team, which they can name.
	create := e.guarded(e.a.CreateList)
	if code, _ := e.call(create, op, "POST", "/api/lists", "", `{"name":"No team"}`); code != http.StatusBadRequest {
		t.Fatalf("%d", code)
	}
	if code, data := e.call(create, op, "POST", "/api/lists", "", `{"name":"Op list","tags":["팀:가"]}`); code != 200 {
		t.Fatalf("%d %s", code, data)
	}
}

// LST-68: the permissions a team member must not have (core.TeamWidePerms), on the four ways in.

func (e *teamsEnv) userRole(name string, perms ...string) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO roles(type,name,permissions) VALUES('user',$1,$2) RETURNING id`, name, pq.StringArray(perms))
	return id
}

// allowPerms lets the role screen accept the permissions (the audit env knows only two).
func (e *teamsEnv) allowPerms(perms ...string) {
	for _, p := range perms {
		e.a.cfg.Permissions[p] = struct{}{}
	}
}

func teamBody(name string, role int) string {
	return fmt.Sprintf(`{"name":%q,"user_role_id":%d}`, name, role)
}

func roleBody(name string, perms ...string) string {
	b, _ := json.Marshal(map[string]any{"name": name, "permissions": perms})
	return string(b)
}

func TestTeamDefaultRoleRefusesWidePerms(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	for i, p := range core.TeamWidePerms {
		r := e.userRole("wide-"+itoa(i), p, "subscribers:get")
		if code, _ := e.call(e.a.CreateTeam, admin, "POST", "/x", "", teamBody("새"+itoa(i), r)); code != http.StatusBadRequest {
			t.Fatalf("create with %s: %d", p, code)
		}
		if code, _ := e.call(e.a.UpdateTeam, admin, "PUT", "/x", itoa(e.team1), teamBody("가", r)); code != http.StatusBadRequest {
			t.Fatalf("update with %s: %d", p, code)
		}
	}
	// What a team may keep: the dashboard numbers, the bounce screen, ordinary rights.
	ok := e.userRole("clean", "campaigns:get_analytics", "bounces:get", "bounces:manage", "subscribers:get", "campaigns:get")
	if code, data := e.call(e.a.CreateTeam, admin, "POST", "/x", "", teamBody("깨끗", ok)); code != 200 {
		t.Fatalf("clean role: %d %s", code, data)
	}
	// Reading across teams is how members work: subscribers:get_all stays allowed.
	reads := e.userRole("reads", "subscribers:get_all", "subscribers:get")
	if code, data := e.call(e.a.CreateTeam, admin, "POST", "/x", "", teamBody("읽기", reads)); code != 200 {
		t.Fatalf("a role that only reads all subscribers: %d %s", code, data)
	}
	if code, _ := e.call(e.a.CreateTeam, admin, "POST", "/x", "", teamBody("슈퍼", 1)); code != http.StatusBadRequest {
		t.Fatalf("super admin role: %d", code)
	}
}

func TestTeamMemberWithWidePermsNeedsTheTeamsRole(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	for _, p := range core.TeamWidePerms {
		r := e.userRole("w-"+p, p)
		var u int
		e.get(&u, `INSERT INTO users(username,email,name,user_role_id,status,type) VALUES($1,$1||'@example.invalid','w',$2,'enabled','user') RETURNING id`, "w-"+strings.ReplaceAll(p, ":", "-"), r)
		bare, err := e.core.CreateTeam("무역할"+p, 0)
		if err != nil {
			t.Fatal(err)
		}
		if code, _ := e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(bare.ID), fmt.Sprintf(`{"user_ids":[%d]}`, u)); code != http.StatusBadRequest {
			t.Fatalf("%s into a team without a default role: %d", p, code)
		}
		if code, _ := e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(e.team1), fmt.Sprintf(`{"user_ids":[%d,%d]}`, e.ua, u)); code != 200 {
			t.Fatalf("%s into a team with one: %d", p, code)
		}
		var got int
		e.get(&got, `SELECT user_role_id FROM users WHERE id = $1`, u)
		if got != e.memberRole {
			t.Fatalf("%s: the new member gets the team's role: %d", p, got)
		}
		e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(e.team1), fmt.Sprintf(`{"user_ids":[%d]}`, e.ua))
	}
}

// The roles screen: a role a team uses keeps no wide permission; any other role is free.
func TestUserRoleOfATeamKeepsNoWidePerms(t *testing.T) {
	e := newAuditEnv(t)
	e.allowPerms(append([]string{"campaigns:get_analytics", "roles:get", "subscribers:manage", "campaigns:manage", "subscribers:get_all", "lists:get_all", "campaigns:get_all"}, core.TeamWidePerms...)...)
	admin := e.as(e.admin)
	save := func(id int, perms ...string) (int, string) {
		code, data := e.call(e.a.UpdateUserRole, admin, "PUT", "/x", itoa(id), roleBody("r"+itoa(id), perms...))
		return code, string(data)
	}
	// The default role of a team.
	r := e.userRole("team-role", "subscribers:get")
	if _, err := e.core.CreateTeam("다", r); err != nil {
		t.Fatal(err)
	}
	for _, p := range core.TeamWidePerms {
		if code, _ := save(r, "subscribers:get", p); code != http.StatusBadRequest {
			t.Fatalf("adding %s to a team's role: %d", p, code)
		}
	}
	if code, _ := save(r, "subscribers:get", "campaigns:get", "campaigns:get_analytics", "subscribers:get_all"); code != 200 {
		t.Fatalf("other permissions: %d", code)
	}
	// Used by a member only.
	solo := e.userRole("solo", "subscribers:get")
	e.db.MustExec(`UPDATE users SET user_role_id = $1 WHERE id = $2`, solo, e.ub)
	if code, _ := save(solo, "campaigns:manage_all"); code != http.StatusBadRequest {
		t.Fatalf("a member's role: %d", code)
	}
	// A role no team uses; the operator role, used by an admin who is no member.
	free := e.userRole("free", "subscribers:get")
	if code, _ := save(free, "lists:get_all", "campaigns:manage_all"); code != 200 {
		t.Fatalf("free role: %d", code)
	}
	var operator int
	e.get(&operator, `SELECT user_role_id FROM users WHERE id = $1`, e.admin)
	if code, _ := save(operator, "lists:get_all", "lists:manage_all", "subscribers:get_all", "roles:get"); code != 200 {
		t.Fatalf("operator: %d", code)
	}
}

// The users screen: a team member does not get a wide user role.
func TestTeamMemberUserRoleChange(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	var operator int
	e.get(&operator, `SELECT user_role_id FROM users WHERE id = $1`, e.admin)
	body := func(name string, userRole int, listRole any) string {
		b, _ := json.Marshal(map[string]any{"username": name + "-user", "email": name + "@example.invalid", "name": "N " + name,
			"type": "user", "status": "enabled", "user_role_id": userRole, "list_role_id": listRole})
		return string(b)
	}
	update := func(id int, b string) int {
		code, _ := e.call(e.a.UpdateUser, admin, "PUT", "/x", itoa(id), b)
		return code
	}
	if code := update(e.ua, body("ua", e.memberRole, e.team1)); code != 200 {
		t.Fatalf("a rename: %d", code)
	}
	if code := update(e.ua, body("ua", operator, e.team1)); code != http.StatusBadRequest {
		t.Fatalf("a member given the operator role: %d", code)
	}
	if code := update(e.ua, body("ua", 1, e.team1)); code != http.StatusBadRequest {
		t.Fatalf("a member given the super admin role: %d", code)
	}
	// A person who is not a member, or who leaves the team in the same save.
	if code := update(e.plain, body("plain", operator, nil)); code != 200 {
		t.Fatalf("not a member: %d", code)
	}
	if code := update(e.ub, body("ub", operator, nil)); code != 200 {
		t.Fatalf("leaving the team: %d", code)
	}
	// Joining a team with a wide role at the same time.
	if code := update(e.admin, body("admin", operator, e.team2)); code != http.StatusBadRequest {
		t.Fatalf("an operator joining a team: %d", code)
	}
	// New users.
	create := func(b string) int {
		code, _ := e.call(e.a.CreateUser, admin, "POST", "/x", "", b)
		return code
	}
	if code := create(body("newwide", operator, e.team1)); code != http.StatusBadRequest {
		t.Fatalf("new wide member: %d", code)
	}
	if code := create(body("newmember", e.memberRole, e.team1)); code != 200 {
		t.Fatalf("new member: %d", code)
	}
	if code := create(body("neweditor", operator, nil)); code != 200 {
		t.Fatalf("new operator without a team: %d", code)
	}
}

// What production looked like: the team's default role (and so its members) already had
// lists:*_all and subscribers:get_all. Nothing is mended by the code; the screens tell, the
// save that keeps things as they are works, and the ways to make it worse are shut.
func TestTeamAlreadyWide(t *testing.T) {
	e := newAuditEnv(t)
	e.allowPerms(append([]string{"campaigns:get_analytics", "roles:get", "subscribers:manage", "campaigns:manage", "subscribers:get_all", "lists:get_all", "campaigns:get_all"}, core.TeamWidePerms...)...)
	admin := e.as(e.admin)
	wide := []string{"lists:manage_all"}
	// Production's role: the two reading permissions beside it are not what the screens warn about.
	e.db.MustExec(`UPDATE roles SET permissions = $2 WHERE id = $1`, e.memberRole, pq.StringArray([]string{"subscribers:get", "subscribers:manage", "campaigns:get", "campaigns:manage", "lists:get_all", "lists:manage_all", "subscribers:get_all"}))

	teams := func() map[int]models.Team {
		code, data := e.call(e.a.GetTeams, admin, "GET", "/x", "", "")
		if code != 200 {
			t.Fatal(code)
		}
		var ts []models.Team
		json.Unmarshal(data, &ts)
		m := map[int]models.Team{}
		for _, tm := range ts {
			m[tm.ID] = tm
		}
		return m
	}
	tm := teams()[e.team1]
	if !sameSet(tm.Wide, wide) || tm.WideMembers != 1 {
		t.Fatalf("the teams screen: wide %v, %d members", tm.Wide, tm.WideMembers)
	}
	if raw, _ := json.Marshal(tm); !strings.Contains(string(raw), `"wide_members":1`) {
		t.Fatalf("%s", raw)
	}

	// Saving the team as it is, or its name, is refused: its default role is wide.
	if code, _ := e.call(e.a.UpdateTeam, admin, "PUT", "/x", itoa(e.team1), teamBody("가", e.memberRole)); code != http.StatusBadRequest {
		t.Fatalf("update of a wide team: %d", code)
	}
	// A member can still change their name or password: the role does not change.
	b, _ := json.Marshal(map[string]any{"username": "ua-user", "email": "ua@example.invalid", "name": "새 이름", "type": "user", "status": "enabled",
		"user_role_id": e.memberRole, "list_role_id": e.team1})
	if code, _ := e.call(e.a.UpdateUser, admin, "PUT", "/x", itoa(e.ua), string(b)); code != 200 {
		t.Fatalf("member rename: %d", code)
	}
	// The same members saved again, or fewer, pass; a new one does not.
	set := func(ids ...int) int {
		b, _ := json.Marshal(map[string]any{"user_ids": ids})
		code, _ := e.call(e.a.SetTeamMembers, admin, "PUT", "/x", itoa(e.team1), string(b))
		return code
	}
	if code := set(e.ua); code != 200 {
		t.Fatalf("same members: %d", code)
	}
	if code := set(e.ua, e.plain); code != http.StatusBadRequest {
		t.Fatalf("a new member of a wide team: %d", code)
	}
	if code := set(); code != 200 {
		t.Fatalf("no members: %d", code)
	}
	e.db.MustExec(`UPDATE users SET list_role_id = $1 WHERE id = $2`, e.team1, e.ua)
	// The role screen: keeping the wide permissions is refused, taking them out is how it is mended.
	save := func(perms ...string) int {
		code, _ := e.call(e.a.UpdateUserRole, admin, "PUT", "/x", itoa(e.memberRole), roleBody("member", perms...))
		return code
	}
	if code := save("subscribers:get", "lists:manage_all"); code != http.StatusBadRequest {
		t.Fatalf("keeping a wide permission: %d", code)
	}
	if code := save("subscribers:get", "subscribers:manage", "campaigns:get", "campaigns:manage"); code != 200 {
		t.Fatalf("mending: %d", code)
	}
	tm = teams()[e.team1]
	if len(tm.Wide) != 0 || tm.WideMembers != 0 {
		t.Fatalf("mended: %v %d", tm.Wide, tm.WideMembers)
	}
	if code, _ := e.call(e.a.UpdateTeam, admin, "PUT", "/x", itoa(e.team1), teamBody("가", e.memberRole)); code != 200 {
		t.Fatalf("update after the mend: %d", code)
	}
}

// LST-68, decision B: lists:get_all is read access. Upstream reads "get or manage" list access as
// one in its campaign checks, so a member with lists:get_all and campaigns:manage could change,
// send and aim campaigns at the lists of other teams. They may read everything, and write to
// the campaigns of their own lists only. lists:manage_all and the super admin keep every
// campaign; campaigns:get_all reads every campaign and writes none.
func TestListsGetAllDoesNotManageOtherTeamsCampaigns(t *testing.T) {
	e := newAuditEnv(t)
	camp := func(name string, list int) int {
		var id int
		e.get(&id, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),$1,'S','test@example.invalid','b','email') RETURNING id`, name)
		e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) SELECT $1, id, name FROM lists WHERE id = $2`, id, list)
		return id
	}
	campA, campB := camp("A camp", e.aList), camp("B camp", e.bList)

	can := func(user, c int, typ auth.PermType) int {
		h := func(ctx echo.Context) error { return e.a.checkCampaignPerm(typ, getID(ctx), ctx) }
		code, _ := e.call(h, e.as(user), "PUT", "/x", itoa(c), "")
		return code
	}
	member := func(name string, perms ...string) int {
		role := e.userRole("camp-"+name, perms...)
		var id int
		e.get(&id, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES($1,$1||'@example.invalid',$1,$2,$3,'enabled','user') RETURNING id`, "u-"+name, role, e.team1)
		return id
	}
	plain := member("plain", "campaigns:get", "campaigns:manage")
	readsLists := member("lga", "campaigns:get", "campaigns:manage", "lists:get_all")
	readsCamps := member("cga", "campaigns:get", "campaigns:manage", "campaigns:get_all")
	both := member("both", "campaigns:get", "campaigns:manage", "lists:get_all", "lists:manage_all")
	writesAll := member("cma", "campaigns:get", "campaigns:manage", "campaigns:manage_all")

	for name, u := range map[string]int{"plain": plain, "lists:get_all": readsLists, "campaigns:get_all": readsCamps} {
		if code := can(u, campA, auth.PermTypeManage); code != 200 {
			t.Fatalf("%s manages their own team's campaign: %d", name, code)
		}
		if code := can(u, campB, auth.PermTypeManage); code != http.StatusForbidden {
			t.Fatalf("%s manages another team's campaign: %d", name, code)
		}
	}
	// Reading stays as it was.
	for name, u := range map[string]int{"lists:get_all": readsLists, "campaigns:get_all": readsCamps} {
		if code := can(u, campB, auth.PermTypeGet); code != 200 {
			t.Fatalf("%s reads another team's campaign: %d", name, code)
		}
	}
	if code := can(plain, campB, auth.PermTypeGet); code != http.StatusForbidden {
		t.Fatalf("a member without a reading permission: %d", code)
	}
	// A campaign that sends to a list of another team as well is not the member's to change: a
	// send or a delete reaches all of its lists.
	mixed := camp("A and B", e.aList)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES($1,$2,'B1')`, mixed, e.bList)
	both2 := camp("Both", e.bothList)
	gone := camp("A and a deleted list", e.aList)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES($1,NULL,'gone')`, gone)
	for name, u := range map[string]int{"plain": plain, "lists:get_all": readsLists} {
		if code := can(u, mixed, auth.PermTypeManage); code != http.StatusForbidden {
			t.Fatalf("%s manages a campaign that also sends to another team: %d", name, code)
		}
		if code := can(u, mixed, auth.PermTypeGet); name == "lists:get_all" && code != 200 {
			t.Fatalf("%s reads it: %d", name, code)
		}
		if code := can(u, both2, auth.PermTypeManage); code != 200 {
			t.Fatalf("%s: a list that both teams own: %d", name, code)
		}
		if code := can(u, gone, auth.PermTypeManage); code != 200 {
			t.Fatalf("%s: a campaign whose other list was deleted: %d", name, code)
		}
	}
	// The delete by IDs, by query and "all" has the same reach.
	deleted, err := e.core.DeleteCampaignsGuarded([]int{mixed, campA, campB}, "", true, false, []int{e.aList, e.bothList})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].ID != campA {
		t.Fatalf("only the campaign of the user's own lists is deleted: %+v", deleted)
	}
	// What the *_all permissions that write still do (the decision keeps them off team roles).
	for name, u := range map[string]int{"lists:manage_all": both, "campaigns:manage_all": writesAll, "super admin": e.superAdmin} {
		if code := can(u, campB, auth.PermTypeManage); code != 200 {
			t.Fatalf("%s: %d", name, code)
		}
	}

	// The lists a campaign is saved with.
	ids := []int{e.aList, e.bList, e.bothList, e.plainList}
	same := func(who string, got, want []int) {
		t.Helper()
		g, w := []string{}, []string{}
		for _, id := range got {
			g = append(g, itoa(id))
		}
		for _, id := range want {
			w = append(w, itoa(id))
		}
		if !sameSet(g, w) {
			t.Fatalf("%s: %v, want %v", who, got, want)
		}
	}
	ids = []int{e.aList, e.bothList}
	got := func(u int, in []int) ([]int, error) { return campaignWriteListIDs(e.as(u), in) }
	g, err := got(plain, []int{e.aList, e.bList, e.bothList, e.plainList})
	if err != nil {
		t.Fatal(err)
	}
	same("plain (upstream filter)", g, ids)
	// A user with lists:get_all is refused a list that is not theirs, not given fewer lists.
	if g, err = got(readsLists, []int{e.aList, e.bList}); err != auth.ErrPermDenied {
		t.Fatalf("lists:get_all asking for another team's list: %v %v", g, err)
	}
	g, err = got(readsLists, ids)
	if err != nil {
		t.Fatal(err)
	}
	same("lists:get_all", g, ids)
	all4 := []int{e.aList, e.bList, e.bothList, e.plainList}
	g, _ = got(both, all4)
	same("lists:manage_all", g, all4)
	g, _ = got(e.superAdmin, all4)
	same("super admin", g, all4)
	if all, own := campaignWriteLists(e.as(readsLists)); all || len(own) != 2 {
		t.Fatalf("lists:get_all writes to %v %v", all, own)
	}
	// The dashboard's "own" (LST-64) uses the write rule, not the read one.
	own := func(u int, lists ...int) bool { return campaignIsOwn(e.as(u), lists) }
	if !own(readsLists, e.aList) || !own(readsLists, e.aList, e.bothList) || own(readsLists, e.aList, e.bList) || own(readsLists, e.bList) || own(readsLists) {
		t.Fatal("lists:get_all owns the campaigns of its own lists only")
	}
	if !own(both, e.bList) || !own(e.superAdmin, e.bList) {
		t.Fatal("lists:manage_all owns every campaign")
	}
	if all, _ := campaignWriteLists(e.as(both)); !all {
		t.Fatal("lists:manage_all writes everywhere")
	}
}

// The users screen judges the pair of roles the save will store, not only the ones in the request:
// a user role ID of 0 keeps the present role, a list role ID of 0 keeps the present list role, and
// no list role clears it (queries/users.sql update-user).
func TestTeamMemberRoleIsJudgedByTheStoredPair(t *testing.T) {
	e := newAuditEnv(t)
	admin := e.as(e.admin)
	var operator int
	e.get(&operator, `SELECT user_role_id FROM users WHERE id = $1`, e.admin)
	update := func(id int, body map[string]any) int {
		body["username"], body["email"], body["type"], body["status"] = "user-"+itoa(id), "user"+itoa(id)+"@example.invalid", "user", "enabled"
		b, _ := json.Marshal(body)
		code, _ := e.call(e.a.UpdateUser, admin, "PUT", "/x", itoa(id), string(b))
		return code
	}
	// The admin has the operator role and no team. Only the list role is sent: the stored pair
	// would be a team member with the operator role.
	if code := update(e.admin, map[string]any{"list_role_id": e.team1}); code != http.StatusBadRequest {
		t.Fatalf("joining a team with the kept operator role: %d", code)
	}
	var lr *int
	e.get(&lr, `SELECT list_role_id FROM users WHERE id = $1`, e.admin)
	if lr != nil {
		t.Fatalf("nothing was stored: %v", *lr)
	}
	// A member is sent with a new user role and list role 0 (keep): still a member.
	if code := update(e.ua, map[string]any{"user_role_id": operator, "list_role_id": 0}); code != http.StatusBadRequest {
		t.Fatalf("a member given the operator role, list role kept: %d", code)
	}
	// A member sent with the new role and no list role leaves the team in the same save.
	if code := update(e.ub, map[string]any{"user_role_id": operator}); code != 200 {
		t.Fatalf("leaving the team while the role changes: %d", code)
	}
	// Neither role in the request, a member of a wide team keeps working.
	e.db.MustExec(`UPDATE roles SET permissions = '{subscribers:get,lists:manage_all}' WHERE id = $1`, e.memberRole)
	if code := update(e.plain, map[string]any{"name": "N", "list_role_id": 0}); code != 200 {
		t.Fatalf("a rename of somebody in an ordinary list role: %d", code)
	}
}

// The team-role lock serializes: a second taker waits until the first lets go.
func TestTeamRoleLockSerializes(t *testing.T) {
	e := newAuditEnv(t)
	unlock, err := e.core.LockTeamRoles()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u2, err := e.core.LockTeamRoles()
		if err == nil {
			u2()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("the second taker did not wait")
	case <-time.After(500 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker never got the lock")
	}
}
