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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"gopkg.in/volatiletech/null.v6"
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
		// multi: people on more than one list; excluded: people on the lists who are left out.
		multi, excluded int
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
		}, 4, 0, 2},
		{"two lists overlap: counted once", func() (int, map[int]int) {
			a, b := e.pfList("single", ""), e.pfList("single", "")
			for i := 0; i < 3; i++ {
				e.pfSub("enabled", "", map[int]string{a: "confirmed", b: "confirmed"})
			}
			e.pfSub("enabled", "", map[int]string{a: "confirmed"})
			e.pfSub("enabled", "", map[int]string{b: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{b: "confirmed"})
			return e.pfCampaign("regular", "draft", a, b), map[int]int{a: 4, b: 5}
		}, 6, 3, 0},
		{"double opt-in list: only confirmed", func() (int, map[int]int) {
			l := e.pfList("double", "")
			e.pfSub("enabled", "", map[int]string{l: "confirmed"})
			e.pfSub("enabled", "", map[int]string{l: "confirmed"})
			e.pfSub("enabled", "", map[int]string{l: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{l: "unsubscribed"})
			return e.pfCampaign("regular", "draft", l), map[int]int{l: 2}
		}, 2, 0, 2},
		{"opt-in campaign: unconfirmed of double lists only", func() (int, map[int]int) {
			d, s := e.pfList("double", ""), e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{d: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{d: "unconfirmed", s: "unconfirmed"})
			e.pfSub("enabled", "", map[int]string{d: "confirmed"})
			e.pfSub("enabled", "", map[int]string{s: "unconfirmed"})
			e.pfSub("blocklisted", "", map[int]string{d: "unconfirmed"})
			return e.pfCampaign("optin", "draft", d, s), map[int]int{d: 2, s: 0}
		}, 2, 0, 0},
		{"dynamic list", func() (int, map[int]int) {
			base := e.pfList("single", "")
			for _, p := range []string{"pro", "pro", "free", "pro"} {
				e.pfSub("enabled", p, map[int]string{base: "confirmed"})
			}
			e.pfSub("enabled", "pro", map[int]string{base: "unsubscribed"})
			e.pfSub("blocklisted", "pro", map[int]string{base: "confirmed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("regular", "draft", d), map[int]int{d: 3}
		}, 3, 0, 0},
		{"dynamic and regular lists overlap", func() (int, map[int]int) {
			base, other := e.pfList("single", ""), e.pfList("single", "")
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed", other: "confirmed"})
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed"})
			e.pfSub("enabled", "free", map[int]string{base: "confirmed", other: "confirmed"})
			e.pfSub("enabled", "free", map[int]string{other: "unsubscribed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("regular", "draft", other, d), map[int]int{other: 2, d: 2}
		}, 3, 1, 1},
		{"opt-in campaign never reaches a dynamic list", func() (int, map[int]int) {
			base, dbl := e.pfList("single", ""), e.pfList("double", "")
			e.pfSub("enabled", "pro", map[int]string{base: "confirmed"})
			e.pfSub("enabled", "pro", map[int]string{dbl: "unconfirmed"})
			d := e.pfList("single", dynFilter(base))
			return e.pfCampaign("optin", "draft", d, dbl), map[int]int{d: 0, dbl: 1}
		}, 1, 0, 0},
		{"one person on three lists is one overlapping person, a blocklisted one is excluded", func() (int, map[int]int) {
			a, b, c := e.pfList("single", ""), e.pfList("single", ""), e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{a: "confirmed", b: "confirmed", c: "confirmed"})
			e.pfSub("blocklisted", "", map[int]string{a: "confirmed", b: "confirmed", c: "confirmed"})
			return e.pfCampaign("regular", "draft", a, b, c), map[int]int{a: 1, b: 1, c: 1}
		}, 1, 1, 1},
		{"nobody", func() (int, map[int]int) {
			l := e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{l: "unsubscribed"})
			return e.pfCampaign("regular", "draft", l), map[int]int{l: 0}
		}, 0, 0, 1},
		{"a list that was deleted keeps its name and counts zero", func() (int, map[int]int) {
			l, gone := e.pfList("single", ""), e.pfList("single", "")
			e.pfSub("enabled", "", map[int]string{l: "confirmed", gone: "confirmed"})
			camp := e.pfCampaign("regular", "draft", l, gone)
			e.db.MustExec(`DELETE FROM lists WHERE id=$1`, gone)
			return camp, map[int]int{l: 1}
		}, 1, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			camp, wantLists := c.build()
			lists, aud, err := e.core.GetPreflightAudience(ctx, camp, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if aud.Total != c.total || aud.Multi != c.multi || aud.Excluded != c.excluded {
				t.Fatalf("total %d multi %d excluded %d, want %d %d %d", aud.Total, aud.Multi, aud.Excluded, c.total, c.multi, c.excluded)
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

	_, aud, err := e.core.GetPreflightAudience(ctx, camp, subs[4], true)
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

// With the checkpoint at 0 a person who already has a send record is still skipped when the
// campaign is resumed (the sender reads the records above the checkpoint, and 0 is below all).
func TestPreflightRemainingWithCheckpointZero(t *testing.T) {
	e := newSendsEnv(t, 0)
	ctx := context.Background()
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, e.campID)
	l := e.pfList("single", "")
	a := e.pfSub("enabled", "", map[int]string{l: "confirmed"})
	e.pfSub("enabled", "", map[int]string{l: "confirmed"})
	camp := e.pfCampaign("regular", "paused", l)
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, camp, a)

	_, aud, err := e.core.GetPreflightAudience(ctx, camp, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := e.reach(camp, 0)
	fresh := 0
	for _, id := range ids {
		if id != a {
			fresh++
		}
	}
	if aud.Remaining != fresh || fresh != 1 || aud.Total != 2 {
		t.Fatalf("remaining %d total %d, the sender sends %d", aud.Remaining, aud.Total, fresh)
	}
	// A draft does not look at the records.
	if _, aud, _ = e.core.GetPreflightAudience(ctx, camp, 0, false); aud.Remaining != 2 {
		t.Fatalf("not a resume: %d", aud.Remaining)
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
	// A real From: the status change refuses a placeholder (LST-45).
	e.db.MustExec(`UPDATE campaigns SET status='draft', from_email='Team <team@crevasse.ai>', content_type='richtext', body='<p>Hi <a href="{{ UnsubscribeURL }}">x</a></p>' WHERE id=$1`, e.campID)
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
	// A real From; the body has the unsubscribe link.
	if level["unsubscribe.missing"] != "ok" || level["from.placeholder"] != "ok" || level["recipients.zero"] != "" || level["from.refused"] != "" {
		t.Fatalf("checks: %v", level)
	}
	// An example address: the status change refuses the draft (LST-45), so the window blocks.
	e.db.MustExec(`UPDATE campaigns SET from_email='sender@example.invalid' WHERE id=$1`, e.campID)
	_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, target, id, "")
	out.Checks = nil
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	level = map[string]string{}
	for _, c := range out.Checks {
		level[c.Key] = c.Level
	}
	if level["from.placeholder"] != "warn" || level["from.refused"] != "block" {
		t.Fatalf("placeholder from: %v", level)
	}
	e.db.MustExec(`UPDATE campaigns SET from_email='Team <team@crevasse.ai>' WHERE id=$1`, e.campID)

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

// What the confirmation says the recipients get is what the mail goes out with: the custom
// headers replace the subject, the sender and the reply address, and the link to leave is
// looked for in the rendered mail, not in the source.
func TestCampaignPreflightWhatIsSent(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	id := strconv.Itoa(e.campID)
	read := func() (out struct {
		Subject   string                  `json:"subject"`
		FromEmail string                  `json:"from_email"`
		ReplyTo   string                  `json:"reply_to"`
		Checks    []models.PreflightCheck `json:"checks"`
	}) {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if code != http.StatusOK || json.Unmarshal(data, &out) != nil {
			t.Fatalf("%d %s", code, data)
		}
		return out
	}
	level := func(checks []models.PreflightCheck, key string) string {
		for _, c := range checks {
			if c.Key == key {
				return c.Level
			}
		}
		return ""
	}
	e.db.MustExec(`UPDATE campaigns SET subject='Stored subject', from_email='News <news@crevasse.ai>' WHERE id=$1`, e.campID)
	if o := read(); o.Subject != "Stored subject" || o.FromEmail != "News <news@crevasse.ai>" || o.ReplyTo != "" {
		t.Fatalf("stored: %+v", o)
	}

	// Headers of the campaign replace the three fields of the mail.
	e.db.MustExec(`UPDATE campaigns SET headers='[{"From":"Editorial <editor@crevasse.ai>","Subject":"Header subject","Reply-To":"reply@crevasse.ai"}]'::jsonb WHERE id=$1`, e.campID)
	if o := read(); o.Subject != "Header subject" || o.FromEmail != "Editorial <editor@crevasse.ai>" || o.ReplyTo != "reply@crevasse.ai" {
		t.Fatalf("headers: %+v", o)
	}
	e.db.MustExec(`UPDATE campaigns SET headers='[]'::jsonb WHERE id=$1`, e.campID)

	// A subject with a template expression is shown rendered, as it goes out (LST-48 review).
	e.db.MustExec(`UPDATE campaigns SET subject='Check {{ .Campaign.Name }}' WHERE id=$1`, e.campID)
	{
		var name string
		e.get(&name, `SELECT name FROM campaigns WHERE id=$1`, e.campID)
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Subject  string `json:"subject"`
			Rendered bool   `json:"subject_rendered"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil || o.Subject != "Check "+name || !o.Rendered {
			t.Fatalf("subject template: %d %s", code, data)
		}
		// The campaign's own UUID, as the mail has it (LST-48 review: a placeholder was shown).
		var uuid string
		e.get(&uuid, `SELECT uuid::TEXT FROM campaigns WHERE id=$1`, e.campID)
		e.db.MustExec(`UPDATE campaigns SET subject='News {{ .Campaign.UUID }}' WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o) != nil || o.Subject != "News "+uuid || !o.Rendered {
			t.Fatalf("subject with the UUID: %s", data)
		}
		// A Subject header with a template expression goes out rendered too.
		e.db.MustExec(`UPDATE campaigns SET headers='[{"Subject":"Header {{ .Campaign.Name }}"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o) != nil || o.Subject != "Header "+name || !o.Rendered {
			t.Fatalf("templated Subject header: %s", data)
		}
		// A body that the example subscriber cannot fill does not hide a subject that does not
		// depend on the subscriber (LST-48 review).
		var body string
		e.get(&body, `SELECT body FROM campaigns WHERE id=$1`, e.campID)
		e.db.MustExec(`UPDATE campaigns SET headers='[]'::jsonb, subject='News {{ .Campaign.Name }}', body='<p>{{ index .Subscriber.Attribs.offers 0 }}</p>' WHERE id=$1`, e.campID)
		var o2 struct {
			Subject string `json:"subject"`
			Note    string `json:"subject_note"`
		}
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o2) != nil || o2.Subject != "News "+name || o2.Note != "rendered" {
			t.Fatalf("subject with a body the example cannot fill: %s", data)
		}
		e.db.MustExec(`UPDATE campaigns SET body=$2 WHERE id=$1`, e.campID, body)
		// A subject with the campaign's change time: the status change sets it, so the window
		// shows today's, and says that it can vary between the mails (LST-48 review).
		e.db.MustExec(`UPDATE campaigns SET subject='Brief {{ .Campaign.UpdatedAt.Time.Format "2006-01-02" }}', updated_at=NOW() - INTERVAL '1 day' WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o2) != nil || o2.Subject != "Brief "+time.Now().Format("2006-01-02") || o2.Note != "varies" {
			t.Fatalf("subject with the change time: %s", data)
		}
		// A subject the example subscriber cannot fill is shown as written, and said so.
		e.db.MustExec(`UPDATE campaigns SET subject='Hi {{ index .Subscriber.Attribs.offers 0 }}' WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o2) != nil || o2.Note != "unrendered" {
			t.Fatalf("subject the example cannot fill: %s", data)
		}
		// A clock layout that a shift of whole days and 12 hours would not show (03:04): still
		// said to vary (LST-48 review). So is a template that calls Date.
		for _, subj := range []string{`At {{ .Campaign.UpdatedAt.Time.Format "03:04" }}`, `On {{ Date "2006-01-02" }}`} {
			e.db.MustExec(`UPDATE campaigns SET subject=$2 WHERE id=$1`, e.campID, subj)
			_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
			if json.Unmarshal(data, &o2) != nil || o2.Note != "varies" {
				t.Fatalf("%s: %s", subj, data)
			}
		}
		// Only the parts that vary are said to (LST-48 review: a fixed Bcc beside a subject with
		// the change time was left out of the comparison and a changed Bcc went out unseen).
		e.db.MustExec(`UPDATE campaigns SET subject='At {{ .Campaign.UpdatedAt }}', headers='[{"Bcc":"archive@crevasse.ai"}]'::jsonb WHERE id=$1`, e.campID)
		{
			_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
			var ov struct {
				Varies []string `json:"varies"`
			}
			if json.Unmarshal(data, &ov) != nil || len(ov.Varies) != 1 || ov.Varies[0] != "subject" {
				t.Fatalf("varies: %s", data)
			}
		}
		// A Cc that varies beside a fixed Bcc: only "cc" varies. A Date in the subject field under
		// a fixed Subject header varies nothing (LST-48 review).
		e.db.MustExec(`UPDATE campaigns SET subject='On {{ Date "2006-01-02" }}', headers='[{"Cc":"desk-{{ .Campaign.UpdatedAt.Time.Format \"0405\" }}@crevasse.ai","Bcc":"archive@crevasse.ai","Subject":"Delivery - Standard"}]'::jsonb WHERE id=$1`, e.campID)
		{
			_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
			var ov struct {
				Varies []string `json:"varies"`
			}
			if json.Unmarshal(data, &ov) != nil || len(ov.Varies) != 1 || ov.Varies[0] != "cc" {
				t.Fatalf("varies cc only: %s", data)
			}
		}
		e.db.MustExec(`UPDATE campaigns SET headers='[]'::jsonb WHERE id=$1`, e.campID)

		// A Subject header that reads the rendered subject field ({{ .Subject }}).
		e.db.MustExec(`UPDATE campaigns SET subject='News {{ .Campaign.Name }}', headers='[{"Subject":"[Note] {{ .Subject }}"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o2) != nil || o2.Subject != "[Note] News "+name {
			t.Fatalf("subject header of the rendered subject: %s", data)
		}
		e.db.MustExec(`UPDATE campaigns SET headers='[]'::jsonb WHERE id=$1`, e.campID)

		// A Cc header: every mail also goes there, and the window counts those mails.
		e.db.MustExec(`UPDATE campaigns SET subject='Stored subject', headers='[{"Cc":"Desk <desk@crevasse.ai>"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o3 struct {
			Copies []struct {
				Kind, Address string
			} `json:"copies"`
			Checks []models.PreflightCheck `json:"checks"`
		}
		if json.Unmarshal(data, &o3) != nil || len(o3.Copies) != 1 || o3.Copies[0].Address != "desk@crevasse.ai" || level(o3.Checks, "mail.copies") != "warn" {
			t.Fatalf("cc header: %s", data)
		}
		// A Bcc taken from each subscriber: not listed, said to go to one more address per mail.
		e.db.MustExec(`UPDATE campaigns SET headers='[{"Bcc":"{{ with .Subscriber.Attribs.owner }}{{ . }}{{ end }}"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		o3.Copies, o3.Checks = nil, nil
		if json.Unmarshal(data, &o3) != nil || len(o3.Copies) != 0 || level(o3.Checks, "mail.copiesPerSubscriber") != "warn" {
			t.Fatalf("bcc per subscriber: %s", data)
		}
		// A fixed Cc beside a Bcc taken from each subscriber: the Cc is still listed and counted,
		// the Bcc is said to come from each subscriber (LST-48 review).
		e.db.MustExec(`UPDATE campaigns SET headers='[{"Cc":"archive@crevasse.ai","Bcc":"{{ with .Subscriber.Attribs.owner }}{{ . }}{{ end }}"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		o3.Copies, o3.Checks = nil, nil
		if json.Unmarshal(data, &o3) != nil || len(o3.Copies) != 1 || o3.Copies[0].Address != "archive@crevasse.ai" ||
			level(o3.Checks, "mail.copies") != "warn" || level(o3.Checks, "mail.copiesPerSubscriber") != "warn" {
			t.Fatalf("fixed cc and bcc per subscriber: %s", data)
		}
		// From and Reply-To taken from each subscriber: said to be the example's.
		var o4 struct {
			PerSubscriber bool `json:"sender_per_subscriber"`
		}
		e.db.MustExec(`UPDATE campaigns SET headers='[{"From":"{{ with .Subscriber.Attribs.owner }}{{ . }}{{ else }}news@crevasse.ai{{ end }}","Reply-To":"desk@crevasse.ai"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o4) != nil || !o4.PerSubscriber {
			t.Fatalf("from per subscriber: %s", data)
		}
		e.db.MustExec(`UPDATE campaigns SET headers='[{"Reply-To":"desk@crevasse.ai"}]'::jsonb WHERE id=$1`, e.campID)
		o4.PerSubscriber = false
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o4) != nil || o4.PerSubscriber {
			t.Fatalf("fixed reply-to: %s", data)
		}
		e.db.MustExec(`UPDATE campaigns SET subject='Check {{ .Campaign.Name }}', headers='[]'::jsonb WHERE id=$1`, e.campID)

		// A Subject header replaces it: shown as the header, not as rendered.
		e.db.MustExec(`UPDATE campaigns SET headers='[{"Subject":"Header subject"}]'::jsonb WHERE id=$1`, e.campID)
		_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if json.Unmarshal(data, &o) != nil || o.Subject != "Header subject" || o.Rendered {
			t.Fatalf("subject header over a template: %s", data)
		}
	}
	e.db.MustExec(`UPDATE campaigns SET subject='Stored subject', headers='[]'::jsonb WHERE id=$1`, e.campID)

	// A link that is only in the source of a condition that does not hold is not in the mail.
	e.db.MustExec(`UPDATE campaigns SET body='<p>News {{ if false }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("hidden link: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET body='<p>News {{ if true }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "ok" {
		t.Fatalf("shown link: %q", got)
	}
	// A body that only has the link while the campaign is "running" is fine for a start, and
	// not for a campaign with a send time, which the sender reads as "scheduled".
	e.db.MustExec(`UPDATE campaigns SET body='<p>News {{ if eq .Campaign.Status "running" }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "ok" {
		t.Fatalf("link while running, a start: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("link while running, a scheduled one: %q", got)
	}
	// A body that only has the link while "scheduled" loses it for the rest of the list when
	// the sender reads the campaign again as "running" (a restart while it is being sent,
	// LST-48 review): it warns.
	e.db.MustExec(`UPDATE campaigns SET body='<p>News {{ if eq .Campaign.Status "scheduled" }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("link while scheduled, a scheduled one: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL WHERE id=$1`, e.campID)

	// The mail is rendered as it is when sent, and only an <a> counts.
	e.db.MustExec(`UPDATE campaigns SET body='<p>News {{ if eq .Campaign.Status "draft" }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("link only while draft: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET body='<p data-unsubscribe="{{ UnsubscribeURL }}">News</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("address in an attribute: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET content_type='plain', body='News {{ UnsubscribeURL }}' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "ok" {
		t.Fatalf("plain text with the address: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET content_type='richtext' WHERE id=$1`, e.campID)
	// The tracking line reads the mail the same way: a pixel only while "running" is there for
	// a start, and is missing for a scheduled one, which is first read as "scheduled"; a pixel
	// only while "scheduled" is missing after a restart.
	view := func() string {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Tracking *struct {
				View string `json:"view"`
			} `json:"tracking"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil || o.Tracking == nil {
			t.Fatalf("%d %s", code, data)
		}
		return o.Tracking.View
	}
	e.db.MustExec(`UPDATE campaigns SET content_type='html', body='<p>News</p>{{ if eq .Campaign.Status "running" }}{{ TrackView }}{{ end }}' WHERE id=$1`, e.campID)
	if got := view(); got != "manual" {
		t.Fatalf("pixel while running, a start: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	if got := view(); got != "missing" {
		t.Fatalf("pixel while running, a scheduled one: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET body='<p>News</p>{{ if eq .Campaign.Status "scheduled" }}{{ TrackView }}{{ end }}' WHERE id=$1`, e.campID)
	if got := view(); got != "missing" {
		t.Fatalf("pixel while scheduled, a scheduled one: %q", got)
	}
	// The pixel and an untracked link only while "scheduled": both warnings, from two statuses.
	e.db.MustExec(`UPDATE campaigns SET body='<p>News</p>{{ if eq .Campaign.Status "scheduled" }}{{ TrackView }}<a href="https://example.invalid/article">article</a>{{ end }}' WHERE id=$1`, e.campID)
	{
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Tracking struct {
				View  string `json:"view"`
				Links struct {
					Untracked int `json:"untracked"`
				} `json:"links"`
			} `json:"tracking"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil || o.Tracking.View != "missing" || o.Tracking.Links.Untracked != 1 {
			t.Fatalf("pixel and link while scheduled: %d %s", code, data)
		}
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL, content_type='richtext' WHERE id=$1`, e.campID)

	// More untracked links than the sample of addresses: 20 in both statuses and one more in
	// each. All 22 are counted (LST-48 review).
	{
		var b strings.Builder
		b.WriteString(`<p>News</p>`)
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&b, `<a href="https://common.invalid/%d">c</a>`, i)
		}
		b.WriteString(`{{ if eq .Campaign.Status "scheduled" }}<a href="https://only-scheduled.invalid/">s</a>{{ else }}<a href="https://only-running.invalid/">r</a>{{ end }}`)
		e.db.MustExec(`UPDATE campaigns SET body=$2, send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID, b.String())
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Tracking struct {
				Links struct {
					Total     int `json:"total"`
					Untracked int `json:"untracked"`
				} `json:"links"`
			} `json:"tracking"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil || o.Tracking.Links.Untracked != 22 || o.Tracking.Links.Total != 22 {
			t.Fatalf("links past the sample: %d %s", code, data)
		}
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL, content_type='richtext' WHERE id=$1`, e.campID)

	// A body that cannot be rendered says nothing about it.
	e.db.MustExec(`UPDATE campaigns SET body='<p>{{ .Subscriber.NoSuchField }}</p>' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "" {
		t.Fatalf("unrenderable: %q", got)
	}
}

// A paused campaign with a send time is scheduled by the confirmation, so the window shows
// the time; with nothing left it only warns; and the paused time comes from the audit log.
func TestCampaignPreflightResume(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	id := strconv.Itoa(e.campID)
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	e.db.MustExec(`UPDATE campaigns SET status='paused', send_at=NOW() + INTERVAL '2 days', last_subscriber_id=0, to_send=1 WHERE id=$1`, e.campID)
	e.db.MustExec(`INSERT INTO axm_audit_log(action,target_type,target_id,created_at) VALUES('campaign.pause','campaign',$1,'2026-01-02 03:04:05+00')`, e.campID)
	read := func() (out struct {
		Mode     string                  `json:"mode"`
		SendAt   *string                 `json:"send_at"`
		PausedAt *string                 `json:"paused_at"`
		Checks   []models.PreflightCheck `json:"checks"`
	}) {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if code != http.StatusOK || json.Unmarshal(data, &out) != nil {
			t.Fatalf("%d %s", code, data)
		}
		return out
	}
	o := read()
	if o.Mode != "resume" || o.SendAt == nil || o.PausedAt == nil || !strings.HasPrefix(*o.PausedAt, "2026-01-02T03:04:05") {
		t.Fatalf("paused with a send time: %+v", o)
	}
	for _, c := range o.Checks {
		if c.Key == "schedule.past" {
			t.Fatalf("a time ahead: %+v", o.Checks)
		}
	}
	// Resumed since the pause, then stopped by the sender: the audit row is no longer the pause.
	e.db.MustExec(`INSERT INTO axm_audit_log(action,target_type,target_id,created_at) VALUES('campaign.start','campaign',$1,'2026-01-03 00:00:00+00')`, e.campID)
	if o = read(); o.PausedAt == nil || strings.HasPrefix(*o.PausedAt, "2026-01-02") {
		t.Fatalf("paused again by the sender: %+v", o.PausedAt)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() - INTERVAL '1 hour' WHERE id=$1`, e.campID)
	found := false
	for _, c := range read().Checks {
		found = found || (c.Key == "schedule.past" && c.Level == "warn")
	}
	if !found {
		t.Fatal("a resume with a time that has passed has no warning")
	}

	// The last list is gone: nothing is left, which only warns for a resume.
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL WHERE id=$1`, e.campID)
	e.db.MustExec(`DELETE FROM lists WHERE id=$1`, e.aList)
	member = e.as(e.superAdmin) // a member no longer reaches a campaign without lists
	got := map[string]string{}
	for _, c := range read().Checks {
		got[c.Key] = c.Level
	}
	if got["lists.none"] != "warn" {
		t.Fatalf("no list on resume: %v", got)
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
	// The sender of the test app runs a started campaign, so it may already be finished.
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
	if code, s := status(fmt.Sprintf(`{"status":"running","updated_at":%q}`, seen)); code != http.StatusOK || (s != "running" && s != "finished") {
		t.Fatalf("fresh: %d %s", code, s)
	}

	// Without the value it behaves as before.
	e.db.MustExec(`UPDATE campaigns SET status='draft', updated_at=updated_at + INTERVAL '5 microseconds' WHERE id=$1`, e.campID)
	if code, s := status(`{"status":"running"}`); code != http.StatusOK || (s != "running" && s != "finished") {
		t.Fatalf("no value: %d %s", code, s)
	}

	// A schedule that was taken off while the window was open is a 409 too, not the refusal
	// that a campaign without a send time would get.
	later := e.pfCampaign("regular", "draft", e.aList)
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, later)
	_, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", strconv.Itoa(later), "")
	var laterRead struct {
		UpdatedAt string `json:"updated_at"`
	}
	json.Unmarshal(data, &laterRead)
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL, updated_at=NOW() WHERE id=$1`, later)
	code, _ := e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, "/", strconv.Itoa(later), fmt.Sprintf(`{"status":"scheduled","updated_at":%q}`, laterRead.UpdatedAt))
	if code != http.StatusConflict {
		t.Fatalf("schedule removed: %d", code)
	}

	// An unreadable value is a bad request, not a server error. A campaign of its own: the
	// sender of the test app may still be finishing the one above.
	bad := e.pfCampaign("regular", "draft", e.aList)
	code, _ = e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, "/", strconv.Itoa(bad), `{"status":"running","updated_at":"not-a-time"}`)
	var st string
	e.get(&st, `SELECT status FROM campaigns WHERE id=$1`, bad)
	if code != http.StatusBadRequest || st != "draft" {
		t.Fatalf("garbage: %d %s", code, st)
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
			_, aud, err := e.core.GetPreflightAudience(ctx, c.id, c.after, c.after > 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%-26s run %d: %6.2fs  total %d remaining %d", c.name, i+1, time.Since(start).Seconds(), aud.Total, aud.Remaining)
		}
	}
}

// A status change runs on one connection: the pool may have only one free while the sender
// holds its lock connection (LST-48 review: with two connections and the sender's lock, a
// schedule waited for the sender to let go).
func TestCampaignStatusOneConnection(t *testing.T) {
	e := newSendsEnv(t, 1)
	draft, _ := e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaigns SET send_at = NOW() + INTERVAL '1 day' WHERE id = $1`, draft)
	var seen string
	e.get(&seen, `SELECT updated_at::TEXT FROM campaigns WHERE id = $1`, draft)
	e.db.SetMaxOpenConns(1)
	defer e.db.SetMaxOpenConns(8)

	run := func(status, seenAt, want string) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			out, err := e.core.UpdateCampaignStatus(draft, status, "", seenAt, false)
			if err == nil && out.Status != want {
				err = fmt.Errorf("status %q", out.Status)
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", status, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: waits for a second connection", status)
		}
	}
	run(models.CampaignStatusScheduled, seen, models.CampaignStatusScheduled)
	run(models.CampaignStatusDraft, "", models.CampaignStatusDraft)
	run(models.CampaignStatusScheduled, "", models.CampaignStatusScheduled)

	// A campaign that does not exist is still refused.
	if _, err := e.core.UpdateCampaignStatus(draft+1000, models.CampaignStatusRunning, "", "", false); err == nil {
		t.Fatal("unknown campaign")
	}
}

// A schedule from the pre-send confirmation whose send time has come by the database's clock
// is refused (412) unless the window said so: a browser clock behind the server's showed
// "later" while the campaign went out at once (LST-48 review). The API without updated_at is
// not judged.
func TestCampaignStatusSendTimePassed(t *testing.T) {
	e := newSendsEnv(t, 1)
	draft, _ := e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaigns SET send_at = NOW() - INTERVAL '4 seconds' WHERE id = $1`, draft)
	seen := func() string {
		var s string
		e.get(&s, `SELECT updated_at::TEXT FROM campaigns WHERE id = $1`, draft)
		return s
	}
	status := func() string {
		var s string
		e.get(&s, `SELECT status FROM campaigns WHERE id = $1`, draft)
		return s
	}
	for _, st := range []string{models.CampaignStatusScheduled, models.CampaignStatusRunning} {
		_, err := e.core.UpdateCampaignStatus(draft, st, "", seen(), false)
		var he *echo.HTTPError
		if !errors.As(err, &he) || he.Code != http.StatusPreconditionFailed || status() != "draft" {
			t.Fatalf("%s, not shown: %v %s", st, err, status())
		}
	}
	// Shown: it goes on.
	if _, err := e.core.UpdateCampaignStatus(draft, models.CampaignStatusScheduled, "", seen(), true); err != nil || status() != "scheduled" {
		t.Fatalf("shown: %v %s", err, status())
	}
	// The API, as before.
	e.db.MustExec(`UPDATE campaigns SET status='draft' WHERE id = $1`, draft)
	if _, err := e.core.UpdateCampaignStatus(draft, models.CampaignStatusScheduled, "", "", false); err != nil || status() != "scheduled" {
		t.Fatalf("api: %v %s", err, status())
	}
	// The send time comes while the request waits for the row lock: judged by the time after
	// the wait (LST-48 review), so still refused.
	e.db.MustExec(`UPDATE campaigns SET status='draft', send_at = clock_timestamp() + INTERVAL '1 second' WHERE id = $1`, draft)
	{
		hold, err := e.db.Beginx()
		if err != nil {
			t.Fatal(err)
		}
		hold.MustExec(`SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, draft)
		seenNow := seen()
		go func() { time.Sleep(2500 * time.Millisecond); hold.Rollback() }()
		_, err = e.core.UpdateCampaignStatus(draft, models.CampaignStatusScheduled, "", seenNow, false)
		var he *echo.HTTPError
		if !errors.As(err, &he) || he.Code != http.StatusPreconditionFailed || status() != "draft" {
			t.Fatalf("time came during the lock wait: %v %s", err, status())
		}
	}

	// A time ahead is not judged.
	e.db.MustExec(`UPDATE campaigns SET status='draft', send_at = NOW() + INTERVAL '1 hour' WHERE id = $1`, draft)
	if _, err := e.core.UpdateCampaignStatus(draft, models.CampaignStatusScheduled, "", seen(), false); err != nil || status() != "scheduled" {
		t.Fatalf("ahead: %v %s", err, status())
	}
}

// A draft whose From matches the team's From list in another spelling starts with the list's
// spelling (LST-45); the window shows that one, and a subject that prints the sender too
// (LST-48 review: the window said "North Desk", the mail came from "South Desk").
func TestCampaignPreflightFromListSpelling(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	e.a.cfg.FromAddresses = []string{"South Desk <press@crevasse.ai>"}
	defer func() { e.a.cfg.FromAddresses = nil }()
	e.db.MustExec(`UPDATE campaigns SET from_email='North Desk <press@crevasse.ai>', subject='From {{ .Campaign.FromEmail }}' WHERE id=$1`, e.campID)
	code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", strconv.Itoa(e.campID), "")
	var o struct {
		From    string                  `json:"from_email"`
		Subject string                  `json:"subject"`
		Checks  []models.PreflightCheck `json:"checks"`
	}
	if code != http.StatusOK || json.Unmarshal(data, &o) != nil || o.From != "South Desk <press@crevasse.ai>" || o.Subject != "From South Desk <press@crevasse.ai>" {
		t.Fatalf("list spelling: %d %s", code, data)
	}
	for _, c := range o.Checks {
		if c.Key == "from.refused" {
			t.Fatalf("refused: %s", data)
		}
	}
	// Not on the list: refused, as the status change refuses it.
	e.db.MustExec(`UPDATE campaigns SET from_email='Other <other@crevasse.ai>' WHERE id=$1`, e.campID)
	_, data = e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", strconv.Itoa(e.campID), "")
	o.Checks = nil
	found := false
	if json.Unmarshal(data, &o) == nil {
		for _, c := range o.Checks {
			found = found || (c.Key == "from.refused" && c.Level == "block")
		}
	}
	if !found {
		t.Fatalf("not on the list: %s", data)
	}
}

// What varies between the mails is measured by rendering at two moments, not read from the
// templates (LST-48 reviews): an SMTP header is never rendered, so a Date written in it does not
// vary; a header that prints a subject with the time ({{ .Subject }}) does; a Bcc that shares an
// address with a varying Cc stays fixed.
func TestPreflightEnvelopeVaries(t *testing.T) {
	e := newPreflightApp(t)
	var camp models.Campaign
	camp.ID, camp.Messenger, camp.ContentType, camp.Body = e.campID, "email", "richtext", "<p>Hi</p>"
	camp.UUID = "9a7c9a2e-0000-4000-8000-000000000001"
	camp.FromEmail = "News <news@crevasse.ai>"
	camp.UpdatedAt = null.TimeFrom(time.Now())
	statuses := []string{"scheduled", "running"}
	vary := func(c models.Campaign, server map[string]string) []string {
		t.Helper()
		return sortedKeys(envelopeVaries(e.a, c, statuses, c.FromEmail, server, false))
	}

	c := camp
	c.Subject = "Weekly update"
	if got := vary(c, map[string]string{"Subject": `Standard {{ Date "2006-01-02" }}`}); len(got) != 0 {
		t.Fatalf("SMTP subject with Date written in it: %v", got)
	}

	c = camp
	c.Subject = `Issue {{ Date "15:04:05" }}`
	c.Headers = models.Headers{{"Subject": "[News] {{ .Subject }}"}}
	if got := vary(c, nil); len(got) != 1 || got[0] != "subject" {
		t.Fatalf("header of a subject with the time: %v", got)
	}

	c = camp
	c.Subject = "Fixed"
	c.Headers = models.Headers{{"Cc": "{{ if .Campaign.StartedAt.Valid }}desk@crevasse.ai{{ else }}support@crevasse.ai{{ end }}"}}
	if got := vary(c, map[string]string{"Bcc": "desk@crevasse.ai, archive@crevasse.ai"}); len(got) != 1 || got[0] != "cc" {
		t.Fatalf("varying cc beside a fixed bcc with a common address: %v", got)
	}
}

// The unsubscribe link is checked for real recipients too, not only for the example subscriber
// (LST-48 review: a link shown only with a city attribute passed for the example, which has a
// city, and was missing for every real recipient). A Subject header that reads the rendered
// body ({{ .Body }}) is rendered after the body, as the send does.
func TestCampaignPreflightRealRecipients(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	id := strconv.Itoa(e.campID)
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	read := func() (o struct {
		Subject string                  `json:"subject"`
		Checks  []models.PreflightCheck `json:"checks"`
	}) {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil {
			t.Fatalf("%d %s", code, data)
		}
		return o
	}
	level := func(checks []models.PreflightCheck, key string) string {
		for _, c := range checks {
			if c.Key == key {
				return c.Level
			}
		}
		return ""
	}
	e.db.MustExec(`UPDATE subscribers SET attribs='{}'::jsonb WHERE id IN (SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1)`, e.aList)
	e.db.MustExec(`UPDATE campaigns SET content_type='html', body='<p>Hi</p>{{ if .Subscriber.Attribs.city }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}' WHERE id=$1`, e.campID)
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("link only with a city, recipients without: %q", got)
	}
	e.db.MustExec(`UPDATE subscribers SET attribs='{"city":"Seoul"}'::jsonb WHERE id IN (SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1)`, e.aList)
	// The bare example has no city either: still a warning, as some recipient may not.
	if got := level(read().Checks, "unsubscribe.missing"); got != "warn" {
		t.Fatalf("link only with a city: %q", got)
	}
	e.db.MustExec(`UPDATE campaigns SET body='<p>Hi</p><a href="{{ UnsubscribeURL }}">leave</a>', headers='[{"Subject":"Review {{ if .Body }}newsletter{{ else }}empty draft{{ end }}"}]'::jsonb WHERE id=$1`, e.campID)
	o := read()
	if level(o.Checks, "unsubscribe.missing") != "ok" || o.Subject != "Review newsletter" {
		t.Fatalf("link always, subject header of the body: %+v", o)
	}
}

// The sample follows the sender's rule (a double opt-in list sends only to the confirmed), and a
// link under a condition on the subscriber is never a pass on the sample (LST-48 review).
func TestCampaignPreflightUnsubscribeSample(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	id := strconv.Itoa(e.campID)
	read := func() string {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Checks []models.PreflightCheck `json:"checks"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil {
			t.Fatalf("%d %s", code, data)
		}
		out := []string{}
		for _, c := range o.Checks {
			if strings.HasPrefix(c.Key, "unsubscribe.") {
				out = append(out, c.Key+"="+c.Level)
			}
		}
		return strings.Join(out, ",")
	}
	// Ten unconfirmed first, then two confirmed with the compact edition, on a double opt-in list.
	e.db.MustExec(`UPDATE lists SET optin='double' WHERE id=$1`, e.aList)
	for i := 0; i < 10; i++ {
		e.pfSub("enabled", "", map[int]string{e.aList: "unconfirmed"})
	}
	for i := 0; i < 2; i++ {
		sid := e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
		e.db.MustExec(`UPDATE subscribers SET attribs='{"compact":true}'::jsonb WHERE id=$1`, sid)
	}
	e.db.MustExec(`UPDATE campaigns SET content_type='html', body='<p>News</p>{{ if not .Subscriber.Attribs.compact }}<a href="{{ UnsubscribeURL }}">leave</a>{{ end }}' WHERE id=$1`, e.campID)
	if got := read(); got != "unsubscribe.missing=warn" {
		t.Fatalf("the two who get mail have no link: %q", got)
	}
	// Only someone beyond the sample lacks it: the window cannot vouch for all, it warns.
	e.db.MustExec(`UPDATE lists SET optin='single' WHERE id=$1`, e.aList)
	e.db.MustExec(`UPDATE subscribers SET attribs='{}'::jsonb WHERE id IN (SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1)`, e.aList)
	if got := read(); got != "unsubscribe.perSubscriber=warn" {
		t.Fatalf("link under a subscriber condition, present for the sample: %q", got)
	}
	// Printed always: a pass.
	e.db.MustExec(`UPDATE campaigns SET body='<p>News</p><a href="{{ UnsubscribeURL }}">leave</a>' WHERE id=$1`, e.campID)
	if got := read(); got != "unsubscribe.missing=ok" {
		t.Fatalf("link always: %q", got)
	}
}

// A template that includes the body (and its link) only for the full edition; ten full, then
// one compact. Never a pass; and on resume after the ten, the one left is in the sample, and
// its mail has no link (LST-48 review).
func TestCampaignPreflightUnsubscribeTemplateResume(t *testing.T) {
	e := newPreflightApp(t)
	member := e.as(e.ua)
	id := strconv.Itoa(e.campID)
	read := func() string {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, member, http.MethodGet, "/", id, "")
		var o struct {
			Checks []models.PreflightCheck `json:"checks"`
		}
		if code != http.StatusOK || json.Unmarshal(data, &o) != nil {
			t.Fatalf("%d %s", code, data)
		}
		out := []string{}
		for _, c := range o.Checks {
			if strings.HasPrefix(c.Key, "unsubscribe.") {
				out = append(out, c.Key+"="+c.Level)
			}
		}
		return strings.Join(out, ",")
	}
	var last int
	for i := 0; i < 10; i++ {
		last = e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	}
	compact := e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	e.db.MustExec(`UPDATE subscribers SET attribs='{"compact":true}'::jsonb WHERE id=$1`, compact)
	var tpl int
	e.get(&tpl, `INSERT INTO templates(name,type,subject,body) VALUES('editions','campaign','',
		'<html><body>{{ if not .Subscriber.Attribs.compact }}{{ template "content" . }}{{ else }}<p>Compact</p>{{ end }}</body></html>') RETURNING id`)
	e.db.MustExec(`UPDATE campaigns SET content_type='html', template_id=$2, body='<p>Full</p><a href="{{ UnsubscribeURL }}">leave</a>' WHERE id=$1`, e.campID, tpl)
	if got := read(); got != "unsubscribe.perSubscriber=warn" {
		t.Fatalf("body with the link included only for the full edition: %q", got)
	}
	// Paused after the ten: the compact one is the only one left, and is in the sample.
	e.db.MustExec(`UPDATE campaigns SET status='paused', last_subscriber_id=$2, to_send=11, sent=10 WHERE id=$1`, e.campID, last)
	if got := read(); got != "unsubscribe.missing=warn" {
		t.Fatalf("resume, the one left gets no link: %q", got)
	}
}
