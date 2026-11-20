package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

type recentListEnv struct {
	*teamsEnv
	admin, member auth.User
	draft         int
	a1, a2, a3    int // team 가 audience: one received recently, one earlier, one blocklisted
	a4, a5, both  int // unsubscribed, clean, member of both team lists (received recently)
	b1, plain1    int
	sentA, sentB  int
}

// newRecentListEnv builds a small audience across two teams with a mix of recent sends, and real
// translations so default names and descriptions can be checked.
func newRecentListEnv(t *testing.T) *recentListEnv {
	t.Helper()
	te := newTeamsEnv(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "i18n", "ko.json"))
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	te.a.cfg = &Config{}
	te.a.log = log.New(io.Discard, "", 0)
	te.a.i18n = lang
	te.a.core = core.New(&core.Opt{DB: te.db, Queries: te.q, Log: te.a.log, I18n: lang}, nil)
	e := &recentListEnv{teamsEnv: te, admin: te.as(te.superAdmin), member: te.as(te.ua)}

	person := func(name, status string, lists map[int]string) int {
		var id int
		e.get(&id, `INSERT INTO subscribers(uuid,email,name,status) VALUES(gen_random_uuid(),$1,$1,$2::subscriber_status) RETURNING id`,
			name+"@example.test", status)
		for list, sub := range lists {
			e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES ($1,$2,$3::subscription_status)`, id, list, sub)
		}
		return id
	}
	e.a1 = person("a1", "enabled", map[int]string{e.aList: "confirmed"})
	e.a2 = person("a2", "enabled", map[int]string{e.aList: "confirmed"})
	e.a3 = person("a3", "blocklisted", map[int]string{e.aList: "confirmed"})
	e.a4 = person("a4", "enabled", map[int]string{e.aList: "unsubscribed"})
	e.a5 = person("a5", "enabled", map[int]string{e.aList: "confirmed"})
	e.both = person("both", "enabled", map[int]string{e.aList: "confirmed", e.bList: "confirmed"})
	e.b1 = person("b1", "enabled", map[int]string{e.bList: "confirmed"})
	e.plain1 = person("plain1", "enabled", map[int]string{e.plainList: "confirmed"})

	// Team 가 (list A1) sent to a1 and "both" three days ago; team 나 (list B1) sent to b1 and a5 two days ago;
	// a2 received a team 가 campaign 20 days ago.
	sent := func(list int, age string, subs ...int) int {
		id, _ := e.addCampaign("finished")
		e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, id, list)
		for _, s := range subs {
			e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at) VALUES ($1,$2,NOW()-$3::interval)`, id, s, age)
		}
		return id
	}
	e.sentA = sent(e.aList, "3 days", e.a1, e.both)
	e.sentB = sent(e.bList, "2 days", e.b1, e.a5)
	sent(e.aList, "20 days", e.a2)
	e.draft, _ = e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.draft, e.aList)
	return e
}

func (e *recentListEnv) request(user auth.User, body string) (int, json.RawMessage) {
	e.t.Helper()
	h := e.a.auth.Perm(e.a.MakeRecentExcludeList, auth.PermCampaignsManageAll, auth.PermCampaignsManage)
	return e.call(e.a.listMaker(h), user, http.MethodPost, "/api/campaigns/recent-exclude/list", "", body)
}

func (e *recentListEnv) make(user auth.User, body string) models.RecentListResult {
	e.t.Helper()
	code, data := e.request(user, body)
	var out models.RecentListResult
	if code != http.StatusOK || json.Unmarshal(data, &out) != nil {
		e.t.Fatalf("make %s: %d %s", body, code, data)
	}
	return out
}

func (e *recentListEnv) members(list int) []int {
	return e.ids(`SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 AND status='confirmed' ORDER BY subscriber_id`, list)
}

func (e *recentListEnv) lists() int {
	var n int
	e.get(&n, `SELECT COUNT(*) FROM lists`)
	return n
}

