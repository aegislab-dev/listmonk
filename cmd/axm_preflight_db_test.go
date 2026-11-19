package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
)

// LST-48: the pre-send confirmation counts the people a campaign reaches with the same
// rules as the sender. These tests run the sender's own queries (next-campaigns and the
// batch queries, through the real store) and compare what they reach with what the
// confirmation says.

func (e *sendsEnv) pfList(optin, filter string) int {
	e.t.Helper()
	var id int
	if filter == "" {
		e.get(&id, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'L'||gen_random_uuid()::TEXT,'private',$1) RETURNING id`, optin)
	} else {
		e.get(&id, `INSERT INTO lists(uuid,name,type,optin,axm_filter) VALUES(gen_random_uuid(),'D'||gen_random_uuid()::TEXT,'private',$1,$2::jsonb) RETURNING id`, optin, filter)
	}
	return id
}

func (e *sendsEnv) pfSub(status, plan string, memberships map[int]string) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO subscribers(uuid,email,name,status,attribs) VALUES(gen_random_uuid(),gen_random_uuid()::TEXT||'@example.invalid','S',$1::subscriber_status,$2::jsonb) RETURNING id`,
		status, fmt.Sprintf(`{"plan":%q}`, plan))
	for l, st := range memberships {
		e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,$3::subscription_status)`, id, l, st)
	}
	return id
}

func (e *sendsEnv) pfCampaign(typ, status string, lists ...int) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO campaigns(uuid,name,subject,from_email,body,content_type,messenger,type,status)
		VALUES(gen_random_uuid(),'C'||gen_random_uuid()::TEXT,'Subject','Team <team@crevasse.ai>','<p>Hi</p>','html','email',$1::campaign_type,$2::campaign_status) RETURNING id`, typ, status)
	for _, l := range lists {
		e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) SELECT $1,id,name FROM lists WHERE id=$2`, id, l)
	}
	return id
}

func dynFilter(listIDs ...int) string {
	b, _ := json.Marshal(map[string]any{"version": 1, "base": map[string]any{"type": "lists", "lists": listIDs}, "match": "all",
		"conditions": []map[string]any{{"key": "plan", "op": "eq", "value": "pro"}}})
	return string(b)
}

// reach runs the campaign the way the sender does and returns the subscribers it reads.
// after is the checkpoint it resumes from. The campaign's status is left running.
func (e *sendsEnv) reach(id, after int) (ids []int, toSend int) {
	e.t.Helper()
	e.db.MustExec(`UPDATE campaigns SET status='running' WHERE id=$1`, id)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		e.t.Fatal(err)
	}
	e.get(&toSend, `SELECT to_send FROM campaigns WHERE id=$1`, id)
	last := after
	for {
		batch, err := e.store.NextSubscribers(id, last, 7)
		if err != nil {
			e.t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			ids = append(ids, s.ID)
			last = s.ID
		}
	}
	return ids, toSend
}

func TestPreflightAudienceMatchesSend(t *testing.T) {
	e := newSendsEnv(t, 0)
	ctx := context.Background()
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, e.campID)

	type tc struct {
		name  string
		build func() (camp int, lists map[int]int) // the campaign and each list's expected own count
		total int
	}
	cases := []tc{
		{"single list: unconfirmed counts, unsubscribed and blocklisted do not", func() (int, map[int]int) {
			l := e.pfList("single", "")
			for i := 0; i < 3; i++ {
				e.pfSub("enabled", "", map[int]string{l: "unconfirmed"})
			}
			e.pfSub("enabled", "", map[int]string{l: "confirmed"})
			e.pfSub("enabled", "", map[int]string{l: "unsubscribed"})
			e.pfSub("blocklisted", "", map[int]string{l: "confirmed"})
			return e.pfCampaign("regular", "draft", l), map[int]int{l: 4}
		}, 4},
		{"two lists overlap: counted once", func() (int, map[int]int) {
			a, b := e.pfList("single", ""), e.pfList("single", "")
			for i := 0; i < 3; i++ {
				e.pfSub("enabled", "", map[int]string{a: "confirmed", b: "confirmed"})
			}
			e.pfSub("enabled", "", map[int]string{a: "confirmed"})
			e.pfSub("enabled", "", map[int]string{b: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{b: "confirmed"})
			return e.pfCampaign("regular", "draft", a, b), map[int]int{a: 4, b: 5}
		}, 6},
		{"double opt-in list: only confirmed", func() (int, map[int]int) {
			l := e.pfList("double", "")
			e.pfSub("enabled", "", map[int]string{l: "confirmed"})
			e.pfSub("enabled", "", map[int]string{l: "confirmed"})
			e.pfSub("enabled", "", map[int]string{l: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{l: "unsubscribed"})
			return e.pfCampaign("regular", "draft", l), map[int]int{l: 2}
		}, 2},
		{"opt-in campaign: unconfirmed of double lists only", func() (int, map[int]int) {
			d, s := e.pfList("double", ""), e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{d: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{d: "unconfirmed", s: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{d: "confirmed"})
			e.pfSub("enabled", "", map[int]string{s: "unconfirmed"})
			e.pfSub("blocklisted", "", map[int]string{d: "unconfirmed"})
			return e.pfCampaign("optin", "draft", d, s), map[int]int{d: 2, s: 0}
		}, 2},
		{"dynamic list", func() (int, map[int]int) {
			base := e.pfList("single", "")
			for _, p := range []string{"pro", "pro", "free", "pro"} {
				e.pfSub("enabled", p, map[int]string{base: "confirmed"})
			}
			e.pfSub("enabled", "pro", map[int]string{base: "unsubscribed"})
			e.pfSub("blocklisted", "pro", map[int]string{base: "confirmed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("regular", "draft", d), map[int]int{d: 3}
		}, 3},
		{"dynamic and regular lists overlap", func() (int, map[int]int) {
			base, other := e.pfList("single", ""), e.pfList("single", "")
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed", other: "confirmed"})
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed"})
			e.pfSub("enabled", "free", map[int]string{base: "confirmed", other: "confirmed"})
			e.pfSub("enabled", "free", map[int]string{other: "unsubscribed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("regular", "draft", other, d), map[int]int{other: 2, d: 2}
		}, 3},
		{"opt-in campaign never reaches a dynamic list", func() (int, map[int]int) {
			base, dbl := e.pfList("single", ""), e.pfList("double", "")
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed"})
			e.pfSub("enabled", "pro", map[int]string{dbl: "unconfirmed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("optin", "draft", d, dbl), map[int]int{d: 0, dbl: 1}
		}, 1},
		{"nobody", func() (int, map[int]int) {
			l := e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{l: "unsubscribed"})
			return e.pfCampaign("regular", "draft", l), map[int]int{l: 0}
		}, 0},
		{"a list that was deleted keeps its name and counts zero", func() (int, map[int]int) {
			l, gone := e.pfList("single", ""), e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{l: "confirmed", gone: "confirmed"})
			camp := e.pfCampaign("regular", "draft", l, gone)
			e.db.MustExec(`DELETE FROM lists WHERE id=$1`, gone)
			return camp, map[int]int{l: 1}
		}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			camp, wantLists := c.build()
			lists, aud, err := e.core.GetPreflightAudience(ctx, camp, 0)
			if err != nil {
				t.Fatal(err)
			}
			if aud.Total != c.total {
				t.Fatalf("total %d, want %d", aud.Total, c.total)
			}
			for _, l := range lists {
				if l.Deleted {
					if l.Count != 0 || l.Name == "" || l.ID.Valid {
						t.Fatalf("deleted list: %+v", l)
					}
					continue
				}
				if want, ok := wantLists[l.ID.Int]; !ok || l.Count != want {
					t.Fatalf("list %+v, want count %d", l, want)
				}
			}
			// What the sender reaches is what the confirmation said.
			ids, toSend := e.reach(camp, 0)
			if len(ids) != aud.Total || toSend != aud.Total {
				t.Fatalf("the sender reaches %d (to_send %d), the confirmation says %d", len(ids), toSend, aud.Total)
			}
			seen := map[int]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Fatalf("subscriber %d read twice", id)
				}
				seen[id] = true
			}
		})
	}
}

// A paused campaign that is resumed recounts its upper bound, so people who joined while
// it was paused are sent; people after the checkpoint who already have a send record are
// skipped by the sender.
func TestPreflightRemainingMatchesResume(t *testing.T) {
	e := newSendsEnv(t, 0)
	ctx := context.Background()
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, e.campID)
	l := e.pfList("single", "")
	var subs []int
	for i := 0; i < 10; i++ {
		subs = append(subs, e.pfSub("enabled", "", map[int]string{l: "confirmed"}))
	}
	camp := e.pfCampaign("regular", "paused", l)
	// Five were done: the checkpoint is the fifth. Two after it have records anyway (the
	// run was cut off between sending them and saving the checkpoint).
	e.db.MustExec(`UPDATE campaigns SET last_subscriber_id=$2, to_send=10, sent=5 WHERE id=$1`, camp, subs[4])
	for _, id := range append(slices.Clone(subs[:5]), subs[5], subs[6]) {
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, camp, id)
	}
	// Two people joined while it was paused, one of them is blocklisted, one left.
	joined := e.pfSub("enabled", "", map[int]string{l: "confirmed"})
	e.pfSub("blocklisted", "", map[int]string{l: "confirmed"})
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE subscriber_id=$1`, subs[9])

	_, aud, err := e.core.GetPreflightAudience(ctx, camp, subs[4])
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := e.reach(camp, subs[4])
	sent := map[int]bool{}
	for _, id := range e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id=$1`, camp) {
		sent[id] = true
	}
	var fresh []int
	for _, id := range ids {
		if !sent[id] {
			fresh = append(fresh, id)
		}
	}
	want := []int{subs[7], subs[8], joined}
	if !reflect.DeepEqual(fresh, want) {
		t.Fatalf("the sender sends %v, want %v", fresh, want)
	}
	if aud.Remaining != len(fresh) {
		t.Fatalf("remaining %d, the sender sends %d", aud.Remaining, len(fresh))
	}
	if aud.Total != 10 { // all that are eligible now, sent or not: 10 - 1 who left + 1 who joined
		t.Fatalf("total %d", aud.Total)
	}
}

// preflightApp is the app of a handler test: the team users of newTeamsEnv with a real
// manager and the Korean texts.
func newPreflightApp(t *testing.T) *teamsEnv {
	e := newTeamsEnv(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "i18n", "ko.json"))
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.a.i18n = lang
	e.a.log = log.New(io.Discard, "", 0)
	e.a.cfg = &Config{}
	e.a.manager = e.startManager(e.store, &bodyMessenger{}, io.Discard)
	e.db.MustExec(`UPDATE campaigns SET status='draft', content_type='richtext', body='<p>Hi <a href="{{ UnsubscribeURL }}">x</a></p>' WHERE id=$1`, e.campID)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, e.aList)
	return e
}

func TestCampaignPreflightHandler(t *testing.T) {
	e := newPreflightApp(t)
	member, other := e.as(e.ua), e.as(e.ub)
	target := fmt.Sprintf("/api/campaigns/%d/preflight", e.campID)
	id := strconv.Itoa(e.campID)
	for i := 0; i < 3; i++ {
		e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	}

	code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, target, id, "")
	if code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	var out struct {
		models.PreflightAudience
		UpdatedAt  string                  `json:"updated_at"`
		Mode       string                  `json:"mode"`
		FromEmail  string                  `json:"from_email"`
		Recipients int                     `json:"recipients"`
		Lists      []models.PreflightList  `json:"lists"`
		Checks     []models.PreflightCheck `json:"checks"`
		Tracking   *struct{ State string } `json:"tracking"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "start" || out.Recipients != 3 || len(out.Lists) != 1 || out.Lists[0].Count != 3 || out.Tracking == nil {
		t.Fatalf("answer: %s", data)
	}
	level := map[string]string{}
	for _, c := range out.Checks {
		level[c.Key] = c.Level
	}
	// The campaign's from is the fixture's example address; the body has the unsubscribe link.
	if level["unsubscribe.missing"] != "ok" || level["from.placeholder"] != "warn" || level["recipients.zero"] != "" {
		t.Fatalf("checks: %v", level)
	}

	// A user without access to the campaign gets nothing.
	if code, _ := e.call(e.a.GetCampaignPreflight, other, http.MethodGet, target, id, ""); code != http.StatusForbidden {
		t.Fatalf("other team: %d", code)
	}
	// Nothing was changed by reading.
	var running int
	e.get(&running, `SELECT COUNT(*) FROM campaigns WHERE status != 'draft'`)
	if running != 0 {
		t.Fatalf("%d campaigns are not drafts", running)
	}

	// A campaign that cannot be started has no confirmation.
	e.db.MustExec(`UPDATE campaigns SET status='running' WHERE id=$1`, e.campID)
	if code, _ := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, target, id, ""); code != http.StatusBadRequest {
		t.Fatalf("running: %d", code)
	}
	e.db.MustExec(`UPDATE campaigns SET status='draft' WHERE id=$1`, e.campID)

	// With a send time the status change schedules it, so the confirmation says schedule.
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() - INTERVAL '1 hour' WHERE id=$1`, e.campID)
	_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, target, id, "")
	json.Unmarshal(data, &out)
	level = map[string]string{}
	for _, c := range out.Checks {
		level[c.Key] = c.Level
	}
	if out.Mode != "schedule" || level["schedule.past"] != "warn" {
		t.Fatalf("scheduled in the past: %s", data)
	}

	// A saved recent-send rule is only recorded: the line says so, with who it would exclude.
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL WHERE id=$1`, e.campID)
	e.db.MustExec(`INSERT INTO axm_campaign_recent_rules(campaign_id,days,scope) VALUES($1,14,'all')`, e.campID)
	_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, target, id, "")
	json.Unmarshal(data, &out)
	var recent *models.PreflightCheck
	for i, c := range out.Checks {
		if c.Key == "recent.notApplied" {
			recent = &out.Checks[i]
		}
	}
	if recent == nil || recent.Level != "warn" || recent.Params["days"] != float64(14) {
		t.Fatalf("recent rule: %s", data)
	}
	if _, ok := recent.Params["excluded"]; !ok {
		t.Fatalf("the number it would exclude is missing: %s", data)
	}
}

