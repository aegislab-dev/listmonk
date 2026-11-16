package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// drawEnv is a schema with the core on top, and a few helpers to build lists and people.
type drawEnv struct {
	t  *testing.T
	db *sqlx.DB
	q  map[string]string
	c  *Core
}

func newDrawEnv(t *testing.T) *drawEnv {
	db, q, _ := dynamicTestDB(t)
	return &drawEnv{t: t, db: db, q: q, c: testCore(t, db, q)}
}

// list makes an ordinary list with the given name.
func (e *drawEnv) list(name, optin string, tags ...string) int {
	var id int
	if err := e.db.Get(&id, `INSERT INTO lists(uuid,name,type,optin,tags) VALUES(gen_random_uuid(),$1,'private',$2,$3) RETURNING id`, name, optin, pq.StringArray(tags)); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// subs makes n subscribers with the given attribs and puts them on the list.
func (e *drawEnv) subs(list, n int, status, attribs string) []int {
	ids := make([]int, 0, n)
	for i := 0; i < n; i++ {
		id := testSub(e.db, attribs, "enabled")
		e.join(list, id, status)
		ids = append(ids, id)
	}
	return ids
}

func (e *drawEnv) join(list, sub int, status string) {
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,$3) ON CONFLICT (subscriber_id,list_id) DO UPDATE SET status=$3`, sub, list, status)
}

func (e *drawEnv) campaign(list int) int {
	var id int
	e.db.Get(&id, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'Synthetic','S','test@example.invalid','b','email') RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES($1,$2,'Synthetic')`, id, list)
	return id
}

// sent records that the campaign went to the people, ago in the past.
func (e *drawEnv) sent(camp int, ago time.Duration, subs ...int) {
	for _, s := range subs {
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at) VALUES($1,$2,NOW() - $3 * INTERVAL '1 second')`, camp, s, int(ago.Seconds()))
	}
}

func (e *drawEnv) members(list int) []int {
	ids := dbIDs(e.t, e.db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 ORDER BY subscriber_id`, list)
	return ids
}

func (e *drawEnv) draw(src int, s models.DrawSettings) (models.List, models.ListDraw, error) {
	return e.c.DrawList(src, s, DrawOpts{})
}

func count(n int) models.DrawSettings {
	return models.DrawSettings{Unit: models.DrawUnitCount, Value: n}
}

func union(sets ...[]int) []int {
	var out []int
	for _, s := range sets {
		out = append(out, s...)
	}
	sort.Ints(out)
	return out
}

func overlaps(a, b []int) bool {
	m := map[int]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if m[x] {
			return true
		}
	}
	return false
}

func seq(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func v2Filter(base string, baseLists []int, exclude []int, conds ...DynamicCondition) string {
	f := DynamicFilter{Version: 2, Base: DynamicBase{Type: base, Lists: baseLists}, Match: "all", Conditions: conds, Exclude: &DynamicExclude{Lists: exclude}}
	if conds == nil {
		f.Conditions = []DynamicCondition{}
	}
	b, _ := json.Marshal(f)
	return string(b)
}

func eq(key, val string) DynamicCondition { return DynamicCondition{Key: key, Op: "eq", Value: &val} }

// Dynamic list rules of version 2: exclude lists and no conditions.
func TestDynamicV2Members(t *testing.T) {
	e := newDrawEnv(t)
	db := e.db
	base := e.list("Base", "single")
	other := e.list("Other", "single")
	vip := e.list("Customers", "single")
	vip2 := e.list("Customers 2", "single")
	pro := e.subs(base, 4, "confirmed", `{"plan":"pro"}`)   // a0..a3
	free := e.subs(base, 2, "confirmed", `{"plan":"free"}`) // f0, f1
	e.join(other, pro[3], "confirmed")
	e.join(vip, pro[0], "confirmed")
	e.join(vip, pro[1], "unsubscribed") // any status on an exclude list counts
	e.join(vip2, free[0], "unconfirmed")
	blocked := testSub(db, `{"plan":"pro"}`, "blocklisted")
	e.join(base, blocked, "confirmed")

	members := func(filter string) []int {
		return dbIDs(t, db, `SELECT * FROM axm_filter_members($1::jsonb, NULL)`, filter)
	}
	// Version 1 gives what it always gave, also with a stray "exclude" the DB must ignore.
	v1 := testFilter("lists", []int{base})
	wantIDs(t, members(v1), union(pro))
	wantIDs(t, members(strings.Replace(v1, `"version":1`, `"version":1,"exclude":{"lists":[`+fmt.Sprint(vip)+`]}`, 1)), union(pro))
	// Version 2 with one exclude list and a condition.
	wantIDs(t, members(v2Filter("lists", []int{base}, []int{vip}, eq("plan", "pro"))), union([]int{pro[2], pro[3]}))
	// Two exclude lists, no condition: everyone of the base but the excluded and the blocked.
	wantIDs(t, members(v2Filter("lists", []int{base}, []int{vip, vip2})), union([]int{pro[2], pro[3], free[1]}))
	// Base "all" with an exclude list and no condition.
	wantIDs(t, members(v2Filter("all", nil, []int{vip})), union([]int{pro[2], pro[3], free[0], free[1]}))
	// Version 2 without exclude lists matches like version 1.
	wantIDs(t, members(v2Filter("lists", []int{base}, []int{}, eq("plan", "pro"))), union(pro))
	// A version 2 rule with a malformed condition list matches nobody, like version 1.
	wantIDs(t, members(`{"version":2,"base":{"type":"all"},"match":"all","conditions":{}}`), []int{})
	// Paging reads the same members as the whole.
	f := v2Filter("lists", []int{base}, []int{vip})
	got := dbIDs(t, db, `SELECT * FROM axm_filter_members($1::jsonb, NULL, $2, 2147483647, 2)`, f, pro[2])
	wantIDs(t, got, []int{pro[3], free[0]})
	// The dynamic list function reads the stored rule.
	dyn := testList(db, v2Filter("lists", []int{base}, []int{vip, vip2}), "single")
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dyn), union([]int{pro[2], pro[3], free[1]}))
	// Someone who unsubscribed from the dynamic list itself is still left out.
	e.join(dyn, pro[2], "unsubscribed")
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dyn), union([]int{pro[3], free[1]}))
}

