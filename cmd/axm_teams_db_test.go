package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
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
