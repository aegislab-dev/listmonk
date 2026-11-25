package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
)

// LST-68: a user who may not read all lists sees and deletes only the bounce records of people
// who are on a list they read, and blocklists / deletes only people who are not locked (LST-42).

// bounceEnv is the boundary environment (s1..s6, see newBoundaryEnv) with s7 on team 나's list
// only and s8 on no list, one bounce each. s1 has the bounce of campaign campA (on A1), s2 of
// campB (on B1).
type bounceEnv struct {
	*boundaryEnv
	s7, s8       int
	campA, campB int
	b            map[int]int // subscriber -> bounce ID
}

func (e *bounceEnv) campaign(name string, list int) int {
	var id int
	e.get(&id, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),$1,'S','test@example.invalid','b','email') RETURNING id`, name)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) SELECT $1, id, name FROM lists WHERE id = $2`, id, list)
	return id
}

func newBounceEnv(t *testing.T) *bounceEnv {
	e := &bounceEnv{boundaryEnv: newBoundaryEnv(t), b: map[int]int{}}
	e.s7 = e.person("s7", e.bList, "confirmed")
	e.s8 = e.person("s8")
	e.campA, e.campB = e.campaign("CampA", e.aList), e.campaign("CampB", e.bList)
	camp := map[int]int{e.s1: e.campA, e.s2: e.campB}
	types := map[int]string{e.s1: "hard", e.s2: "soft", e.s3: "complaint", e.s4: "hard", e.s5: "soft", e.s6: "hard", e.s7: "soft", e.s8: "hard"}
	at := 1700000000
	for i, s := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6, e.s7, e.s8} {
		var c any
		if camp[s] > 0 {
			c = camp[s]
		}
		var id int
		e.get(&id, `INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta, created_at) VALUES ($1, $2, $3::bounce_type, $4, '{}', to_timestamp($5)) RETURNING id`,
			s, c, types[s], "src"+string(rune('a'+i)), at+i*100)
		e.b[s] = id
	}
	return e
}

type bouncePage struct {
	Results []models.Bounce `json:"results"`
	Total   int             `json:"total"`
}

func (e *bounceEnv) list(user int, target string) (bouncePage, string) {
	e.t.Helper()
	code, data := e.call(e.a.GetBounces, e.as(user), "GET", target, "", "")
	if code != 200 {
		e.t.Fatalf("%s: %d", target, code)
	}
	var p bouncePage
	json.Unmarshal(data, &p)
	return p, string(data)
}

func (e *bounceEnv) emails(p bouncePage) []string {
	out := []string{}
	for _, b := range p.Results {
		out = append(out, b.Email)
	}
	return out
}

func (e *bounceEnv) left() []string {
	var out []string
	e.db.Select(&out, `SELECT s.name FROM bounces b JOIN subscribers s ON s.id = b.subscriber_id ORDER BY s.name`)
	return out
}

func nameSet(want ...string) []string { return want }

func (e *bounceEnv) emailOf(ids ...int) []string {
	out := []string{}
	for _, id := range ids {
		var em string
		e.get(&em, `SELECT email FROM subscribers WHERE id = $1`, id)
		out = append(out, em)
	}
	return out
}

func TestBouncesListIsScoped(t *testing.T) {
	e := newBounceEnv(t)
	ua, ub := e.as(e.ua), e.as(e.ub)
	_ = ua
	_ = ub

	// Admin and Super Admin: everything, as upstream.
	for _, u := range []int{e.admin, e.superAdmin} {
		if p, _ := e.list(u, "/api/bounces"); p.Total != 8 || len(p.Results) != 8 {
			t.Fatalf("admin sees %d (total %d)", len(p.Results), p.Total)
		}
	}
	// Team 가 reads A1 and Both: s1..s6. s7 (only on team 나's list) and s8 (on no list) are not theirs.
	p, raw := e.list(e.ua, "/api/bounces")
	if !sameSet(e.emails(p), e.emailOf(e.s1, e.s2, e.s3, e.s4, e.s5, e.s6)) || p.Total != 6 {
		t.Fatalf("team 가: %v total %d", e.emails(p), p.Total)
	}
	if strings.Contains(raw, "s7@") || strings.Contains(raw, "s8@") {
		t.Fatalf("the answer names somebody outside: %s", raw)
	}
	// Team 나 reads B1 and Both: s2, s4 (unsubscribed from B1 still has the row), s5, s6, s7.
	p, _ = e.list(e.ub, "/api/bounces")
	if !sameSet(e.emails(p), e.emailOf(e.s2, e.s4, e.s5, e.s6, e.s7)) || p.Total != 5 {
		t.Fatalf("team 나: %v total %d", e.emails(p), p.Total)
	}
	// An ordinary list role on A1 (not a team member): the same rule.
	p, _ = e.list(e.plain, "/api/bounces")
	if !sameSet(e.emails(p), e.emailOf(e.s1, e.s2, e.s3, e.s4, e.s5)) {
		t.Fatalf("plain: %v", e.emails(p))
	}

	// The total and the pages follow the scope.
	p, _ = e.list(e.ua, "/api/bounces?per_page=4&page=2")
	if p.Total != 6 || len(p.Results) != 2 {
		t.Fatalf("page 2: %d of %d", len(p.Results), p.Total)
	}
	// Filters.
	p, _ = e.list(e.ua, "/api/bounces?campaign_id="+itoa(e.campB))
	if len(p.Results) != 1 || p.Results[0].SubscriberID != e.s2 {
		t.Fatalf("campaign filter: %v", e.emails(p))
	}
	p, _ = e.list(e.ua, "/api/bounces?source=srcg") // s7's source
	if len(p.Results) != 0 {
		t.Fatalf("source filter shows an outsider: %v", e.emails(p))
	}
	p, _ = e.list(e.ub, "/api/bounces?source=srcg")
	if len(p.Results) != 1 || p.Results[0].SubscriberID != e.s7 {
		t.Fatalf("source filter: %v", e.emails(p))
	}
}