func TestRecentListMembersAndScopes(t *testing.T) {
	e := newRecentListEnv(t)
	body := func(rule string, lists ...int) string {
		return fmt.Sprintf(`{"list_ids":%s,"campaign_id":%d,%s}`, mustJSON(lists), e.draft, rule)
	}
	for _, tc := range []struct {
		name  string
		rule  string
		lists []int
		want  []int
		excl  int
	}{
		// Audience of list A1: a1, a2, a5, both (a3 blocked, a4 unsubscribed).
		{"all teams", `"days":14,"scope":"all"`, []int{e.aList}, []int{e.a2}, 3},
		{"seven days", `"days":7,"scope":"all"`, []int{e.aList}, []int{e.a2}, 3},
		{"same team", `"days":14,"scope":"team"`, []int{e.aList}, []int{e.a2, e.a5}, 2},
		{"thirty days", `"days":30,"scope":"team"`, []int{e.aList}, []int{e.a5}, 3},
		{"chosen campaign", fmt.Sprintf(`"days":14,"scope":"campaigns","campaign_ids":[%d]`, e.sentB), []int{e.aList}, []int{e.a1, e.a2, e.both}, 1},
		// Two source lists share "both", who must appear once.
		{"two lists", `"days":14,"scope":"team"`, []int{e.aList, e.bList}, []int{e.a2}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, data := e.call(e.a.auth.Perm(e.a.PreviewRecentExclusion, auth.PermCampaignsManageAll, auth.PermCampaignsManage),
				e.admin, http.MethodPost, "/preview", "", body(tc.rule, tc.lists...))
			var preview models.RecentCounts
			if code != 200 || json.Unmarshal(data, &preview) != nil {
				t.Fatalf("preview %d %s", code, data)
			}
			out := e.make(e.admin, body(tc.rule, tc.lists...)[:len(body(tc.rule, tc.lists...))-1]+`,"team":"팀:가"}`)
			got := e.members(out.List.ID)
			if !sameInts(got, tc.want) {
				t.Fatalf("members %v, want %v (counts %+v, preview %+v)", got, tc.want, out.Counts, preview)
			}
			if out.Counts.Target != preview.Target || out.Counts.Excluded != preview.Excluded || out.Counts.Added != preview.WillSend ||
				out.Counts.Added != len(got) || out.List.SubscriberCount != len(got) {
				t.Fatalf("counts %+v against preview %+v and %d rows", out.Counts, preview, len(got))
			}
			if out.List.Type != "private" || out.List.Optin != "single" || out.List.Status != "active" ||
				!sameSet(out.List.Tags, []string{models.RecentListTag, "팀:가"}) {
				t.Fatalf("list %+v", out.List)
			}
			var stored struct {
				Description string
				Preset      string
			}
			e.get(&stored, `SELECT description, axm_preset AS preset FROM lists WHERE id=$1`, out.List.ID)
			for _, want := range []string{fmt.Sprintf("%d명", out.Counts.Excluded), fmt.Sprintf("%d명을 추가", out.Counts.Added), "A1 (#"} {
				if !strings.Contains(stored.Description, want) {
					t.Fatalf("description %q lacks %q", stored.Description, want)
				}
			}
		})
	}
}