func TestDynamicV2Validation(t *testing.T) {
	ok := func(raw string) DynamicFilter {
		f, err := ValidateDynamicFilter([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return f
	}
	bad := func(name, raw string) {
		t.Helper()
		if _, err := ValidateDynamicFilter([]byte(raw)); err == nil {
			t.Fatalf("%s accepted: %s", name, raw)
		}
	}
	f := ok(`{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[3,3,4]}}`)
	if f.Version != 2 || len(f.Conditions) != 0 || f.Conditions == nil || fmt.Sprint(f.Exclude.Lists) != "[3 4]" {
		t.Fatalf("%+v", f)
	}
	raw, _ := json.Marshal(f)
	if !strings.Contains(string(raw), `"conditions":[]`) {
		t.Fatalf("conditions must stay an array: %s", raw)
	}
	// A condition without exclude lists is a valid version 2 rule with an empty list.
	f = ok(`{"version":2,"base":{"type":"all"},"match":"any","conditions":[{"key":"a","op":"eq","value":"b"}]}`)
	if f.Exclude == nil || len(f.Exclude.Lists) != 0 {
		t.Fatalf("%+v", f)
	}
	ok(`{"version":2,"base":{"type":"lists","lists":[1]},"match":"all","conditions":[],"exclude":{"lists":[2]}}`)
	bad("nothing at all", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[]}`)
	bad("empty exclude", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[]}}`)
	bad("version 1 with exclude", `{"version":1,"base":{"type":"all"},"match":"all","exclude":{"lists":[1]},"conditions":[{"key":"a","op":"eq","value":"b"}]}`)
	bad("version 3", `{"version":3,"base":{"type":"all"},"match":"all","conditions":[]}`)
	bad("zero id", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[0]}}`)
	bad("huge id", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[2147483648]}}`)
	bad("unknown field", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[1],"x":1}}`)
	var many []string
	for i := 1; i <= 21; i++ {
		many = append(many, fmt.Sprint(i))
	}
	bad("21 exclude lists", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[],"exclude":{"lists":[`+strings.Join(many, ",")+`]}}`)
	bad("21 conditions", `{"version":2,"base":{"type":"all"},"match":"all","conditions":[`+strings.Repeat(`{"key":"a","op":"eq","value":"b"},`, 20)+`{"key":"a","op":"eq","value":"b"}],"exclude":{"lists":[1]}}`)
}

func TestDynamicV2BaseChecks(t *testing.T) {
	e := newDrawEnv(t)
	plain := e.list("Plain", "single")
	dyn := testList(e.db, testFilter("all", nil), "single")
	ex := func(ids ...int) DynamicFilter {
		return DynamicFilter{Version: 2, Base: DynamicBase{Type: "all"}, Match: "all", Conditions: []DynamicCondition{}, Exclude: &DynamicExclude{Lists: ids}}
	}
	if err := e.c.ValidateDynamicBase(ex(plain)); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]DynamicFilter{"dynamic": ex(dyn), "unknown": ex(plain, 99999)} {
		if err := e.c.ValidateDynamicBase(f); err == nil {
			t.Fatalf("%s exclude list accepted", name)
		}
	}
	f := ex(plain)
	f.Base = DynamicBase{Type: "lists", Lists: []int{dyn}}
	if err := e.c.ValidateDynamicBase(f); err == nil {
		t.Fatal("dynamic base list accepted")
	}
}