// The five sort fields give the order of the upstream query, restricted to what the user sees.
func TestBouncesScopedSort(t *testing.T) {
	e := newBounceEnv(t)
	mine := map[string]bool{}
	for _, em := range e.emailOf(e.s1, e.s2, e.s3, e.s4, e.s5, e.s6) {
		mine[em] = true
	}
	for _, f := range []string{"email", "source", "created_at"} {
		for _, dir := range []string{"asc", "desc"} {
			q := "/api/bounces?order_by=" + f + "&order=" + dir
			all, _ := e.list(e.admin, q)
			want := []string{}
			for _, b := range all.Results {
				if mine[b.Email] {
					want = append(want, b.Email)
				}
			}
			got, _ := e.list(e.ua, q)
			if strings.Join(want, ",") != strings.Join(e.emails(got), ",") {
				t.Fatalf("%s %s: want %v got %v", f, dir, want, e.emails(got))
			}
		}
	}
	// type: by the enum order, either way.
	for _, dir := range []string{"asc", "desc"} {
		got, _ := e.list(e.ua, "/api/bounces?order_by=type&order="+dir)
		rank := map[string]int{"soft": 0, "hard": 1, "complaint": 2}
		var ranks []int
		for _, b := range got.Results {
			ranks = append(ranks, rank[b.Type])
		}
		if !sort.IntsAreSorted(ranks) && dir == "asc" {
			t.Fatalf("type asc: %v", ranks)
		}
		if dir == "desc" && !sort.IsSorted(sort.Reverse(sort.IntSlice(ranks))) {
			t.Fatalf("type desc: %v", ranks)
		}
	}
	// campaign_name works for the scoped query (it fails in upstream's, which names a column the query lacks).
	if got, _ := e.list(e.ua, "/api/bounces?order_by=campaign_name&order=asc"); len(got.Results) != 6 {
		t.Fatalf("campaign_name: %d", len(got.Results))
	}
	// Anything else falls back to the default, and does not reach the statement.
	if got, _ := e.list(e.ua, "/api/bounces?order_by=1;DROP&order=up"); len(got.Results) != 6 {
		t.Fatalf("bad sort: %d", len(got.Results))
	}
}

func TestBounceGetOneIsScoped(t *testing.T) {
	e := newBounceEnv(t)
	get := func(user, sub int) (int, string) {
		code, data := e.call(e.a.GetBounce, e.as(user), "GET", "/x", itoa(e.b[sub]), "")
		return code, string(data)
	}
	if code, _ := get(e.ua, e.s1); code != 200 {
		t.Fatalf("own: %d", code)
	}
	// Outside the reach is answered like a bounce that does not exist.
	code, data := get(e.ua, e.s7)
	missing, _ := e.call(e.a.GetBounce, e.as(e.ua), "GET", "/x", "999999", "")
	if code != http.StatusBadRequest || code != missing || strings.Contains(data, "s7@") {
		t.Fatalf("outside: %d %s (missing: %d)", code, data, missing)
	}
	if code, _ := get(e.admin, e.s8); code != 200 {
		t.Fatalf("admin: %d", code)
	}
}