func sameInts(a, b []int) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestRecentListSourceKindsAndNames(t *testing.T) {
	e := newRecentListEnv(t)
	var double, base, dynamic int
	e.get(&double, `INSERT INTO lists(uuid,name,type,optin,tags) VALUES(gen_random_uuid(),'Double','private','double','{팀:가}') RETURNING id`)
	e.get(&base, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Dynamic base','private','single') RETURNING id`)
	d1 := e.addSub("enabled", "unsubscribed")
	d2 := e.addSub("enabled", "unsubscribed")
	d3 := e.addSub("blocklisted", "unsubscribed")
	dyn := e.addSub("enabled", "unsubscribed")
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES ($1,$2,'confirmed'),($3,$2,'unconfirmed'),($4,$2,'confirmed'),($5,$6,'confirmed')`,
		d1, double, d2, d3, dyn, base)
	filter := fmt.Sprintf(`{"version":2,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[]}`, base)
	e.get(&dynamic, `INSERT INTO lists(uuid,name,type,optin,axm_filter) VALUES (gen_random_uuid(),'Dynamic','private','single',$1::jsonb) RETURNING id`, filter)

	out := e.make(e.admin, fmt.Sprintf(`{"list_ids":[%d,%d],"days":14,"scope":"all","no_team":true}`, double, dynamic))
	// Double opt-in: only confirmed; blocked excluded; unconfirmed left out. Dynamic list follows its rule.
	wantSame(t, "double and dynamic members", e.members(out.List.ID), sortedInts(d1, dyn))
	if out.List.Name != "Double 외 1개 · 최근 14일 제외 · "+out.AsOf.Format("2006-01-02") {
		t.Fatalf("default name %q", out.List.Name)
	}
	// The same default name again gets a number instead of colliding.
	again := e.make(e.admin, fmt.Sprintf(`{"list_ids":[%d,%d],"days":14,"scope":"all","no_team":true}`, double, dynamic))
	if again.List.Name != out.List.Name+" (2)" {
		t.Fatalf("second name %q", again.List.Name)
	}
	named := e.make(e.admin, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true,"name":"  내 리스트  "}`, e.plainList))
	if named.List.Name != "내 리스트" {
		t.Fatalf("custom name %q", named.List.Name)
	}
	long := strings.Repeat("가", 201)
	if code, _ := e.request(e.admin, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true,"name":%q}`, e.plainList, long)); code != http.StatusBadRequest {
		t.Fatalf("name longer than 200: %d", code)
	}
}

func sortedInts(ids ...int) []int {
	slices.Sort(ids)
	return ids
}

func TestRecentListEmptyAndInvalid(t *testing.T) {
	e := newRecentListEnv(t)
	before := e.lists()
	// Everybody on the plain list's audience received a campaign: nothing would be left.
	id, _ := e.addCampaign("finished")
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES ($1,$2)`, id, e.plain1)
	for _, body := range []string{
		fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true}`, e.plainList), // 0 left
		`{"days":14,"scope":"all","no_team":true}`,                                           // no lists
		fmt.Sprintf(`{"list_ids":[%d],"days":9,"scope":"all","no_team":true}`, e.aList),      // bad days
		fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"nope","no_team":true}`, e.aList),    // bad scope
		fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"campaigns","no_team":true}`, e.aList),
		fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"team","no_team":true}`, e.plainList), // no team tag
		fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true}`, 987654),       // unknown list
		fmt.Sprintf(`{"list_ids":[0],"days":14,"scope":"all","no_team":true}`),
	} {
		if code, _ := e.request(e.admin, body); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, code)
		}
	}
	if after := e.lists(); after != before {
		t.Fatalf("a refused request left %d new lists", after-before)
	}
	var memberships int
	e.get(&memberships, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id NOT IN ($1,$2,$3,$4,$5)`, e.listID, e.aList, e.bList, e.bothList, e.plainList)
	if memberships != 0 {
		t.Fatalf("stray memberships: %d", memberships)
	}
}