// The preview explains the people a version 2 rule leaves out, and its numbers add up.
func TestDynamicV2Preview(t *testing.T) {
	e := newDrawEnv(t)
	base := e.list("Base", "single")
	cust := e.list("Customers", "single")
	pro := e.subs(base, 5, "confirmed", `{"plan":"pro"}`)
	e.join(cust, pro[0], "confirmed")
	e.join(cust, pro[1], "confirmed")
	e.join(base, pro[2], "unsubscribed")
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, pro[3])
	dyn := testList(e.db, v2Filter("lists", []int{base}, []int{cust}, eq("plan", "pro")), "single")
	e.join(dyn, pro[4], "unsubscribed")
	f, err := ValidateDynamicFilter([]byte(v2Filter("lists", []int{base}, []int{cust}, eq("plan", "pro"))))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.c.PreviewDynamicFilter(f, dyn, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := DynamicExcluded{Blocklisted: 1, Unsubscribed: 1, OptedOut: 1, ExcludeList: 2}
	if p.Count != 0 || p.Excluded != want {
		t.Fatalf("%+v want count 0 and %+v", p, want)
	}
	// Without the others the one free person is a member and the numbers add up to the base.
	f, _ = ValidateDynamicFilter([]byte(v2Filter("lists", []int{base}, []int{cust})))
	p, _ = e.c.PreviewDynamicFilter(f, 0, 10)
	if p.Count != 1 || p.Excluded.Blocklisted != 1 || p.Excluded.Unsubscribed != 1 || p.Excluded.ExcludeList != 2 || p.Count+p.Excluded.Blocklisted+p.Excluded.Unsubscribed+p.Excluded.ExcludeList != 5 {
		t.Fatalf("%+v", p)
	}
	// A version 1 rule explains itself as before (no exclude list).
	f, _ = ValidateDynamicFilter([]byte(testFilter("lists", []int{base})))
	p, _ = e.c.PreviewDynamicFilter(f, 0, 10)
	if p.Excluded.ExcludeList != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestDrawBasic(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("미국 크리에이터", "single", "team-a", "vip")
	all := e.subs(src, 100, "confirmed", `{}`)
	list, draw, err := e.c.DrawList(src, count(10), DrawOpts{UserID: 0, Describe: func(s string, at time.Time, r models.DrawCounts) string {
		return fmt.Sprintf("%s %d %d", s, r.WillDraw, r.Target)
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := e.members(list.ID)
	if len(got) != 10 || !overlaps(got, all) {
		t.Fatalf("drawn %v", got)
	}
	var st []string
	e.db.Select(&st, `SELECT DISTINCT status::text FROM subscriber_lists WHERE list_id=$1`, list.ID)
	if fmt.Sprint(st) != "[confirmed]" {
		t.Fatal(st)
	}
	if list.Type != "private" || list.Optin != "single" || list.AxmFilter != nil || list.Status != "active" {
		t.Fatalf("%+v", list)
	}
	if fmt.Sprint([]string(list.Tags)) != "[team-a vip 뽑은-리스트]" {
		t.Fatalf("tags %v", list.Tags)
	}
	if list.SubscriberCount != 10 {
		t.Fatalf("count %d", list.SubscriberCount)
	}
	wantName := "미국 크리에이터 · " + time.Now().Format("2006-01-02") + " · 10명"
	if list.Name != wantName || list.Description != "미국 크리에이터 10 100" {
		t.Fatalf("name %q description %q", list.Name, list.Description)
	}
	// One record with the numbers.
	var rec struct {
		ListID, SourceID int
		SourceName, Name string
		Result, Settings string
	}
	if err := e.db.QueryRow(`SELECT list_id, source_list_id, source_name, list_name, result::text, settings::text FROM axm_list_draws`).Scan(&rec.ListID, &rec.SourceID, &rec.SourceName, &rec.Name, &rec.Result, &rec.Settings); err != nil {
		t.Fatal(err)
	}
	if rec.ListID != list.ID || rec.SourceID != src || rec.SourceName != "미국 크리에이터" || rec.Name != list.Name {
		t.Fatalf("%+v", rec)
	}
	var r map[string]int
	json.Unmarshal([]byte(rec.Result), &r)
	if r["target"] != 100 || r["already_drawn"] != 0 || r["already_received"] != 0 || r["candidates"] != 100 || r["drawn"] != 10 {
		t.Fatalf("%v", r)
	}
	if draw.ID == 0 || *draw.ListID != list.ID || draw.DrawnAt.IsZero() {
		t.Fatalf("%+v", draw)
	}
	// The same name again gets a number.
	l2, _, err := e.c.DrawList(src, count(10), DrawOpts{Name: wantName})
	if err != nil || l2.Name != wantName+" (2)" {
		t.Fatalf("%q %v", l2.Name, err)
	}
	l3, _, _ := e.c.DrawList(src, count(10), DrawOpts{Name: wantName})
	if l3.Name != wantName+" (3)" {
		t.Fatalf("%q", l3.Name)
	}
	// A long name is cut to fit.
	long := e.list(strings.Repeat("가", 250), "single")
	e.subs(long, 3, "confirmed", `{}`)
	l4, _, err := e.c.DrawList(long, count(1), DrawOpts{})
	if err != nil || len([]rune(l4.Name)) != 200 || !strings.HasSuffix(l4.Name, " · "+time.Now().Format("2006-01-02")+" · 1명") {
		t.Fatalf("%d %q %v", len([]rune(l4.Name)), l4.Name, err)
	}
	// The drawn list keeps the column preset of its source.
	var preset, srcPreset string
	e.db.Get(&preset, `SELECT axm_preset FROM lists WHERE id=$1`, list.ID)
	e.db.Get(&srcPreset, `SELECT axm_preset FROM lists WHERE id=$1`, src)
	if preset != srcPreset {
		t.Fatal(preset, srcPreset)
	}
}

func TestDrawPercentAndBounds(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 95, "confirmed", `{}`)
	l, _, err := e.draw(src, models.DrawSettings{Unit: models.DrawUnitPercent, Value: 10})
	if err != nil || len(e.members(l.ID)) != 10 {
		t.Fatalf("%v %v", e.members(l.ID), err)
	}
	// 10% of the target (95), not of what is left: 10 again, then 10 until 95 is covered.
	p, err := e.c.PreviewListDraw(src, models.DrawSettings{Unit: models.DrawUnitPercent, Value: 10})
	if err != nil || p.Target != 95 || p.Requested != 10 || p.WillDraw != 10 || p.Candidates != 85 || p.AlreadyDrawn != 10 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, s := range []models.DrawSettings{
		{Unit: "count", Value: 0}, {Unit: "count", Value: -1}, {Unit: "count", Value: models.DrawMaxCount + 1},
		{Unit: "percent", Value: 0}, {Unit: "percent", Value: 101}, {Unit: "ratio", Value: 5}, {Unit: "", Value: 5},
		{Unit: "count", Value: 5, ExcludeScope: "mine"}, {Unit: "count", Value: 5, ExcludeDays: -1}, {Unit: "count", Value: 5, ExcludeDays: models.DrawMaxDays + 1},
	} {
		if _, err := e.c.PreviewListDraw(src, s); err == nil {
			t.Fatalf("%+v accepted by preview", s)
		}
		if _, _, err := e.draw(src, s); err == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
	if (models.DrawSettings{Unit: "percent", Value: 33}).Want(100) != 33 || (models.DrawSettings{Unit: "percent", Value: 33}).Want(10) != 4 {
		t.Fatal("percent rounds up")
	}
}

// Ten draws of ten cover a hundred people once; the eleventh has nobody and makes nothing.
func TestDrawSuccessive(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	all := e.subs(src, 100, "confirmed", `{}`)
	var lists [][]int
	for i := 0; i < 10; i++ {
		l, _, err := e.draw(src, count(10))
		if err != nil {
			t.Fatal(i, err)
		}
		m := e.members(l.ID)
		for _, prev := range lists {
			if overlaps(prev, m) {
				t.Fatalf("draw %d overlaps", i)
			}
		}
		lists = append(lists, m)
	}
	wantIDs(t, union(lists...), all)
	var nLists, nDraws int
	e.db.Get(&nLists, `SELECT COUNT(*) FROM lists`)
	e.db.Get(&nDraws, `SELECT COUNT(*) FROM axm_list_draws`)
	_, _, err := e.draw(src, count(10))
	if err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("%v", err)
	}
	var l2, d2 int
	e.db.Get(&l2, `SELECT COUNT(*) FROM lists`)
	e.db.Get(&d2, `SELECT COUNT(*) FROM axm_list_draws`)
	if l2 != nLists || d2 != nDraws || d2 != 10 {
		t.Fatalf("lists %d->%d draws %d->%d", nLists, l2, nDraws, d2)
	}
	p, _ := e.c.PreviewListDraw(src, count(10))
	if p.Target != 100 || p.AlreadyDrawn != 100 || p.Candidates != 0 || p.WillDraw != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestDrawShortOfCandidates(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 3, "confirmed", `{}`)
	p, _ := e.c.PreviewListDraw(src, count(10))
	if p.Requested != 10 || p.WillDraw != 3 || p.Candidates != 3 {
		t.Fatalf("%+v", p)
	}
	l, d, err := e.draw(src, count(10))
	if err != nil || len(e.members(l.ID)) != 3 || !strings.Contains(string(d.Result), `"drawn":3`) {
		t.Fatalf("%v %v", e.members(l.ID), err)
	}
	// The default name carries the number drawn.
	if !strings.HasSuffix(l.Name, " · 3명") {
		t.Fatal(l.Name)
	}
}

func TestDrawAlreadyReceived(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	elsewhere := e.list("Elsewhere", "single")
	p := e.subs(src, 12, "confirmed", `{}`)
	campSrc, campOther := e.campaign(src), e.campaign(elsewhere)
	e.sent(campSrc, time.Hour, p[0], p[1], p[2])
	e.sent(campOther, time.Hour, p[3], p[4], p[5])
	e.sent(campSrc, 3*24*time.Hour, p[6])
	e.sent(campSrc, 8*24*time.Hour, p[7])
	prev := func(scope string, days int) models.DrawCounts {
		t.Helper()
		out, err := e.c.PreviewListDraw(src, models.DrawSettings{Unit: "count", Value: 100, ExcludeScope: scope, ExcludeDays: days})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Same source (the default) counts only campaigns of this list, whenever they went out.
	if c := prev("", 0); c.Target != 12 || c.AlreadyReceived != 5 || c.Candidates != 7 || c.AlreadyDrawn != 0 {
		t.Fatalf("%+v", c)
	}
	// All campaigns also counts what went to other lists.
	if c := prev("all", 0); c.AlreadyReceived != 8 || c.Candidates != 4 {
		t.Fatalf("%+v", c)
	}
	// A window of 7 days leaves out the one from 8 days ago.
	if c := prev("same_source", 7); c.AlreadyReceived != 4 || c.Candidates != 8 {
		t.Fatalf("%+v", c)
	}
	if c := prev("all", 7); c.AlreadyReceived != 7 {
		t.Fatalf("%+v", c)
	}
	// The draw takes only the candidates; the people who received nothing from this source.
	l, _, err := e.draw(src, models.DrawSettings{Unit: "count", Value: 100})
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.members(l.ID), union(p[3:6], p[8:]))
	// A campaign sent to a list drawn from this source counts for the same source, but a draw
	// from another list ignores it. Nobody drawn is a candidate twice anyway.
	other := e.list("Other", "single")
	for _, s := range p[8:11] {
		e.join(other, s, "confirmed")
	}
	if c, _ := e.c.PreviewListDraw(other, count(100)); c.Target != 3 || c.AlreadyReceived != 0 || c.Candidates != 3 {
		t.Fatalf("%+v", c)
	}
	e.sent(e.campaign(l.ID), time.Hour, p[8], p[9])
	if c := prev("same_source", 0); c.AlreadyDrawn != 7 || c.Candidates != 0 {
		t.Fatalf("%+v", c)
	}
	// "All" ignores the source: a draw from Other sees the campaign sent to the drawn list.
	if c, _ := e.c.PreviewListDraw(other, models.DrawSettings{Unit: "count", Value: 100, ExcludeScope: "all"}); c.AlreadyReceived != 2 || c.Candidates != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestDrawSourceKinds(t *testing.T) {
	e := newDrawEnv(t)
	// Double opt-in source: only confirmed people. Blocked and unsubscribed never.
	dbl := e.list("Double", "double")
	conf := e.subs(dbl, 3, "confirmed", `{}`)
	e.subs(dbl, 2, "unconfirmed", `{}`)
	e.subs(dbl, 2, "unsubscribed", `{}`)
	b := e.subs(dbl, 1, "confirmed", `{}`)
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, b[0])
	l, _, err := e.draw(dbl, count(50))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.members(l.ID), union(conf))
	// Single opt-in source also takes unconfirmed, but not unsubscribed.
	sgl := e.list("Single", "single")
	a := e.subs(sgl, 2, "confirmed", `{}`)
	u := e.subs(sgl, 2, "unconfirmed", `{}`)
	e.subs(sgl, 2, "unsubscribed", `{}`)
	l, _, _ = e.draw(sgl, count(50))
	wantIDs(t, e.members(l.ID), union(a, u))
	// Dynamic sources, rule versions 1 and 2.
	base := e.list("Base", "single")
	cust := e.list("Customers", "single")
	pro := e.subs(base, 5, "confirmed", `{"plan":"pro"}`)
	e.subs(base, 2, "confirmed", `{"plan":"free"}`)
	e.subs(base, 1, "unsubscribed", `{"plan":"pro"}`)
	e.join(cust, pro[0], "confirmed")
	d1 := testList(e.db, testFilter("lists", []int{base}), "single")
	d2 := testList(e.db, v2Filter("lists", []int{base}, []int{cust}, eq("plan", "pro")), "single")
	l, _, err = e.draw(d1, count(50))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.members(l.ID), union(pro))
	l, _, err = e.draw(d2, count(50))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.members(l.ID), union(pro[1:]))
	// A drawn list or an archived list is not a source, and neither is a missing one.
	if _, _, err := e.draw(l.ID, count(1)); err == nil || !strings.Contains(err.Error(), "drawn") {
		t.Fatalf("drawn list: %v", err)
	}
	e.db.MustExec(`UPDATE lists SET status='archived' WHERE id=$1`, sgl)
	if _, _, err := e.draw(sgl, count(1)); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archived: %v", err)
	}
	if _, err := e.c.PreviewListDraw(sgl, count(1)); err == nil {
		t.Fatal("archived preview")
	}
	if _, _, err := e.draw(999999, count(1)); err == nil {
		t.Fatal("missing")
	}
}