// The status change refuses a campaign that was edited after the confirmation read it,
// and the updated_at the confirmation returns can be sent back as it is.
func TestCampaignStatusSeenUpdatedAt(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	read := func() string {
		t.Helper()
		_, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", strconv.Itoa(e.campID), "")
		var out struct {
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal(data, &out); err != nil || out.UpdatedAt == "" {
			t.Fatalf("%s %v", data, err)
		}
		return out.UpdatedAt
	}
	status := func(body string) (int, string) {
		t.Helper()
		code, _ := e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, "/", strconv.Itoa(e.campID), body)
		var s string
		e.get(&s, `SELECT status FROM campaigns WHERE id=$1`, e.campID)
		return code, s
	}
	// Give the timestamp microseconds: a value cut to milliseconds would never match.
	e.db.MustExec(`UPDATE campaigns SET updated_at = DATE_TRUNC('second', NOW()) + INTERVAL '123456 microseconds' WHERE id=$1`, e.campID)
	seen := read()

	// Edited after the read: refused, and nothing changed.
	e.db.MustExec(`UPDATE campaigns SET updated_at = updated_at + INTERVAL '1 microsecond', name='edited' WHERE id=$1`, e.campID)
	if code, s := status(fmt.Sprintf(`{"status":"running","updated_at":%q}`, seen)); code != http.StatusConflict || s != "draft" {
		t.Fatalf("stale: %d %s", code, s)
	}
	// The value read again goes through.
	seen = read()
	if code, s := status(fmt.Sprintf(`{"status":"running","updated_at":%q}`, seen)); code != http.StatusOK || s != "running" {
		t.Fatalf("fresh: %d %s", code, s)
	}

	// Without the value it behaves as before.
	e.db.MustExec(`UPDATE campaigns SET status='draft', updated_at=updated_at + INTERVAL '5 microseconds' WHERE id=$1`, e.campID)
	if code, s := status(`{"status":"running"}`); code != http.StatusOK || s != "running" {
		t.Fatalf("no value: %d %s", code, s)
	}

	// An unreadable value is a bad request, not a server error.
	e.db.MustExec(`UPDATE campaigns SET status='draft' WHERE id=$1`, e.campID)
	if code, s := status(`{"status":"running","updated_at":"not-a-time"}`); code != http.StatusBadRequest || s != "draft" {
		t.Fatalf("garbage: %d %s", code, s)
	}
}