func TestRecentListPermissionsAndTeams(t *testing.T) {
	e := newRecentListEnv(t)
	readOnly := e.as(e.ua)
	delete(readOnly.PermissionsMap, auth.PermCampaignsManage)
	plain := e.as(e.plain)
	other := e.as(e.ub)
	ownBody := fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","campaign_id":%d}`, e.aList, e.draft)
	before := e.lists()

	for _, tc := range []struct {
		name   string
		user   auth.User
		body   string
		status int
	}{
		{"read-only campaign user", readOnly, ownBody, http.StatusForbidden},
		{"no list-making permission", plain, ownBody, http.StatusForbidden},
		{"other team's campaign", other, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","campaign_id":%d}`, e.bList, e.draft), http.StatusForbidden},
		{"a list the member cannot see among the sources", e.member, fmt.Sprintf(`{"list_ids":[%d,%d],"days":14,"scope":"all","campaign_id":%d}`, e.aList, e.bList, e.draft), http.StatusForbidden},
		{"the member's own list is fine", e.member, ownBody, http.StatusOK},
	} {
		if code, data := e.request(tc.user, tc.body); code != tc.status {
			t.Fatalf("%s: status %d (%s), want %d", tc.name, code, data, tc.status)
		}
	}
	if e.lists() != before+1 {
		t.Fatalf("expected exactly the one allowed list to exist: %d -> %d", before, e.lists())
	}

	// Campaign state: only a draft or scheduled campaign can have its target changed.
	for status, want := range map[string]int{"draft": 200, "scheduled": 200, "paused": 400, "running": 400, "finished": 400, "cancelled": 400} {
		id, _ := e.addCampaign(status)
		e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, id, e.aList)
		body := fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","campaign_id":%d}`, e.aList, id)
		if code, data := e.request(e.member, body); code != want {
			t.Fatalf("%s campaign: status %d (%s), want %d", status, code, data, want)
		}
	}
	optin, _ := e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaigns SET type='optin' WHERE id=$1`, optin)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, optin, e.aList)
	if code, _ := e.request(e.member, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","campaign_id":%d}`, e.aList, optin)); code != http.StatusBadRequest {
		t.Fatalf("opt-in campaign: %d", code)
	}
	// A campaign that does not exist yet (campaign_id 0) is the "new campaign" flow.
	if code, data := e.request(e.member, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all"}`, e.aList)); code != 200 {
		t.Fatalf("new campaign flow: %d %s", code, data)
	}

	// Teams. A member always gets their own team, whatever they ask for.
	mine := e.make(e.member, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","team":"팀:나"}`, e.aList))
	if !sameSet(mine.List.Tags, []string{models.RecentListTag, "팀:가"}) {
		t.Fatalf("member tags %v", mine.List.Tags)
	}
	two := fmt.Sprintf(`"list_ids":[%d,%d],"days":14,"scope":"all"`, e.aList, e.bList)
	for body, want := range map[string][]string{
		"{" + two + "}":                {models.RecentListTag, "팀:가", "팀:나"},
		"{" + two + `,"team":"팀:나"}`:   {models.RecentListTag, "팀:나"},
		"{" + two + `,"no_team":true}`: {models.RecentListTag},
	} {
		if got := e.make(e.admin, body).List.Tags; !sameSet(got, want) {
			t.Fatalf("admin %s: tags %v, want %v", body, got, want)
		}
	}
	if code, _ := e.request(e.admin, "{"+two+`,"team":"팀:가","no_team":true}`); code != http.StatusBadRequest {
		t.Fatalf("team and no team together: %d", code)
	}
	if code, _ := e.request(e.admin, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all"}`, e.plainList)); code != http.StatusBadRequest {
		t.Fatalf("admin without any team choice for a team-less source: %d", code)
	}
}

func TestRecentListAuditAndAudience(t *testing.T) {
	e := newRecentListEnv(t)
	body := fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"team","campaign_id":%d}`, e.aList, e.draft)
	out := e.make(e.member, body)

	rows := e.audits("action = $1 AND target_id = $2", auditListCreate, out.List.ID)
	if len(rows) != 1 {
		t.Fatalf("audit rows: %+v", rows)
	}
	var detail struct {
		From struct {
			Campaign struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"campaign"`
			Lists  []struct{ ID int }      `json:"lists"`
			Days   int                     `json:"days"`
			Scope  string                  `json:"scope"`
			Counts models.RecentListCounts `json:"counts"`
		} `json:"made_from_recent_exclude"`
	}
	if err := json.Unmarshal(rows[0].Detail, &detail); err != nil {
		t.Fatal(err)
	}
	f := detail.From
	if f.Campaign.ID != e.draft || len(f.Lists) != 1 || f.Lists[0].ID != e.aList || f.Days != 14 || f.Scope != "team" ||
		f.Counts != out.Counts || rows[0].Count == nil || *rows[0].Count != out.Counts.Added {
		t.Fatalf("audit detail %s", rows[0].Detail)
	}

	// Swapping the campaign's target to the made list gives exactly that audience.
	// The screen does this with the normal campaign update; the audience is then read from what the
	// campaign stores, the way the sender does.
	camp, err := e.core.GetCampaign(e.draft, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.core.UpdateCampaign(e.draft, camp, []int{out.List.ID}, nil); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "campaign lists", e.ids(`SELECT list_id FROM campaign_lists WHERE campaign_id=$1 ORDER BY 1`, e.draft), []int{out.List.ID})
	audience := func() []int {
		return e.ids(`SELECT subscriber_id FROM axm_campaign_audience(ARRAY(SELECT list_id FROM campaign_lists WHERE campaign_id=$1)) ORDER BY 1`, e.draft)
	}
	wantSame(t, "campaign audience", audience(), e.members(out.List.ID))
	// Blocking a member after the list was made removes only that person from the audience.
	victim := e.members(out.List.ID)[0]
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, victim)
	got := audience()
	if len(got) != out.Counts.Added-1 || slices.Contains(got, victim) {
		t.Fatalf("audience after block: %v (victim %d)", got, victim)
	}
}