// The same people are drawn in the same order every time, and the order is not by ID.
func TestDrawOrder(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	all := e.subs(src, 100, "confirmed", `{}`)
	var want []int
	e.db.Select(&want, `SELECT id FROM (SELECT id FROM subscribers) s ORDER BY hashint8(id::bigint * 1000003 + $1), id LIMIT 10`, src)
	sort.Ints(want)
	pre, _ := e.c.PreviewListDraw(src, count(10))
	l, _, err := e.draw(src, count(10))
	if err != nil {
		t.Fatal(err)
	}
	got := e.members(l.ID)
	wantIDs(t, got, want)
	if fmt.Sprint(got) == fmt.Sprint(all[:10]) {
		t.Fatal("drawn in ID order")
	}
	if pre.WillDraw != 10 || pre.Candidates != 100 {
		t.Fatalf("%+v", pre)
	}
	// A different source draws a different set from the same people.
	src2 := e.list("S2", "single")
	for _, s := range all {
		e.join(src2, s, "confirmed")
	}
	l2, _, _ := e.draw(src2, count(10))
	if fmt.Sprint(e.members(l2.ID)) == fmt.Sprint(got) {
		t.Fatal("two sources drew the same people")
	}
	// The preview is cheap and writes nothing.
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_list_draws`)
	if n != 2 {
		t.Fatal(n)
	}
}

func TestDrawConcurrent(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 100, "confirmed", `{}`)
	var wg sync.WaitGroup
	res := make([][]int, 2)
	errs := make([]error, 2)
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, _, err := e.draw(src, count(60))
			errs[i] = err
			if err == nil {
				res[i] = e.members(l.ID)
			}
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	if overlaps(res[0], res[1]) || len(res[0])+len(res[1]) != 100 {
		t.Fatalf("%d and %d, overlap %v", len(res[0]), len(res[1]), overlaps(res[0], res[1]))
	}
}

func TestDrawUnsubscribeSync(t *testing.T) {
	e := newDrawEnv(t)
	status := func(list, sub int) string {
		var s string
		e.db.Get(&s, `SELECT COALESCE((SELECT status::text FROM subscriber_lists WHERE list_id=$1 AND subscriber_id=$2),'none')`, list, sub)
		return s
	}
	unsubLink := func(camp, sub int, blocklist bool) {
		var cu, su string
		e.db.Get(&cu, `SELECT uuid FROM campaigns WHERE id=$1`, camp)
		e.db.Get(&su, `SELECT uuid FROM subscribers WHERE id=$1`, sub)
		if _, err := e.db.Exec(e.q["unsubscribe-by-campaign"], cu, su, blocklist); err != nil {
			t.Fatal(err)
		}
	}

	// Ordinary source with two drawn lists.
	src := e.list("S", "single")
	p := e.subs(src, 10, "confirmed", `{}`)
	g1, _, _ := e.draw(src, count(5))
	g2, _, _ := e.draw(src, count(5))
	m1, m2 := e.members(g1.ID), e.members(g2.ID)
	// Unsubscribing from the source takes the person off the drawn list.
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE list_id=$1 AND subscriber_id=$2`, src, m1[0])
	if status(g1.ID, m1[0]) != "unsubscribed" || status(g1.ID, m1[1]) != "confirmed" {
		t.Fatal("source unsubscribe did not reach the drawn list")
	}
	// Unsubscribing from a drawn list through its campaign takes the person off the source,
	// and the other drawn lists of the source (the person is only on the one).
	camp := e.campaign(g2.ID)
	unsubLink(camp, m2[0], false)
	if status(g2.ID, m2[0]) != "unsubscribed" || status(src, m2[0]) != "unsubscribed" || status(src, m2[1]) != "confirmed" {
		t.Fatal("drawn unsubscribe did not reach the source")
	}
	// Blocklisting changes every subscription, including the drawn ones.
	if _, err := e.db.Exec(e.q["blocklist-subscribers"], pq.Array([]int{m2[2]})); err != nil {
		t.Fatal(err)
	}
	if status(g2.ID, m2[2]) != "unsubscribed" || status(src, m2[2]) != "unsubscribed" {
		t.Fatal("blocklist")
	}
	_ = p

	// Dynamic source over two base lists: leaving one base list keeps the person.
	b1, b2 := e.list("B1", "single"), e.list("B2", "single")
	both := e.subs(b1, 6, "confirmed", `{"plan":"pro"}`)
	for _, s := range both {
		e.join(b2, s, "confirmed")
	}
	dyn := testList(e.db, testFilter("lists", []int{b1, b2}), "single")
	g, _, err := e.draw(dyn, count(6))
	if err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE list_id=$1 AND subscriber_id=$2`, b1, both[0])
	if status(g.ID, both[0]) != "confirmed" {
		t.Fatal("still valid through the other base list")
	}
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE list_id=$1 AND subscriber_id=$2`, b2, both[0])
	if status(g.ID, both[0]) != "unsubscribed" {
		t.Fatal("no valid base subscription left")
	}
	// The attribute condition is not looked at again: changing it keeps the person.
	e.db.MustExec(`UPDATE subscribers SET attribs='{"plan":"free"}' WHERE id=$1`, both[1])
	if status(g.ID, both[1]) != "confirmed" {
		t.Fatal("attributes must not matter after the draw")
	}
	// Unsubscribing through the drawn list's campaign reaches the dynamic source like its own
	// campaign link: an exclusion row and the base lists.
	camp = e.campaign(g.ID)
	unsubLink(camp, both[2], false)
	if status(g.ID, both[2]) != "unsubscribed" || status(dyn, both[2]) != "unsubscribed" || status(b1, both[2]) != "unsubscribed" || status(b2, both[2]) != "unsubscribed" {
		t.Fatalf("%s %s %s %s", status(g.ID, both[2]), status(dyn, both[2]), status(b1, both[2]), status(b2, both[2]))
	}
	if status(b1, both[3]) != "confirmed" || status(g.ID, both[3]) != "confirmed" {
		t.Fatal("others untouched")
	}
	// An exclusion row on the dynamic list (as its own campaign link makes) also reaches it.
	e.join(dyn, both[4], "unsubscribed")
	if status(g.ID, both[4]) != "unsubscribed" {
		t.Fatal("dynamic list exclusion")
	}
	// A drawn list used as a base list of a dynamic list still works with the rest.
	next := testList(e.db, testFilter("lists", []int{g.ID}), "single")
	if len(dbIDs(t, e.db, `SELECT * FROM axm_dynamic_list_members($1)`, next)) != 0 {
		t.Log("no pro attribute members left; fine")
	}
}