// A campaign shows only to a user who may read it: it sends to a list they read, or they have
// campaigns:get_all. s2's bounce is of campB (team 나's list).
func TestBounceCampaignIsMasked(t *testing.T) {
	e := newBounceEnv(t)
	campaignOf := func(user, sub int) string {
		code, data := e.call(e.a.GetBounce, e.as(user), "GET", "/x", itoa(e.b[sub]), "")
		if code != 200 {
			t.Fatalf("%d", code)
		}
		var b models.Bounce
		json.Unmarshal(data, &b)
		if b.Campaign == nil {
			return ""
		}
		var c struct{ Name string }
		json.Unmarshal(*b.Campaign, &c)
		return c.Name
	}
	if got := campaignOf(e.ua, e.s1); got != "CampA" {
		t.Fatalf("own team's campaign: %q", got)
	}
	if got := campaignOf(e.ua, e.s2); got != "" {
		t.Fatalf("the other team's campaign name is shown: %q", got)
	}
	if got := campaignOf(e.ub, e.s2); got != "CampB" {
		t.Fatalf("ub: %q", got)
	}
	p, raw := e.list(e.ua, "/api/bounces")
	if strings.Contains(raw, "CampB") {
		t.Fatalf("the list names the other team's campaign: %s", raw)
	}
	for _, b := range p.Results {
		if b.SubscriberID == e.s2 && b.Campaign != nil {
			t.Fatal("campaign not null")
		}
	}
	// The bounce tab in the subscriber window.
	tab := func(user, sub int) string {
		code, data := e.call(e.a.GetSubscriberBounces, e.as(user), "GET", "/x", itoa(sub), "")
		if code != 200 {
			t.Fatalf("tab: %d", code)
		}
		return string(data)
	}
	if got := tab(e.ua, e.s2); strings.Contains(got, "CampB") {
		t.Fatalf("the subscriber's bounce tab names the other team's campaign: %s", got)
	}
	if got := tab(e.ub, e.s2); !strings.Contains(got, "CampB") {
		t.Fatalf("ub: %s", got)
	}
	if got := tab(e.admin, e.s2); !strings.Contains(got, "CampB") {
		t.Fatalf("admin: %s", got)
	}
	// campaigns:get_all reads every campaign.
	var role, user int
	e.get(&role, `INSERT INTO roles(type,name,permissions) VALUES('user','camp-reader','{subscribers:get,campaigns:get_all}') RETURNING id`)
	e.get(&user, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES('camp-reader','cr@example.invalid','c',$1,$2,'enabled','user') RETURNING id`, role, e.team1)
	if got := campaignOf(user, e.s2); got != "CampB" {
		t.Fatalf("campaigns:get_all: %q", got)
	}
}

func TestBounceDeleteIsScoped(t *testing.T) {
	e := newBounceEnv(t)
	del := func(user int, ids ...int) int {
		q := "/api/bounces?"
		for _, id := range ids {
			q += "id=" + itoa(e.b[id]) + "&"
		}
		code, _ := e.call(e.a.DeleteBounces, e.as(user), "DELETE", q, "", "")
		return code
	}
	all := nameSet("s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8")
	if got := e.left(); !sameSet(got, all) {
		t.Fatalf("%v", got)
	}
	// Somebody outside the reach in the list: nothing is deleted, not even the own team's.
	if code := del(e.ua, e.s1, e.s7); code != http.StatusForbidden {
		t.Fatalf("outsider mixed in: %d", code)
	}
	if code := del(e.ua, e.s8); code != http.StatusForbidden {
		t.Fatalf("a subscriber on no list: %d", code)
	}
	if got := e.left(); !sameSet(got, all) {
		t.Fatalf("something was deleted: %v", got)
	}
	// Locked: s2 is on team 나's list too, s3 on a list without a team.
	for _, s := range []int{e.s2, e.s3, e.s4} {
		if code := del(e.ua, e.s1, s); code != http.StatusForbidden {
			t.Fatalf("locked %d: %d", s, code)
		}
	}
	if got := e.left(); !sameSet(got, all) {
		t.Fatalf("something was deleted: %v", got)
	}
	// s1 is team 가's alone; s5 and s6 are on the list both teams own, which team 가 manages too.
	if code := del(e.ua, e.s1, e.s5); code != 200 {
		t.Fatalf("own: %d", code)
	}
	if got := e.left(); !sameSet(got, nameSet("s2", "s3", "s4", "s6", "s7", "s8")) {
		t.Fatalf("%v", got)
	}
	// The single route follows the same rule.
	if code, _ := e.call(e.a.DeleteBounce, e.as(e.ua), "DELETE", "/x", itoa(e.b[e.s7]), ""); code != http.StatusForbidden {
		t.Fatalf("single outsider: %d", code)
	}
	if code, _ := e.call(e.a.DeleteBounce, e.as(e.ua), "DELETE", "/x", itoa(e.b[e.s6]), ""); code != 200 {
		t.Fatalf("single own: %d", code)
	}
	// IDs that are no bounce are ignored.
	if code, _ := e.call(e.a.DeleteBounces, e.as(e.ua), "DELETE", "/api/bounces?id=999999", "", ""); code != 200 {
		t.Fatalf("no such bounce: %d", code)
	}
	// An admin deletes anybody's.
	if code := del(e.admin, e.s8, e.s7); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if got := e.left(); !sameSet(got, nameSet("s2", "s3", "s4")) {
		t.Fatalf("%v", got)
	}
}

func TestBounceDeleteAllIsScoped(t *testing.T) {
	e := newBounceEnv(t)
	code, data := e.call(e.a.DeleteBounces, e.as(e.ua), "DELETE", "/api/bounces?all=true", "", "")
	if code != 200 {
		t.Fatal(code)
	}
	var out struct{ Deleted, Skipped int }
	json.Unmarshal(data, &out)
	// Deleted: s1, s5, s6. Kept for being locked: s2, s3, s4. Out of reach and left alone: s7, s8.
	if out.Deleted != 3 || out.Skipped != 3 {
		t.Fatalf("%s", data)
	}
	if got := e.left(); !sameSet(got, nameSet("s2", "s3", "s4", "s7", "s8")) {
		t.Fatalf("%v", got)
	}
	// A member of team 나 deletes theirs: s7 now, and s2, s4 are locked for them too (A1).
	_, data = e.call(e.a.DeleteBounces, e.as(e.ub), "DELETE", "/api/bounces?all=true", "", "")
	json.Unmarshal(data, &out)
	if out.Deleted != 1 || out.Skipped != 2 {
		t.Fatalf("ub: %s", data)
	}
	if got := e.left(); !sameSet(got, nameSet("s2", "s3", "s4", "s8")) {
		t.Fatalf("%v", got)
	}
	if code, data := e.call(e.a.DeleteBounces, e.as(e.admin), "DELETE", "/api/bounces?all=true", "", ""); code != 200 || string(data) != "true" {
		t.Fatalf("admin: %d %s", code, data)
	}
	if got := e.left(); len(got) != 0 {
		t.Fatalf("admin deletes all: %v", got)
	}
}

func TestBounceBlocklistIsScoped(t *testing.T) {
	e := newBounceEnv(t)
	status := func() map[string]string {
		rows := []struct {
			Name   string `db:"name"`
			Status string `db:"status"`
		}{}
		e.db.Select(&rows, `SELECT name, status::TEXT FROM subscribers`)
		m := map[string]string{}
		for _, r := range rows {
			m[r.Name] = r.Status
		}
		return m
	}
	code, data := e.call(e.a.BlocklistBouncedSubscribers, e.as(e.ua), "PUT", "/api/bounces/blocklist", "", "")
	if code != 200 {
		t.Fatal(code)
	}
	var out struct{ Blocklisted, Skipped int }
	json.Unmarshal(data, &out)
	if out.Blocklisted != 3 || out.Skipped != 3 {
		t.Fatalf("%s", data)
	}
	st := status()
	for _, n := range []string{"s1", "s5", "s6"} {
		if st[n] != "blocklisted" {
			t.Fatalf("%s: %s", n, st[n])
		}
	}
	for _, n := range []string{"s2", "s3", "s4", "s7", "s8"} {
		if st[n] != "enabled" {
			t.Fatalf("%s should stay: %s", n, st[n])
		}
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscriber_lists sl JOIN subscribers s ON s.id = sl.subscriber_id WHERE s.name = 's1' AND sl.status != 'unsubscribed'`)
	if n != 0 {
		t.Fatal("a blocklisted subscriber stays subscribed")
	}
	if code, _ := e.call(e.a.BlocklistBouncedSubscribers, e.as(e.admin), "PUT", "/api/bounces/blocklist", "", ""); code != 200 {
		t.Fatal(code)
	}
	for name, s := range status() {
		if s != "blocklisted" {
			t.Fatalf("admin blocklists everybody: %s %s", name, s)
		}
	}
}

// Production's state first (the team role has lists:get_all: everybody counts as an admin for
// the bounce screen), then the state after the role is mended (the scope applies).
func TestBounceScopeFollowsTheRole(t *testing.T) {
	e := newBounceEnv(t)
	e.db.MustExec(`UPDATE roles SET permissions = '{subscribers:get,lists:get_all,lists:manage_all,subscribers:get_all}' WHERE id = $1`, e.memberRole)
	if p, _ := e.list(e.ua, "/api/bounces"); p.Total != 8 {
		t.Fatalf("before the mend: %d", p.Total)
	}
	e.db.MustExec(`UPDATE roles SET permissions = '{subscribers:get}' WHERE id = $1`, e.memberRole)
	if p, _ := e.list(e.ua, "/api/bounces"); p.Total != 6 {
		t.Fatalf("after the mend: %d", p.Total)
	}
}