// TestPreflightSpeed measures the confirmation's counts on a big list. It only runs with
// AXM_PREFLIGHT_BENCH=<subscribers>, e.g. 400000, and logs the times (FORK.md quotes them).
func TestPreflightSpeed(t *testing.T) {
	raw := os.Getenv("AXM_PREFLIGHT_BENCH")
	n, _ := strconv.Atoi(raw)
	if n <= 0 {
		t.Skip("AXM_PREFLIGHT_BENCH not set")
	}
	e := newSendsEnv(t, 0)
	ctx := context.Background()
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, e.campID)
	a, b := e.pfList("single", ""), e.pfList("single", "")
	e.db.MustExec(`INSERT INTO subscribers(uuid,email,name,status,attribs)
		SELECT gen_random_uuid(), 'u'||g||'@example.invalid', 'S', 'enabled', jsonb_build_object('plan', CASE WHEN g % 3 = 0 THEN 'pro' ELSE 'free' END)
		FROM generate_series(1, $1) g`, n)
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) SELECT id, $1, 'confirmed' FROM subscribers`, a)
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) SELECT id, $1, 'confirmed' FROM subscribers WHERE id % 8 < 3`, b)
	dyn := e.pfList("single", dynFilter(a))
	e.db.MustExec(`ANALYZE`)

	one := e.pfCampaign("regular", "draft", a)
	two := e.pfCampaign("regular", "draft", a, b)
	mixed := e.pfCampaign("regular", "draft", b, dyn)
	resume := e.pfCampaign("regular", "paused", a, b)
	e.db.MustExec(`UPDATE campaigns SET last_subscriber_id=$2 WHERE id=$1`, resume, n/2)
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) SELECT $1, id FROM subscribers WHERE id <= $2`, resume, n/2+n/10)

	for _, c := range []struct {
		name  string
		id    int
		after int
	}{{"one list", one, 0}, {"two lists, 37% on both", two, 0}, {"regular + dynamic list", mixed, 0}, {"resume (half done)", resume, n / 2}} {
		for i := 0; i < 2; i++ { // the second run is the warm one
			start := time.Now()
			_, aud, err := e.core.GetPreflightAudience(ctx, c.id, c.after)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%-26s run %d: %6.2fs  total %d remaining %d", c.name, i+1, time.Since(start).Seconds(), aud.Total, aud.Remaining)
		}
	}
}