func TestRecentListSameTeamNeedsATeamAndNamesStayUnique(t *testing.T) {
	e := newRecentListEnv(t)
	before := e.lists()
	// A same-team rule cannot be paired with a list that has no team.
	body := fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"team","no_team":true}`, e.aList)
	if code, _ := e.request(e.admin, body); code != http.StatusBadRequest {
		t.Fatalf("same-team rule with no team: %d", code)
	}
	if e.lists() != before {
		t.Fatal("a refused request left a list")
	}
	// Other scopes may make a list without a team.
	if code, data := e.request(e.admin, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true}`, e.aList)); code != 200 {
		t.Fatalf("all-teams rule with no team: %d %s", code, data)
	}

	// Several makers at once, same name: every list gets its own name.
	const n = 4
	names := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			code, data := e.request(e.admin, fmt.Sprintf(`{"list_ids":[%d],"days":14,"scope":"all","no_team":true,"name":"동시 생성"}`, e.aList))
			var out models.RecentListResult
			if code != 200 || json.Unmarshal(data, &out) != nil {
				names <- fmt.Sprintf("ERROR %d %s", code, data)
				return
			}
			names <- out.List.Name
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		name := <-names
		if strings.HasPrefix(name, "ERROR") || seen[name] {
			t.Fatalf("name %q (seen %v)", name, seen)
		}
		seen[name] = true
	}
	for _, want := range []string{"동시 생성", "동시 생성 (2)", "동시 생성 (3)", "동시 생성 (4)"} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, seen)
		}
	}
}

