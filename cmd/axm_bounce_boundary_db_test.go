package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/core"
	"github.com/lib/pq"
)

// LST-73: a user who may not manage all lists deletes or blocks only the bounces of people
// who are on no list outside their own. Reading stays company-wide.

type bounceEnv struct {
	*boundaryEnv
	none int // a subscriber with no subscription at all
}

// newBounceEnv adds two bounces to every subscriber of the boundary environment: one from the
// API and one from SES. Locked for the team 가 member (ua): s2, s3, s4.
func newBounceEnv(t *testing.T) *bounceEnv {
	e := &bounceEnv{boundaryEnv: newBoundaryEnv(t)}
	e.none = e.person("none")
	e.db.MustExec(`DELETE FROM bounces`)
	e.seed()
	return e
}

func (e *bounceEnv) seed() {
	e.db.MustExec(`DELETE FROM bounces`)
	for _, id := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6, e.none} {
		e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source) VALUES ($1, 'hard', 'api'), ($1, 'soft', 'ses')`, id)
	}
}

func (e *bounceEnv) left(id int) int {
	var n int
	e.get(&n, `SELECT COUNT(*) FROM bounces WHERE subscriber_id = $1`, id)
	return n
}

// bounceIDs are the IDs of the bounces of the subscribers.
func (e *bounceEnv) bounceIDs(subs ...int) []int {
	out := []int{}
	for _, s := range subs {
		var ids []int
		if err := e.db.Select(&ids, `SELECT id FROM bounces WHERE subscriber_id = $1 ORDER BY id`, s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, ids...)
	}
	return out
}

func idQuery(prefix string, ids []int) string {
	q := prefix
	for _, id := range ids {
		q += "&id=" + strconv.Itoa(id)
	}
	return q
}

type bounceResp struct{ Done, Skipped int }

func parseResp(t *testing.T, data json.RawMessage) (bounceResp, bool) {
	var r bounceResp
	if err := json.Unmarshal(data, &r); err != nil {
		return r, false
	}
	return r, true
}

func TestBounceDeleteStaysInsideTheTeam(t *testing.T) {
	e := newBounceEnv(t)
	ua := e.as(e.ua)
	locked := map[int]bool{e.s2: true, e.s3: true, e.s4: true}

	// One bounce at a time: a locked person's bounce is refused, the others are deleted.
	for _, id := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6, e.none} {
		b := e.bounceIDs(id)[0]
		want := 200
		if locked[id] {
			want = http.StatusForbidden
		}
		if code, _ := e.call(e.a.DeleteBounce, ua, "DELETE", "/x", itoa(b), ""); code != want {
			t.Fatalf("delete one bounce of %d: %d, want %d", id, code, want)
		}
		wantLeft := 1
		if locked[id] {
			wantLeft = 2
		}
		if got := e.left(id); got != wantLeft {
			t.Fatalf("bounces of %d left: %d, want %d", id, got, wantLeft)
		}
	}

	// By ID, all or nothing: one locked person refuses the whole request.
	e.seed()
	mixed := e.bounceIDs(e.s1, e.s2, e.s5)
	if code, _ := e.call(e.a.DeleteBounces, ua, "DELETE", idQuery("/x?", mixed), "", ""); code != http.StatusForbidden {
		t.Fatalf("mixed ids: %d, want 403", code)
	}
	for _, id := range []int{e.s1, e.s2, e.s5} {
		if e.left(id) != 2 {
			t.Fatalf("a refused request deleted bounces of %d", id)
		}
	}
	// Only own people: all deleted, and the answer says so.
	own := e.bounceIDs(e.s1, e.s5, e.s6, e.none)
	code, data := e.call(e.a.DeleteBounces, ua, "DELETE", idQuery("/x?", own), "", "")
	if r, ok := parseResp(t, data); code != 200 || !ok || r.Done != len(own) || r.Skipped != 0 {
		t.Fatalf("own ids: %d %s", code, data)
	}
	for _, id := range []int{e.s1, e.s5, e.s6, e.none} {
		if e.left(id) != 0 {
			t.Fatalf("bounces of %d left: %d", id, e.left(id))
		}
	}
	// A bounce that does not exist is nobody's: nothing happens, nothing fails.
	if code, _ := e.call(e.a.DeleteBounces, ua, "DELETE", "/x?id=999999", "", ""); code != 200 {
		t.Fatalf("unknown id: %d", code)
	}

	// "Select all": the bounces of locked people are left out and counted.
	e.seed()
	code, data = e.call(e.a.DeleteBounces, ua, "DELETE", "/x?all=true", "", "")
	r, ok := parseResp(t, data)
	if code != 200 || !ok || r.Done != 8 || r.Skipped != 6 {
		t.Fatalf("all: %d %s, want done 8 skipped 6", code, data)
	}
	for _, id := range []int{e.s1, e.s5, e.s6, e.none} {
		if e.left(id) != 0 {
			t.Fatalf("all: bounces of %d left: %d", id, e.left(id))
		}
	}
	for id := range locked {
		if e.left(id) != 2 {
			t.Fatalf("all: bounces of locked %d: %d, want 2", id, e.left(id))
		}
	}

	// The filter of the screen still narrows it (LST-70): only the SES bounces of own people.
	e.seed()
	code, data = e.call(e.a.DeleteBounces, ua, "DELETE", "/x?all=true&source=ses", "", "")
	r, ok = parseResp(t, data)
	if code != 200 || !ok || r.Done != 4 || r.Skipped != 3 {
		t.Fatalf("all, source: %d %s, want done 4 skipped 3", code, data)
	}
	if e.left(e.s1) != 1 || e.left(e.s2) != 2 {
		t.Fatalf("all, source: left %d / %d, want 1 / 2", e.left(e.s1), e.left(e.s2))
	}

	// Somebody who only reads every list (lists:get_all) is not an administrator either.
	// A person with a subscription on a list nobody on the user's team manages is locked.
	e.seed()
	other := e.person("other-team-only", e.bList, "confirmed")
	e.db.MustExec(`INSERT INTO bounces (subscriber_id, type, source) VALUES ($1, 'hard', 'api')`, other)
	if code, _ := e.call(e.a.DeleteBounce, ua, "DELETE", "/x", itoa(e.bounceIDs(other)[0]), ""); code != http.StatusForbidden {
		t.Fatalf("a bounce of the other team's person: %d, want 403", code)
	}
}

func TestBounceDeleteByAdministrator(t *testing.T) {
	e := newBounceEnv(t)
	for name, user := range map[string]int{"administrator": e.admin, "super admin": e.superAdmin} {
		e.seed()
		admin := e.as(user)
		code, data := e.call(e.a.DeleteBounces, admin, "DELETE", idQuery("/x?", e.bounceIDs(e.s2, e.s3, e.s4)), "", "")
		if code != 200 || string(data) != "true" {
			t.Fatalf("%s by id: %d %s, want 200 true", name, code, data)
		}
		if e.left(e.s2)+e.left(e.s3)+e.left(e.s4) != 0 {
			t.Fatalf("%s: bounces left after delete by id", name)
		}
		if code, data = e.call(e.a.DeleteBounce, admin, "DELETE", "/x", itoa(e.bounceIDs(e.s5)[0]), ""); code != 200 || string(data) != "true" {
			t.Fatalf("%s one: %d %s", name, code, data)
		}
		if code, data = e.call(e.a.DeleteBounces, admin, "DELETE", "/x?all=true", "", ""); code != 200 || string(data) != "true" {
			t.Fatalf("%s all: %d %s", name, code, data)
		}
		var n int
		e.get(&n, `SELECT COUNT(*) FROM bounces`)
		if n != 0 {
			t.Fatalf("%s: %d bounces left, want 0", name, n)
		}
	}
}

func TestBounceBlocklistAllStaysInsideTheTeam(t *testing.T) {
	e := newBounceEnv(t)
	ua := e.as(e.ua)
	status := func(id int) string { return e.row(id).Status }

	code, data := e.call(e.a.BlocklistBouncedSubscribers, ua, "PUT", "/x", "", "")
	r, ok := parseResp(t, data)
	if code != 200 || !ok || r.Done != 4 || r.Skipped != 3 {
		t.Fatalf("block all: %d %s, want done 4 skipped 3", code, data)
	}
	for _, id := range []int{e.s1, e.s5, e.s6, e.none} {
		if status(id) != "blocklisted" {
			t.Fatalf("subscriber %d: %s, want blocklisted", id, status(id))
		}
	}
	for _, id := range []int{e.s2, e.s3, e.s4} {
		if status(id) != "enabled" {
			t.Fatalf("locked subscriber %d: %s, want enabled", id, status(id))
		}
	}
	// The blocked people's subscriptions are unsubscribed, the locked person's are untouched.
	e.wantLists(e.s1, "blocked person", "A1:unsubscribed")
	e.wantLists(e.s2, "locked person", "A1:confirmed", "B1:confirmed")
	// The audit row says how many and how many were left out.
	rows := e.audits("action = 'subscriber.blocklist'")
	if len(rows) != 1 || rows[0].Count == nil || *rows[0].Count != 4 || string(rows[0].Detail) == "" {
		t.Fatalf("audit: %+v", rows)
	}
	var detail map[string]any
	json.Unmarshal(rows[0].Detail, &detail)
	if detail["skipped_shared"] != float64(3) || detail["via"] != "bounces" {
		t.Fatalf("audit detail: %v", detail)
	}

	// An administrator blocks everybody who bounced, as before.
	e.db.MustExec(`UPDATE subscribers SET status = 'enabled'`)
	code, data = e.call(e.a.BlocklistBouncedSubscribers, e.as(e.admin), "PUT", "/x", "", "")
	if code != 200 || string(data) != "true" {
		t.Fatalf("administrator: %d %s", code, data)
	}
	for _, id := range []int{e.s1, e.s2, e.s3, e.s4, e.s5, e.s6, e.none} {
		if status(id) != "blocklisted" {
			t.Fatalf("administrator: subscriber %d: %s", id, status(id))
		}
	}
}

// The bounce list is still read by everybody who may read bounces.
func TestBounceReadStaysCompanyWide(t *testing.T) {
	e := newBounceEnv(t)
	code, data := e.call(e.a.GetBounces, e.as(e.ua), "GET", "/x?per_page=all", "", "")
	var page struct {
		Total int `json:"total"`
	}
	json.Unmarshal(data, &page)
	if code != 200 || page.Total != 14 {
		t.Fatalf("read: %d total %d, want 14", code, page.Total)
	}
}

// "Block everyone who bounced" reads who has bounces before it takes the locks. Somebody whose
// last bounce is cleared in that gap (and who was set right again) is not blocked.
func TestBounceBlocklistAllLooksAgainUnderTheLocks(t *testing.T) {
	e := newBounceEnv(t)
	ua := e.as(e.ua)
	core.AfterBouncedRead = func() {
		// s1 (own) and s2 (locked) lose their last bounce after the candidates were read.
		e.db.MustExec(`DELETE FROM bounces WHERE subscriber_id = ANY($1)`, pq.Array([]int{e.s1, e.s2}))
	}
	defer func() { core.AfterBouncedRead = nil }()

	code, data := e.call(e.a.BlocklistBouncedSubscribers, ua, "PUT", "/x", "", "")
	r, ok := parseResp(t, data)
	// Own and still bounced: s5, s6, none. Locked and still bounced: s3, s4. s1 and s2 are gone.
	if code != 200 || !ok || r.Done != 3 || r.Skipped != 2 {
		t.Fatalf("block all: %d %s, want done 3 skipped 2", code, data)
	}
	if got := e.row(e.s1).Status; got != "enabled" {
		t.Fatalf("s1 had no bounce left and is %s, want enabled", got)
	}
	for _, id := range []int{e.s5, e.s6, e.none} {
		if e.row(id).Status != "blocklisted" {
			t.Fatalf("subscriber %d: %s, want blocklisted", id, e.row(id).Status)
		}
	}
}

// Clearing a bounce does not lock the subscriber, so the re-check also locks the bounce rows:
// an administrator's delete in the gap before the blocklist waits until it is done, instead of
// leaving a person with no bounce blocked.
func TestBounceBlocklistAllHoldsTheBouncesUntilItIsDone(t *testing.T) {
	e := newBounceEnv(t)
	ua := e.as(e.ua)
	done := make(chan struct{})
	core.AfterBouncedRecheck = func() {
		go func() {
			e.db.MustExec(`DELETE FROM bounces WHERE subscriber_id = $1`, e.s1)
			close(done)
		}()
		select {
		case <-done:
			t.Error("a bounce was deleted while the blocklist held its rows")
		case <-time.After(700 * time.Millisecond):
		}
	}
	defer func() { core.AfterBouncedRecheck = nil }()

	if code, _ := e.call(e.a.BlocklistBouncedSubscribers, ua, "PUT", "/x", "", ""); code != 200 {
		t.Fatalf("block all: %d", code)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the delete never went through after the blocklist finished")
	}
	// The delete came after the blocklist: s1 was blocked while it still had its bounces.
	if got := e.row(e.s1).Status; got != "blocklisted" {
		t.Fatalf("s1: %s, want blocklisted (it had bounces when it was blocked)", got)
	}
	if e.left(e.s1) != 0 {
		t.Fatalf("the delete did not run: %d bounces left", e.left(e.s1))
	}
}