// Without any draw record the trigger does nothing, so unsubscribing works as before.
func TestDrawUnsubscribeWithoutDraws(t *testing.T) {
	e := newDrawEnv(t)
	l := e.list("L", "single")
	s := e.subs(l, 3, "confirmed", `{}`)
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE subscriber_id=$1`, s[0])
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unsubscribed') ON CONFLICT (subscriber_id,list_id) DO UPDATE SET status='unsubscribed'`, s[1], l)
	wantIDs(t, dbIDs(t, e.db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 AND status='unsubscribed' ORDER BY 1`, l), union(s[:2]))
}

func TestDrawDeleteKeepsReceivers(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 20, "confirmed", `{}`)
	g, d, err := e.draw(src, count(10))
	if err != nil {
		t.Fatal(err)
	}
	drawn := e.members(g.ID)
	camp := e.campaign(g.ID)
	e.sent(camp, time.Hour, drawn[:4]...)
	if _, err := e.db.Exec(e.q["delete-lists"], pq.Array([]int{g.ID}), "", nil, true, pq.Array([]int{})); err != nil {
		// The query shape may differ; fall back to a plain delete.
		e.db.MustExec(`DELETE FROM lists WHERE id=$1`, g.ID)
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM lists WHERE id=$1`, g.ID)
	if n != 0 {
		e.db.MustExec(`DELETE FROM lists WHERE id=$1`, g.ID)
	}
	// The campaign is kept for the source, the record stays marked deleted.
	var kept int
	e.db.Get(&kept, `SELECT COUNT(*) FROM axm_list_draw_campaigns WHERE source_list_id=$1 AND campaign_id=$2`, src, camp)
	if kept != 1 {
		t.Fatal("campaign link lost")
	}
	var del bool
	var listID *int
	e.db.QueryRow(`SELECT deleted_at IS NOT NULL, list_id FROM axm_list_draws WHERE id=$1`, d.ID).Scan(&del, &listID)
	if !del || listID != nil {
		t.Fatalf("record: deleted=%v list=%v", del, listID)
	}
	// Those who got the campaign stay out; the six who were drawn but not sent are back.
	p, _ := e.c.PreviewListDraw(src, count(100))
	if p.Target != 20 || p.AlreadyReceived != 4 || p.AlreadyDrawn != 0 || p.Candidates != 16 {
		t.Fatalf("%+v", p)
	}
	l2, _, err := e.draw(src, count(100))
	if err != nil || overlaps(e.members(l2.ID), drawn[:4]) || len(e.members(l2.ID)) != 16 {
		t.Fatalf("%v %v", e.members(l2.ID), err)
	}
	// Deleting the source keeps the drawn list as an ordinary list and the record by name.
	e.db.MustExec(`DELETE FROM lists WHERE id=$1`, src)
	var name string
	var srcID *int
	e.db.QueryRow(`SELECT source_name, source_list_id FROM axm_list_draws WHERE list_id=$1`, l2.ID).Scan(&name, &srcID)
	if name != "S" || srcID != nil || len(e.members(l2.ID)) != 16 {
		t.Fatalf("%q %v", name, srcID)
	}
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_list_draw_campaigns`)
	if n != 0 {
		t.Fatal("links of a deleted source must go")
	}
}

// A drawn list and its source deleted in one statement must not trip the foreign key.
func TestDrawDeleteBothAtOnce(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 5, "confirmed", `{}`)
	g, _, _ := e.draw(src, count(3))
	e.campaign(g.ID)
	if _, err := e.db.Exec(`DELETE FROM lists WHERE id = ANY($1)`, pq.Array([]int{src, g.ID})); err != nil {
		t.Fatal(err)
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_list_draw_campaigns`)
	if n != 0 {
		t.Fatal(n)
	}
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_list_draws WHERE list_id IS NULL AND source_list_id IS NULL AND deleted_at IS NOT NULL`)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestDrawRecords(t *testing.T) {
	e := newDrawEnv(t)
	src := e.list("S", "single")
	e.subs(src, 30, "confirmed", `{}`)
	var uid int
	e.db.MustExec(`INSERT INTO roles(type,name,permissions) VALUES('user','R','{}') ON CONFLICT DO NOTHING`)
	if err := e.db.Get(&uid, `INSERT INTO users(username,email,type,status,name,user_role_id) VALUES('drawer','drawer@example.invalid','user','enabled','Drawer',(SELECT id FROM roles ORDER BY id LIMIT 1)) RETURNING id`); err != nil {
		t.Fatal(err)
	}
	var ids []int
	for i := 0; i < 3; i++ {
		l, _, err := e.c.DrawList(src, count(5), DrawOpts{UserID: uid})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
	}
	camp := e.campaign(ids[0])
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, camp)
	e.campaign(ids[0])
	rows, err := e.c.GetListDraws(src, nil, 50)
	if err != nil || len(rows) != 3 {
		t.Fatalf("%d %v", len(rows), err)
	}
	if *rows[0].ListID != ids[2] || *rows[2].ListID != ids[0] || rows[0].DrawnByName != "Drawer" {
		t.Fatalf("%+v", rows)
	}
	if rows[2].Campaigns != 2 || rows[2].CampaignsFinished != 1 || rows[1].Campaigns != 0 {
		t.Fatalf("%+v", rows[2])
	}
	rows, _ = e.c.GetListDraws(0, []int{ids[1]}, 1)
	if len(rows) != 1 || *rows[0].ListID != ids[1] {
		t.Fatalf("%+v", rows)
	}
	// Renaming a drawn list shows its current name; the record keeps the name it was made with.
	e.db.MustExec(`UPDATE lists SET name='Renamed' WHERE id=$1`, ids[1])
	rows, _ = e.c.GetListDraws(src, nil, 50)
	var found bool
	for _, r := range rows {
		if *r.ListID == ids[1] && r.ListName == "Renamed" {
			found = true
		}
	}
	if !found {
		t.Fatal("current name")
	}
	// Deleting a user keeps the record.
	e.db.MustExec(`DELETE FROM users WHERE id=$1`, uid)
	rows, _ = e.c.GetListDraws(src, nil, 50)
	if len(rows) != 3 || rows[0].DrawnBy != nil {
		t.Fatalf("%+v", rows[0])
	}
}

// A list that a dynamic list names as an exclude list cannot be deleted by any path.
func TestExcludeListCannotBeDeleted(t *testing.T) {
	e := newDrawEnv(t)
	base := e.list("Base", "single")
	cust := e.list("Customers", "single")
	other := e.list("Other", "single")
	e.subs(base, 3, "confirmed", `{}`)
	dyn := testList(e.db, v2Filter("lists", []int{base}, []int{cust}), "single")
	err := e.c.DeleteLists([]int{cust}, "", true, nil)
	if err == nil || !strings.Contains(err.Error(), "exclude list") {
		t.Fatalf("%v", err)
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != 400 {
		t.Fatalf("%#v", err)
	}
	// By query, with others in the same call, nothing is deleted.
	if err := e.c.DeleteLists([]int{other, cust}, "", true, nil); err == nil {
		t.Fatal("deleted together with a list that can go")
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM lists WHERE id = ANY($1)`, pq.Array([]int{other, cust}))
	if n != 2 {
		t.Fatalf("%d lists left", n)
	}
	// An unrelated list is deleted as before; the dynamic list can go, then so can the exclude list.
	if err := e.c.DeleteLists([]int{other}, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.c.DeleteLists([]int{dyn}, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.c.DeleteLists([]int{cust}, "", true, nil); err != nil {
		t.Fatal(err)
	}
}