// Review: the maker held a connection for its lock and asked the pool for another one for the
// transaction (and resolved teams on the pool while the transaction was open). With
// db.max_open = 1 a single request then waited for itself.
func TestRecentListWorksWithOneConnection(t *testing.T) {
	e := newRecentListEnv(t)
	e.db.SetMaxOpenConns(1)
	defer e.db.SetMaxOpenConns(0)
	for _, team := range []string{`"team":"팀:가"`, `"no_team":true`, ``} {
		body := fmt.Sprintf(`{"list_ids":[%d],"campaign_id":%d,"days":14,"scope":"all"`, e.aList, e.draft)
		if team != "" {
			body += "," + team
		}
		body += "}"
		type reply struct {
			code int
			data json.RawMessage
		}
		done := make(chan reply, 1)
		go func() { code, data := e.request(e.admin, body); done <- reply{code, data} }()
		select {
		case r := <-done:
			var out models.RecentListResult
			if r.code != http.StatusOK || json.Unmarshal(r.data, &out) != nil {
				t.Fatalf("%s: %d %s", team, r.code, r.data)
			}
			if !sameInts(e.members(out.List.ID), []int{e.a2}) {
				t.Fatalf("%s: members %v", team, e.members(out.List.ID))
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: making the list did not finish with one connection", team)
		}
	}
}

// Review: the teams were decided before the transaction, and the snapshot counted with the teams
// it saw. A team taken off the source list in between made a same-team list of everybody under
// the old team name. Now the transaction checks the teams and the decision is made again.
func TestRecentListTeamChangedWhileMaking(t *testing.T) {
	e := newRecentListEnv(t)
	rule := models.RecentRule{Days: 14, Scope: models.RecentScopeTeam, CampaignIDs: []int64{}}
	resolve := func(source []string) ([]string, error) { return source, nil }
	preview := func() models.RecentCounts {
		code, data := e.call(e.a.auth.Perm(e.a.PreviewRecentExclusion, auth.PermCampaignsManageAll, auth.PermCampaignsManage),
			e.admin, http.MethodPost, "/preview", "", fmt.Sprintf(`{"list_ids":[%d],"campaign_id":%d,"days":14,"scope":"team"}`, e.aList, e.draft))
		var out models.RecentCounts
		if code != http.StatusOK || json.Unmarshal(data, &out) != nil {
			t.Fatalf("preview %d %s", code, data)
		}
		return out
	}

	// The team is taken off: the same-team rule has nothing to count, and nothing is made.
	before := e.lists()
	changed := false
	_, err := e.a.core.MakeRecentExcludeList(context.Background(), []int{e.aList}, rule, e.admin.ID, core.RecentListOptions{
		ResolveTeams: resolve,
		AfterTeams: func() {
			if !changed {
				changed = true
				e.db.MustExec(`UPDATE lists SET tags = array_remove(tags, '팀:가') WHERE id=$1`, e.aList)
			}
		},
	})
	var he *echo.HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusBadRequest || e.lists() != before {
		t.Fatalf("team removed: err %v, lists %d -> %d", err, before, e.lists())
	}

	// The team is swapped: the list carries the new team and the count of the new team.
	e.db.MustExec(`UPDATE lists SET tags = array_append(tags, '팀:가') WHERE id=$1`, e.aList)
	changed = false
	out, err := e.a.core.MakeRecentExcludeList(context.Background(), []int{e.aList}, rule, e.admin.ID, core.RecentListOptions{
		ResolveTeams: resolve,
		AfterTeams: func() {
			if !changed {
				changed = true
				e.db.MustExec(`UPDATE lists SET tags = array_replace(tags, '팀:가', '팀:나') WHERE id=$1`, e.aList)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := preview()
	if !sameSet(out.List.Tags, []string{models.RecentListTag, "팀:나"}) || out.Counts.Target != want.Target ||
		out.Counts.Excluded != want.Excluded || len(e.members(out.List.ID)) != want.WillSend {
		t.Fatalf("team swapped: list %+v counts %+v, preview %+v, %d members", out.List.Tags, out.Counts, want, len(e.members(out.List.ID)))
	}
}

// Review: a source list moved to another team while the list was being made took the member's
// read access with it, but the retry read its people with the access checked at the start.
func TestRecentListSourceMovedAwayFromMember(t *testing.T) {
	e := newRecentListEnv(t)
	rule := models.RecentRule{Days: 14, Scope: models.RecentScopeAll, CampaignIDs: []int64{}}
	if got := e.member.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, []int{e.aList}); len(got) != 1 {
		t.Fatalf("the member reads list A1 at the start: %v", got)
	}
	before := e.lists()
	moved := false
	_, err := e.a.core.MakeRecentExcludeList(context.Background(), []int{e.aList}, rule, e.draft, core.RecentListOptions{
		UserID:       e.member.ID,
		ResolveTeams: func([]string) ([]string, error) { return []string{"팀:가"}, nil },
		AfterTeams: func() {
			if !moved {
				moved = true
				e.db.MustExec(`UPDATE lists SET tags = array_replace(tags, '팀:가', '팀:나') WHERE id=$1`, e.aList)
			}
		},
	})
	var he *echo.HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusForbidden || e.lists() != before {
		t.Fatalf("moved list: err %v, lists %d -> %d", err, before, e.lists())
	}
	var readable int
	e.get(&readable, `SELECT COUNT(*) FROM roles r JOIN users u ON r.parent_id = u.list_role_id WHERE u.id=$1 AND r.list_id=$2`, e.member.ID, e.aList)
	if readable != 0 {
		t.Fatalf("moving the list should take the member's access (%d roles left): the test does not test the move", readable)
	}

	// Without a move the same member makes the list.
	e.db.MustExec(`UPDATE lists SET tags = array_replace(tags, '팀:나', '팀:가') WHERE id=$1`, e.aList)
	out, err := e.a.core.MakeRecentExcludeList(context.Background(), []int{e.aList}, rule, e.draft, core.RecentListOptions{
		UserID:       e.member.ID,
		ResolveTeams: func([]string) ([]string, error) { return []string{"팀:가"}, nil },
	})
	if err != nil || !sameInts(e.members(out.List.ID), []int{e.a2}) {
		t.Fatalf("member: err %v, members %v", err, e.members(out.List.ID))
	}
}
