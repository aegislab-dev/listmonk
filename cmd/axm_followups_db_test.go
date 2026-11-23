package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// followupEnv is the reaction fixture with a running campaign manager, so that a follow-up
// can be made the way the app does and then sent by the real sender.
type followupEnv struct {
	*reactionEnv
	gate  *gateMessenger
	store *store
	logs  *strings.Builder
}

func newFollowupEnv(t *testing.T) *followupEnv { return newScanningFollowupEnv(t, false) }

// newScanningFollowupEnv lets the sender scan for campaigns on its own when scan is set.
func newScanningFollowupEnv(t *testing.T, scan bool) *followupEnv {
	t.Helper()
	e := &followupEnv{reactionEnv: newReactionEnv(t), gate: &gateMessenger{}, logs: &strings.Builder{}}
	e.db.MustExec(`UPDATE campaigns SET from_email = 'Synthetic <sender@synthetic-sender.dev>' WHERE id = $1`, e.campID)
	e.a.cfg.FromEmail = "Synthetic <sender@synthetic-sender.dev>"
	e.store = newManagerStore(e.q, e.a.core, nil, e.db)
	// Without scanning, the tests call NextCampaigns when they want the sender to look.
	e.a.manager = e.startManagerWith(e.store, e.gate, io.Discard, func(c *manager.Config) { c.ScanCampaigns = scan })
	t.Cleanup(e.a.manager.Close)
	return e
}

func (e *followupEnv) callGet(user auth.User, id int) (int, models.FollowupInfo) {
	e.t.Helper()
	code, data := e.call(e.a.GetCampaignFollowup, user, http.MethodGet, fmt.Sprintf("/api/campaigns/%d/followup", id), fmt.Sprint(id), "")
	var info models.FollowupInfo
	if code == http.StatusOK {
		if err := json.Unmarshal(data, &info); err != nil {
			e.t.Fatal(err, string(data))
		}
	}
	return code, info
}

func (e *followupEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	e.get(&n, query, args...)
	return n
}

func (e *followupEnv) members(list int) []int {
	return e.sendsEnv.ids(`SELECT subscriber_id FROM subscriber_lists WHERE list_id = $1 AND status != 'unsubscribed' ORDER BY subscriber_id`, list)
}

func (e *followupEnv) makeFollowup(user auth.User, body string) models.ReactionResult {
	e.t.Helper()
	got := e.make(user, body)
	if got.Followup == nil {
		e.t.Fatalf("no follow-up in %+v", got)
	}
	return got
}

func TestFollowupMakeCopiesCampaignAndLinks(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	parent := e.campID
	e.db.MustExec(`UPDATE campaigns SET altbody = 'alt', tags = '{promo}', subject = 'Hello {{ .Subscriber.FirstName }}' WHERE id = $1`, parent)
	var media int
	e.get(&media, `INSERT INTO media (uuid, filename, thumb, provider) VALUES (gen_random_uuid(), 'a.png', 'a.png', 'filesystem') RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_media (campaign_id, media_id, filename) VALUES ($1, $2, 'a.png')`, parent, media)

	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	wantSame(t, "list members", e.members(got.List.ID), []int{e.ids[2], e.ids[3]})

	var c struct {
		Name, Subject, FromEmail, Body, Status, Type, ContentType, Messenger string
		AltBody                                                              *string
		SendAt                                                               *time.Time
		Tags                                                                 pq.StringArray
		TemplateID                                                           *int
	}
	row := e.db.QueryRowx(`SELECT name, subject, from_email, body, status, type, content_type, messenger, altbody, send_at, tags, template_id FROM campaigns WHERE id = $1`, got.Followup.ID)
	if err := row.Scan(&c.Name, &c.Subject, &c.FromEmail, &c.Body, &c.Status, &c.Type, &c.ContentType, &c.Messenger, &c.AltBody, &c.SendAt, &c.Tags, &c.TemplateID); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Subject   string
		FromEmail string `db:"from_email"`
		Body      string
	}
	e.get(&p, `SELECT subject, from_email, body FROM campaigns WHERE id = $1`, parent)
	for what, ok := range map[string]bool{
		"name": c.Name == "Synthetic engagement 후속" && got.Followup.Name == c.Name, "status": c.Status == "draft" && c.Type == "regular",
		"subject": c.Subject == p.Subject, "from": c.FromEmail == p.FromEmail, "body": c.Body == p.Body,
		"altbody": c.AltBody != nil && *c.AltBody == "alt", "send_at": c.SendAt == nil,
		"tags": sameSet(c.Tags, []string{"promo", models.FollowupTag}), "type": c.ContentType == "html" && c.Messenger == "email",
	} {
		if !ok {
			t.Fatalf("draft %s: %+v (parent %+v)", what, c, p)
		}
	}
	wantSame(t, "draft lists", e.sendsEnv.ids(`SELECT list_id FROM campaign_lists WHERE campaign_id = $1`, got.Followup.ID), []int{got.List.ID})
	wantSame(t, "draft media", e.sendsEnv.ids(`SELECT media_id FROM campaign_media WHERE campaign_id = $1`, got.Followup.ID), []int{media})

	var link struct {
		Parent    int
		List      string
		Condition string
		Counts    string
	}
	row = e.db.QueryRowx(`SELECT parent_campaign_id, list_uuid::TEXT, condition::TEXT, counts::TEXT FROM axm_campaign_followups WHERE campaign_id = $1`, got.Followup.ID)
	if err := row.Scan(&link.Parent, &link.List, &link.Condition, &link.Counts); err != nil {
		t.Fatal(err)
	}
	if link.Parent != parent || link.List != got.List.UUID || !strings.Contains(link.Condition, `"open": "no"`) || !strings.Contains(link.Counts, `"eligible": 2`) ||
		!strings.Contains(link.Counts, `"received": 8`) {
		t.Fatalf("link: %+v", link)
	}

	// Two audit rows: the list, which names the draft, and the draft, which names its original.
	var listDetail, followDetail string
	e.get(&listDetail, `SELECT detail::TEXT FROM axm_audit_log WHERE action = $1 AND target_id = $2`, auditListCreate, got.List.ID)
	e.get(&followDetail, `SELECT detail::TEXT FROM axm_audit_log WHERE action = $1 AND target_id = $2 AND target_type = 'campaign'`, auditCampaignFollowup, got.Followup.ID)
	if !strings.Contains(listDetail, fmt.Sprintf(`"followup_campaign": %d`, got.Followup.ID)) ||
		!strings.Contains(followDetail, fmt.Sprintf(`"id": %d`, parent)) || !strings.Contains(followDetail, `"open": "no"`) {
		t.Fatalf("audit: %s | %s", listDetail, followDetail)
	}
	if strings.Contains(listDetail+followDetail, "@example.test") {
		t.Fatal("audit holds an address")
	}

	// A list made without the checkbox is the old behavior: no campaign, no link.
	campaigns := e.count(`SELECT COUNT(*) FROM campaigns`)
	plain := e.make(e.admin, `{"no_team":true}`)
	if plain.Followup != nil || e.count(`SELECT COUNT(*) FROM campaigns`) != campaigns || e.count(`SELECT COUNT(*) FROM axm_campaign_followups`) != 1 {
		t.Fatal("a list without the follow-up option made a campaign")
	}
}

func TestFollowupRefusals(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	counts := func() [3]int {
		return [3]int{e.count(`SELECT COUNT(*) FROM lists`), e.count(`SELECT COUNT(*) FROM campaigns`), e.count(`SELECT COUNT(*) FROM axm_campaign_followups`)}
	}
	before := counts()
	refuse := func(name string, code int, id int, body string, user auth.User) {
		t.Helper()
		if got, _ := e.sendRequest(true, user, id, body); got != code {
			t.Fatalf("%s: %d, want %d", name, got, code)
		}
		if counts() != before {
			t.Fatalf("%s left something behind: %v, was %v", name, counts(), before)
		}
	}

	// Not finished: the same campaign while it is still running or only cancelled.
	for _, status := range []string{"running", "paused", "cancelled"} {
		e.db.MustExec(`UPDATE campaigns SET status = $2::campaign_status WHERE id = $1`, e.campID, status)
		refuse("parent "+status, http.StatusBadRequest, e.campID, `{"no_team":true,"followup":true}`, e.admin)
	}
	e.db.MustExec(`UPDATE campaigns SET status = 'finished' WHERE id = $1`, e.campID)

	// No condition that matches anyone leaves nothing behind either.
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = ANY($1::INT[])`, pq.Array(e.ids))
	refuse("nobody", http.StatusBadRequest, e.campID, `{"no_team":true,"followup":true}`, e.admin)
	e.db.MustExec(`UPDATE subscribers SET status = 'enabled' WHERE id = ANY($1::INT[])`, pq.Array(e.ids))

	// Without the campaign permission the list can still be made, but not the draft.
	user := e.as(e.ua)
	delete(user.PermissionsMap, auth.PermCampaignsManage)
	refuse("no campaign permission", http.StatusForbidden, e.campID, `{"team":"팀:나","followup":true}`, user)
	if got := e.make(user, `{"team":"팀:나"}`); got.Followup != nil {
		t.Fatal("a list made a campaign")
	}
	// The first follow-up works; a second one for the same campaign and one for the follow-up
	// itself are refused and leave nothing behind.
	first := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	before = counts()
	refuse("second follow-up", http.StatusConflict, e.campID, `{"open":"no","no_team":true,"followup":true}`, e.admin)
	e.db.MustExec(`UPDATE campaigns SET status = 'finished' WHERE id = $1`, first.Followup.ID)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, first.Followup.ID, e.ids[2])
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id) VALUES ($1, $2)`, first.Followup.ID, e.aList)
	refuse("follow-up of a follow-up", http.StatusBadRequest, first.Followup.ID, `{"no_team":true,"followup":true}`, e.admin)

	// The dialog is told why.
	pre := e.preview(e.admin, `{"tracking_only":true}`)
	if pre.Followup == nil || pre.Followup.Available || pre.Followup.Existing == nil || pre.Followup.Existing.ID != first.Followup.ID || pre.Followup.Reason != "exists" {
		t.Fatalf("offer with an existing follow-up: %+v", pre.Followup)
	}
}

func TestFollowupOffer(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	offer := func(user auth.User) *models.ReactionFollowup {
		t.Helper()
		return e.preview(user, `{"tracking_only":true}`).Followup
	}
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '5 days' WHERE campaign_id = $1`, e.campID)
	o := offer(e.admin)
	if !o.Available || o.WarnDays != models.FollowupWarnDays || o.DaysSince == nil || *o.DaysSince != 5 || o.DefaultName != "Synthetic engagement 후속" {
		t.Fatalf("offer: %+v", o)
	}
	e.db.MustExec(`UPDATE campaigns SET status = 'running' WHERE id = $1`, e.campID)
	if o := offer(e.admin); o.Available || o.Reason != "not_finished" {
		t.Fatalf("running: %+v", o)
	}
	e.db.MustExec(`UPDATE campaigns SET status = 'finished' WHERE id = $1`, e.campID)
	user := e.as(e.ua)
	delete(user.PermissionsMap, auth.PermCampaignsManage)
	if o := offer(user); o.Available || o.Reason != "forbidden" {
		t.Fatalf("member without campaigns:manage: %+v", o)
	}
	if o := offer(e.as(e.ua)); !o.Available {
		t.Fatalf("member: %+v", o)
	}
}

// The members are the two people who did not open the campaign (Charlie, Daniel) and a person
// added by hand who never got it.
func (e *followupEnv) followupWithManualMember(body string) (models.ReactionResult, int) {
	got := e.makeFollowup(e.admin, body)
	manual := e.sendsEnv.ids(`SELECT id FROM subscribers WHERE name = 'Ian'`)[0]
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, manual, got.List.ID)
	return got, manual
}

// held is who the check holds back from a follow-up campaign now.
func (e *followupEnv) held(camp int) []int {
	return e.sendsEnv.ids(`SELECT subscriber_id FROM axm_campaign_followup_holds WHERE campaign_id = $1 ORDER BY subscriber_id`, camp)
}

// send has the sender start the follow-up and read its first batch, as it does at the send time,
// and returns who it would send to; the people it left out are then in held. The campaign is a
// draft again afterwards.
func (e *followupEnv) send(camp int) []int {
	e.t.Helper()
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		e.t.Fatal(err)
	}
	subs, err := e.store.NextSubscribers(camp, 0, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	e.db.MustExec(`UPDATE campaigns SET status = 'draft', send_at = NULL, last_subscriber_id = 0 WHERE id = $1`, camp)
	ids := []int{}
	for _, s := range subs {
		ids = append(ids, s.ID)
	}
	return ids
}

// subscriptions is every subscription row with its status and last change, to show that the
// check does not write to them.
func (e *followupEnv) subscriptions() string {
	e.t.Helper()
	var s string
	e.get(&s, `SELECT COALESCE(string_agg(list_id || ':' || subscriber_id || ':' || status || ':' || updated_at, ',' ORDER BY list_id, subscriber_id), '') FROM subscriber_lists`)
	return s
}

// The sender judges the follow-up's people when it reads them, just before sending: who opened the
// original since the draft was made, or left its list, is left out and recorded; nothing is judged
// before (a draft, a schedule that is not due); subscriptions are not changed.
func TestFollowupRecheckHoldsWhoReactedOrOptedOut(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got, manual := e.followupWithManualMember(`{"open":"no","no_team":true,"followup":true}`)
	camp, list := got.Followup.ID, got.List.ID
	charlie, daniel := e.ids[2], e.ids[3]
	wantSame(t, "before", e.members(list), []int{charlie, daniel, manual})

	// Something changed since the list was made.
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, charlie, e.aList)

	// Nothing is judged before the send: a draft, a schedule that is not due.
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() + INTERVAL '1 hour' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`UPDATE campaigns SET status = 'draft', send_at = NULL WHERE id = $1`, camp)
	wantSame(t, "nobody left out yet", e.held(camp), nil)
	if code, info := e.callGet(e.admin, camp); code != 200 || info.Pending == nil || info.Pending.Reacted != 1 || info.Pending.OptedOut != 1 || info.Pending.Blocked != 0 {
		t.Fatalf("pending: %d %+v", code, info.Pending)
	}

	subs := e.subscriptions()
	wantSame(t, "sent to", e.send(camp), []int{manual})
	wantSame(t, "left out", e.held(camp), []int{charlie, daniel})
	wantSame(t, "the list is unchanged", e.members(list), []int{charlie, daniel, manual})
	if e.subscriptions() != subs {
		t.Fatal("the send changed subscriptions")
	}
	code, info := e.callGet(e.admin, camp)
	if code != 200 || info.Recheck == nil || info.Recheck.Reacted != 1 || info.Recheck.OptedOut != 1 || info.Recheck.Blocked != 0 || info.Recheck.Runs != 1 || info.RecheckedAt == nil {
		t.Fatalf("recorded: %d %+v", code, info.Recheck)
	}
}

// The sender leaves people out without touching their subscriptions and judges them again at every
// read: someone who qualifies again (a subscription that came back) is sent to, whatever was done
// to their row meanwhile (an import that rewrote it with the same status), and someone who
// unsubscribed from the follow-up list meanwhile is not, whether the row changed or not.
func TestFollowupHeldPeopleAreGivenBackOrKept(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp, list := got.Followup.ID, got.List.ID
	charlie, daniel := e.ids[2], e.ids[3]
	// Both leave the original's list and are left out; their follow-up subscriptions stay as they were.
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = ANY($1::INT[]) AND list_id = $2`, pq.Array([]int{charlie, daniel}), e.aList)
	subs := e.subscriptions()
	wantSame(t, "sent to", e.send(camp), nil)
	wantSame(t, "left out", e.held(camp), []int{charlie, daniel})
	if e.subscriptions() != subs {
		t.Fatal("the send changed subscriptions")
	}

	// Daniel unsubscribes from the follow-up list, with the row's last change left as it was; an
	// import rewrites Charlie's row with the status it had. Both come back to the original's list.
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, daniel, list)
	e.db.MustExec(`UPDATE subscriber_lists SET updated_at = NOW() + INTERVAL '1 minute' WHERE subscriber_id = $1 AND list_id = $2`, charlie, list)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'confirmed' WHERE subscriber_id = ANY($1::INT[]) AND list_id = $2`, pq.Array([]int{charlie, daniel}), e.aList)
	wantSame(t, "sent to", e.send(camp), []int{charlie})
	var st string
	e.get(&st, `SELECT status::TEXT FROM subscriber_lists WHERE subscriber_id = $1 AND list_id = $2`, daniel, list)
	if st != "unsubscribed" {
		t.Fatalf("Daniel's unsubscribe became %s", st)
	}
}

func TestFollowupConditionsStayTrue(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	// Opened and did not click: Alice. Brenda clicked and Charlie never opened.
	got := e.makeFollowup(e.admin, `{"open":"yes","click":"no","no_team":true,"followup":true}`)
	wantSame(t, "members", e.members(got.List.ID), []int{e.ids[0]})
	wantSame(t, "still matching", e.send(got.Followup.ID), []int{e.ids[0]})
	// She clicks: the condition no longer holds.
	var link int
	e.get(&link, `SELECT id FROM links LIMIT 1`)
	e.db.MustExec(`INSERT INTO link_clicks (campaign_id, link_id, subscriber_id) VALUES ($1, $2, $3)`, e.campID, link, e.ids[0])
	wantSame(t, "after the click", e.send(got.Followup.ID), nil)
	wantSame(t, "left out", e.held(got.Followup.ID), []int{e.ids[0]})
}


func (e *followupEnv) addRunning() int {
	id, _ := e.addCampaign("running")
	return id
}

// The whole way, with the real sender: the follow-up goes to the people left on the list after
// the check, and not to the one who opened the campaign in between.
func TestFollowupSendsToWhoIsLeft(t *testing.T) {
	t.Parallel()
	e := newScanningFollowupEnv(t, true)
	got, manual := e.followupWithManualMember(`{"open":"no","no_team":true,"followup":true}`)
	charlie, daniel := e.ids[2], e.ids[3]
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	// Charlie's address is on another list as well; he is still reachable.
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() + INTERVAL '1 second' WHERE id = $1`, got.Followup.ID)

	waitFor(t, "the follow-up to finish", func() bool {
		var s string
		e.get(&s, `SELECT status FROM campaigns WHERE id = $1`, got.Followup.ID)
		return s == "finished"
	})
	// Records are saved in the background shortly after the status changes.
	waitFor(t, "the send records", func() bool {
		return e.count(`SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id = $1`, got.Followup.ID) == 2
	})
	wantSame(t, "sent to", e.gate.sentIDs(), []int{charlie, manual})
	wantSame(t, "send records", e.sendsEnv.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1 ORDER BY subscriber_id`, got.Followup.ID), []int{charlie, manual})
}

func TestFollowupUnsubscribeAlsoCoversTheOriginal(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	charlie := e.ids[2]
	var followUUID, parentUUID, subUUID string
	e.get(&followUUID, `SELECT uuid FROM campaigns WHERE id = $1`, got.Followup.ID)
	e.get(&parentUUID, `SELECT uuid FROM campaigns WHERE id = $1`, e.campID)
	e.get(&subUUID, `SELECT uuid FROM subscribers WHERE id = $1`, charlie)
	var dynamic int
	e.get(&dynamic, `SELECT id FROM lists WHERE axm_filter IS NOT NULL`)
	status := func(list int) string {
		var s string
		if err := e.db.Get(&s, `SELECT status FROM subscriber_lists WHERE subscriber_id = $1 AND list_id = $2`, charlie, list); err != nil {
			return "none"
		}
		return s
	}

	// An ordinary campaign behaves as before: only its own lists.
	if _, err := e.q.UnsubscribeByCampaign.Exec(parentUUID, subUUID, false); err != nil {
		t.Fatal(err)
	}
	if status(e.aList) != "unsubscribed" || status(got.List.ID) != "confirmed" {
		t.Fatalf("original: a=%s followup=%s", status(e.aList), status(got.List.ID))
	}
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'confirmed' WHERE subscriber_id = $1 AND list_id = $2`, charlie, e.aList)
	e.db.MustExec(`DELETE FROM subscriber_lists WHERE subscriber_id = $1 AND list_id = $2`, charlie, dynamic)

	// The link of the follow-up covers its own list and the original's lists, a dynamic one included.
	if _, err := e.q.UnsubscribeByCampaign.Exec(followUUID, subUUID, false); err != nil {
		t.Fatal(err)
	}
	if status(got.List.ID) != "unsubscribed" || status(e.aList) != "unsubscribed" || status(dynamic) != "unsubscribed" {
		t.Fatalf("follow-up: followup=%s a=%s dynamic=%s", status(got.List.ID), status(e.aList), status(dynamic))
	}
}

func TestFollowupDeletesAndInfo(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	if code, info := e.callGet(e.admin, e.campID); code != 200 || info.Role != "none" {
		t.Fatalf("no link: %d %+v", code, info)
	}
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	code, parent := e.callGet(e.admin, e.campID)
	if code != 200 || parent.Role != "parent" || parent.Followup.ID != got.Followup.ID || parent.Followup.Status != "draft" {
		t.Fatalf("parent: %d %+v", code, parent)
	}
	code, follow := e.callGet(e.admin, got.Followup.ID)
	if code != 200 || follow.Role != "followup" || follow.Parent.ID != e.campID || follow.Condition.Open != "no" || follow.List == nil ||
		follow.List.ID != got.List.ID || follow.List.Count != 2 || follow.ListsChanged {
		t.Fatalf("follow-up: %d %+v", code, follow)
	}
	// A reader outside the campaign's team does not get it.
	hidden := e.as(e.ua)
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1 AND list_id <> $2`, got.Followup.ID, got.List.ID)
	if c, _ := e.callGet(hidden, got.Followup.ID); c != http.StatusForbidden {
		// The follow-up list has no team tag here, so the member cannot read the campaign.
		t.Fatalf("outside reader: %d", c)
	}
	// Another list on the follow-up is reported.
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id) VALUES ($1, $2)`, got.Followup.ID, e.aList)
	if _, i := e.callGet(e.admin, got.Followup.ID); !i.ListsChanged {
		t.Fatal("changed lists not reported")
	}

	// The list goes away: the link stays and reading it still works.
	e.db.MustExec(`DELETE FROM lists WHERE id = $1`, got.List.ID)
	if c, i := e.callGet(e.admin, got.Followup.ID); c != 200 || i.List != nil || !i.ListsChanged || i.Role != "followup" {
		t.Fatalf("without the list: %d %+v", c, i)
	}
	// The follow-up goes away: the original can have a new one.
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, got.Followup.ID)
	if e.count(`SELECT COUNT(*) FROM axm_campaign_followups`) != 0 {
		t.Fatal("the link outlived the follow-up")
	}
	again := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	// The original goes away: the follow-up stays an ordinary campaign.
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	if e.count(`SELECT COUNT(*) FROM campaigns WHERE id = $1`, again.Followup.ID) != 1 || e.count(`SELECT COUNT(*) FROM axm_campaign_followups`) != 0 {
		t.Fatal("deleting the original")
	}
	if c, i := e.callGet(e.admin, again.Followup.ID); c != 200 || i.Role != "none" {
		t.Fatalf("orphan: %d %+v", c, i)
	}
}

func TestFollowupMemberMakesForOwnTeam(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.member, `{"team":"팀:나","followup":true}`)
	if !sameSet(got.List.Tags, []string{models.ReactionListTag, "팀:가"}) {
		t.Fatalf("tags: %v", got.List.Tags)
	}
	wantSame(t, "members", e.members(got.List.ID), []int{e.ids[0], e.ids[2], e.ids[3]})
	// The draft is on the team's list, so the member sees and can read it.
	if c, info := e.callGet(e.as(e.ua), got.Followup.ID); c != 200 || info.Role != "followup" {
		t.Fatalf("member reads the draft: %d %+v", c, info)
	}
}

func TestFollowupConcurrentMakeCreatesOne(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	lists, campaigns := e.count(`SELECT COUNT(*) FROM lists`), e.count(`SELECT COUNT(*) FROM campaigns`)
	var wg sync.WaitGroup
	codes := make(chan int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true,"followup":true}`)
			codes <- c
		}()
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("code %d", c)
		}
	}
	if ok != 1 || conflict != 3 || e.count(`SELECT COUNT(*) FROM lists`) != lists+1 || e.count(`SELECT COUNT(*) FROM campaigns`) != campaigns+1 ||
		e.count(`SELECT COUNT(*) FROM axm_campaign_followups`) != 1 {
		t.Fatalf("ok=%d conflict=%d lists=%d campaigns=%d", ok, conflict, e.count(`SELECT COUNT(*) FROM lists`), e.count(`SELECT COUNT(*) FROM campaigns`))
	}
}

func TestFollowupSchemaAndMigration(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	root := repoRoot(t)
	schema, _ := os.ReadFile(filepath.Join(root, "schema.sql"))
	mig, _ := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.16.go"))
	const start, end = "-- AXM campaign follow-ups (LST-47). Keep this block identical to V6_2_1_axm_16.", "-- End of AXM campaign follow-ups."
	_, tail, ok := strings.Cut(string(schema), start)
	block, _, endOK := strings.Cut(tail, end)
	if !ok || !endOK || !strings.Contains(string(mig), "`"+start+block+end+"`") {
		t.Fatal("schema and migration SQL of axm_campaign_followups differ")
	}
	e.db.MustExec(`DROP TABLE axm_campaign_followup_holds, axm_campaign_followup_members, axm_campaign_followup_reactions, axm_campaign_followups, axm_tracking_off`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_16(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if e.count(`SELECT COUNT(*) FROM axm_campaign_followups`)+e.count(`SELECT COUNT(*) FROM axm_campaign_followup_holds`) != 0 {
		t.Fatal("rows after the migration")
	}
	if e.count(`SELECT COUNT(*) FROM pg_depend d JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S' WHERE d.refobjid = 'axm_campaign_followups'::regclass`) != 0 {
		t.Fatal("the table owns a sequence")
	}
	var last string
	last = migList[len(migList)-1].version
	if last != "v6.2.1-axm.16" {
		t.Fatal(last)
	}
}

// A reader who cannot open the other campaign of a link learns nothing about it.
func TestFollowupHidesWhatTheReaderCannotOpen(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	// The reader reaches the follow-up and its list only: the original is out of reach.
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id) VALUES ($1, $2)`, e.campID, e.bList)
	e.db.MustExec(`UPDATE lists SET tags = '{팀:가}' WHERE id = $1`, got.List.ID)
	reader := e.as(e.ua)
	if c, info := e.callGet(reader, got.Followup.ID); c != http.StatusOK || info.Role != "followup" || info.Parent.ID != e.campID || info.Parent.Name != "" {
		t.Fatalf("follow-up side: %d %+v", c, info.Parent)
	}
	// And the other way round: a reader of the original that cannot open the follow-up sees no link.
	e.db.MustExec(`UPDATE lists SET tags = '{}' WHERE id = $1`, got.List.ID)
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id) VALUES ($1, $2)`, e.campID, e.aList)
	reader = e.as(e.ua)
	if c, info := e.callGet(reader, e.campID); c != http.StatusOK || info.Role != "none" || info.Followup != nil {
		t.Fatalf("original side: %d %+v", c, info)
	}
	if o := e.preview(reader, `{"tracking_only":true}`).Followup; o == nil || o.Existing != nil || o.Available || o.Reason != "exists_hidden" {
		t.Fatalf("preview: %+v", o)
	}
	if _, info := e.callGet(e.admin, e.campID); info.Role != "parent" {
		t.Fatalf("admin: %+v", info)
	}
}

// addParent makes a second finished campaign, sent to the same four people, for a second
// follow-up.
func (e *followupEnv) addParent() int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO campaigns (uuid, name, subject, from_email, body, content_type, messenger, status, started_at)
		VALUES (gen_random_uuid(), 'Second parent', 'Subject', 'Synthetic <sender@synthetic-sender.dev>', '<p>Hi</p>', 'html', 'email', 'finished', NOW()) RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'A')`, id, e.aList)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) SELECT $1, unnest($2::INT[])`, id, pq.Array(e.ids))
	// Alice opened it, so the condition "did not open" is available.
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, id, e.ids[0])
	return id
}


// A follow-up list that went to the trash and came back is still checked, and only that list is:
// someone the campaign also reaches through a newsletter added to it by hand is not held back.
func TestFollowupListRestoredFromTrashIsStillChecked(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	var newsletter int
	e.get(&newsletter, `INSERT INTO lists (uuid, name, type) VALUES (gen_random_uuid(), 'Independent newsletter', 'private') RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'Independent newsletter')`, got.Followup.ID, newsletter)
	eli, charlie := e.ids[3], e.ids[2]
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, eli, newsletter)
	if code, _ := e.trash(got.List.ID, e.superAdmin); code != 200 {
		t.Fatalf("trash: %d", code)
	}
	if code, _ := e.restore(got.List.ID, e.superAdmin); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	wantSame(t, "restored members", e.members(got.List.ID), []int{charlie, eli})
	// Both leave the original's list; the follow-up starts.
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = ANY($1::INT[]) AND list_id = $2`, pq.Array([]int{charlie, eli}), e.aList)
	e.send(got.Followup.ID)
	wantSame(t, "held after the check", e.held(got.Followup.ID), []int{charlie})
	wantSame(t, "the newsletter is untouched", e.members(newsletter), []int{eli})
	var gone int
	e.get(&gone, `SELECT COALESCE((recheck->>'opted_out')::INT, 0) FROM axm_campaign_followups WHERE campaign_id = $1`, got.Followup.ID)
	if gone != 1 {
		t.Fatalf("counted %d who opted out", gone)
	}
	// The banner reads the restored list again.
	if c, info := e.callGet(e.admin, got.Followup.ID); c != 200 || info.List == nil || info.List.ID != got.List.ID {
		t.Fatalf("info: %d %+v", c, info.List)
	}
}

// When the check holds everyone back, the sender finds nobody to send to and finishes the campaign
// the way it finishes any other: it does not stay scheduled, and a person added to the list later
// does not get the mail without a new schedule.
func TestFollowupWithNobodyLeftEnds(t *testing.T) {
	status := func(e *followupEnv, id int) (st struct {
		Status string
		Sent   int
		ToSend int `db:"to_send"`
	}) {
		e.get(&st, `SELECT status, sent, to_send FROM campaigns WHERE id = $1`, id)
		return
	}
	finished := func(e *followupEnv, id int) func() bool {
		return func() bool { return status(e, id).Status == "finished" }
	}
	t.Run("everyone held", func(t *testing.T) {
		e := newScanningFollowupEnv(t, true)
		got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
		for _, id := range []int{e.ids[2], e.ids[3]} {
			e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
		}
		e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, got.Followup.ID)
		waitFor(t, "the follow-up to finish", finished(e, got.Followup.ID))
		if st := status(e, got.Followup.ID); st.Sent != 0 {
			t.Fatalf("campaign: %+v", st)
		}
		// Someone added afterwards is not sent to: the campaign is over.
		e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, e.ids[1], got.List.ID)
		time.Sleep(200 * time.Millisecond)
		if st := status(e, got.Followup.ID); st.Status != "finished" || len(e.gate.sentIDs()) != 0 {
			t.Fatalf("campaign: %+v, sent to %v", st, e.gate.sentIDs())
		}
	})

	// On a double opt-in list only the confirmed are sent to, so someone waiting for confirmation
	// does not keep the campaign open.
	t.Run("only people waiting for confirmation are left", func(t *testing.T) {
		e := newScanningFollowupEnv(t, true)
		got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
		e.db.MustExec(`UPDATE lists SET optin = 'double' WHERE id = $1`, got.List.ID)
		e.db.MustExec(`UPDATE subscriber_lists SET status = 'unconfirmed' WHERE list_id = $1 AND subscriber_id = $2`, got.List.ID, e.ids[3])
		e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[2])
		e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, got.Followup.ID)
		waitFor(t, "the follow-up to finish", finished(e, got.Followup.ID))
		if len(e.gate.sentIDs()) != 0 {
			t.Fatalf("sent to %v", e.gate.sentIDs())
		}
	})

	// A campaign paused after a partial send keeps what it sent when it resumes with nobody left.
	t.Run("resumed with nobody left keeps its sent count", func(t *testing.T) {
		e := newScanningFollowupEnv(t, true)
		got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
		for _, id := range []int{e.ids[2], e.ids[3]} {
			e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
		}
		e.db.MustExec(`UPDATE campaigns SET status = 'running', sent = 1, to_send = 11 WHERE id = $1`, got.Followup.ID)
		waitFor(t, "the follow-up to finish", finished(e, got.Followup.ID))
		if st := status(e, got.Followup.ID); st.Sent != 1 || len(e.gate.sentIDs()) != 0 {
			t.Fatalf("campaign: %+v, sent to %v", st, e.gate.sentIDs())
		}
	})
}

// What the editor copies includes the campaign's own attributes (a body may read them), but
// not the time zone of a send time the copy does not have, and the parent is left as it was.
func TestFollowupCopiesAttributesAndArchiveSettings(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	var tpl1, tpl2 int
	e.get(&tpl1, `INSERT INTO templates (name, type, subject, body, is_default) VALUES ('send', 'campaign', '', '{{ template "content" . }}', false) RETURNING id`)
	e.get(&tpl2, `INSERT INTO templates (name, type, subject, body, is_default) VALUES ('archive', 'campaign', '', '{{ template "content" . }}', false) RETURNING id`)
	e.db.MustExec(`UPDATE campaigns SET attribs = '{"event_code":"autumn","axm_send_tz":"Asia/Seoul"}', template_id = $2, archive = TRUE,
		archive_slug = 'parent-slug', archive_template_id = $3, archive_meta = '{"name":"Archive person"}' WHERE id = $1`, e.campID, tpl1, tpl2)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)

	var c struct {
		Attribs, ArchiveMeta string
		Archive              bool
		Slug                 *string
		Template, ArchiveTpl *int
	}
	row := e.db.QueryRowx(`SELECT attribs::TEXT, archive_meta::TEXT, archive, archive_slug, template_id, archive_template_id FROM campaigns WHERE id = $1`, got.Followup.ID)
	if err := row.Scan(&c.Attribs, &c.ArchiveMeta, &c.Archive, &c.Slug, &c.Template, &c.ArchiveTpl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Attribs, `"event_code": "autumn"`) || strings.Contains(c.Attribs, "axm_send_tz") {
		t.Fatalf("attributes: %s", c.Attribs)
	}
	if !c.Archive || c.Slug != nil || c.Template == nil || *c.Template != tpl1 || c.ArchiveTpl == nil || *c.ArchiveTpl != tpl2 ||
		!strings.Contains(c.ArchiveMeta, "Archive person") {
		t.Fatalf("archive settings: %+v", c)
	}
	var parent string
	e.get(&parent, `SELECT attribs::TEXT FROM campaigns WHERE id = $1`, e.campID)
	if !strings.Contains(parent, "axm_send_tz") {
		t.Fatalf("the parent lost its time zone: %s", parent)
	}
}

// An opt-in campaign has no follow-up draft: the dialog is not offered one and a request is refused.
func TestFollowupOnlyForRegularCampaigns(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	e.db.MustExec(`UPDATE campaigns SET type = 'optin' WHERE id = $1`, e.campID)
	if o := e.preview(e.admin, `{"open":"no"}`).Followup; o == nil || o.Available || o.Reason != "not_regular" {
		t.Fatalf("offer: %+v", o)
	}
	before := e.count(`SELECT COUNT(*) FROM campaigns`)
	if code, _ := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true,"followup":true}`); code != http.StatusBadRequest {
		t.Fatalf("code %d", code)
	}
	if e.count(`SELECT COUNT(*) FROM campaigns`) != before {
		t.Fatal("a refused request left a campaign")
	}
}

// The dialog shows what the existing follow-up was made with, and the reading of a link names
// when the original was sent and when the follow-up is scheduled.
func TestFollowupExistingCarriesConditionAndInfoCarriesDates(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	o := e.preview(e.admin, `{"open":"yes","click":"yes"}`).Followup
	if o == nil || o.Existing == nil || o.Existing.ID != got.Followup.ID || o.Existing.Count != 2 ||
		!strings.Contains(string(o.Existing.Condition), `"open":"no"`) {
		t.Fatalf("existing: %+v", o)
	}
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '4 days' WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() + INTERVAL '1 day' WHERE id = $1`, got.Followup.ID)
	e.db.MustExec(`UPDATE campaigns SET send_at = NOW() - INTERVAL '5 days' WHERE id = $1`, e.campID)
	_, info := e.callGet(e.admin, got.Followup.ID)
	if info.FollowupSendAt == nil || info.ParentSendAt == nil || info.ParentLastSentAt == nil || info.DaysSince == nil || *info.DaysSince != 4 {
		t.Fatalf("dates: %+v", info)
	}
}

// Someone who left the follow-up list on purpose keeps that record: the check does not delete it
// (the list may be used again) and does not count that person as one who will leave.
func TestFollowupKeepsExplicitUnsubscribes(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	charlie, daniel := e.ids[2], e.ids[3]
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE list_id = $1 AND subscriber_id = ANY($2::INT[])`, got.List.ID, pq.Array([]int{charlie, daniel}))
	// Both open the original, but only Charlie also had a reason to be judged: neither is on the list any more.
	for _, id := range []int{charlie, daniel} {
		e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
	}
	if _, info := e.callGet(e.admin, got.Followup.ID); info.Pending == nil || info.Pending.Reacted != 0 || info.List.Count != 0 {
		t.Fatalf("pending: %+v list %+v", info.Pending, info.List)
	}
	// Nobody is left on the list: the follow-up is ended at its send time without sending.
	camp := got.Followup.ID
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	if subs, err := e.store.NextSubscribers(camp, 0, 100); !errors.Is(err, manager.ErrCampaignNotRunning) || len(subs) != 0 {
		t.Fatalf("read: %d %v", len(subs), err)
	}
	var st string
	e.get(&st, `SELECT status FROM campaigns WHERE id = $1`, camp)
	if st != "finished" {
		t.Fatalf("the campaign is %s", st)
	}
	var kept int
	e.get(&kept, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id = $1 AND status = 'unsubscribed'`, got.List.ID)
	if kept != 2 {
		t.Fatalf("%d unsubscribe records kept, want 2", kept)
	}
}





// Review 9 (2): everyone on the follow-up list unsubscribed from it by the time it is due. The
// campaign ends with nothing sent and someone who subscribes again later does not get it.
func TestFollowupWithEmptyListEnds(t *testing.T) {
	e := newScanningFollowupEnv(t, true)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE list_id = $1`, got.List.ID)
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, got.Followup.ID)
	waitFor(t, "the follow-up to finish", func() bool {
		var st string
		e.get(&st, `SELECT status FROM campaigns WHERE id = $1`, got.Followup.ID)
		return st == "finished"
	})
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'confirmed' WHERE list_id = $1 AND subscriber_id = $2`, got.List.ID, e.ids[2])
	time.Sleep(300 * time.Millisecond)
	if len(e.gate.sentIDs()) != 0 {
		t.Fatalf("sent to %v", e.gate.sentIDs())
	}
}

// Review 9 (3): the follow-up list was replaced by another list. The check holds nobody back and
// nothing is reported as left out.
func TestFollowupReplacedListIsNotChecked(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[2])
	var other int
	e.get(&other, `INSERT INTO lists (uuid, name, type) VALUES (gen_random_uuid(), 'Replacement', 'private') RETURNING id`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, e.ids[1], other)
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, camp)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'Replacement')`, camp, other)
	if c, info := e.callGet(e.admin, camp); c != 200 || !info.ListsChanged || info.Pending != nil {
		t.Fatalf("info: %d changed %v pending %+v", c, info.ListsChanged, info.Pending)
	}
	e.send(camp)
	wantSame(t, "held", e.held(camp), nil)
	if _, info := e.callGet(e.admin, camp); info.Recheck == nil || info.Recheck.Reacted != 0 || info.Recheck.OptedOut != 0 {
		t.Fatalf("recorded: %+v", info.Recheck)
	}
}

// Review 9 (4): after everyone on the follow-up reacted, asking again still answers with the
// existing follow-up (409), not "nobody matches".
func TestFollowupRetryAfterEveryoneReactedPointsAtTheFollowup(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	for _, id := range []int{e.ids[2], e.ids[3]} {
		e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
	}
	code, data := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true,"followup":true}`)
	if code != http.StatusConflict {
		t.Fatalf("retry: %d %s", code, data)
	}
	// The answer names the existing follow-up.
	_, err := e.a.core.MakeReactionList(context.Background(), e.campID, models.ReactionSettings{Open: "no", Click: "any"},
		core.ReactionListOptions{Scope: core.ReactionScope{AllSubscribers: true, IndividualTracking: true}, Followup: &core.FollowupDraft{},
			ResolveTeams: func([]string) ([]string, error) { return nil, nil }})
	if err == nil || !strings.Contains(err.Error(), "#"+fmt.Sprint(got.Followup.ID)) {
		t.Fatalf("core answer: %v", err)
	}
	// Without the follow-up the answer is the old one.
	if code, _ := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true}`); code != http.StatusBadRequest {
		t.Fatalf("list only: %d", code)
	}
}

// twoFollowups makes follow-ups of two parents; the first has the lower id, so it is checked first.
func (e *followupEnv) twoFollowups() (a, b models.ReactionResult, otherParent int) {
	e.t.Helper()
	a = e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	otherParent = e.addParent()
	code, data := e.sendRequest(true, e.admin, otherParent, `{"open":"no","no_team":true,"followup":true}`)
	if code != http.StatusOK || json.Unmarshal(data, &b) != nil || b.Followup == nil {
		e.t.Fatalf("second follow-up: %d %s", code, data)
	}
	if a.Followup.ID > b.Followup.ID {
		e.t.Fatal("the first follow-up must have the lower id")
	}
	return a, b, otherParent
}

// saveCampaign saves a campaign the way the editor does, with new lists and a send time.
func (e *followupEnv) saveCampaign(camp int, lists []int, at time.Time) {
	e.t.Helper()
	cm, err := e.a.core.GetCampaign(camp, "", "")
	if err != nil {
		e.t.Fatal(err)
	}
	cm.SendAt.Time, cm.SendAt.Valid = at, true
	if _, err := e.a.core.UpdateCampaign(camp, cm, lists, nil); err != nil {
		e.t.Fatal(err)
	}
}

func (e *followupEnv) lockWaiting(like string) func() bool {
	return func() bool {
		return e.count(`SELECT COUNT(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE $1`, like) > 0
	}
}



// Review 11: the follow-up is started, and the people open the original while it is paused and
// resumed. They are judged when their batch is read, after that, so they are left out.
func TestFollowupPausedAndResumedBeforeTheFirstReadIsCheckedAgain(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	people := []int{e.ids[2], e.ids[3]}
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.core.UpdateCampaignStatus(camp, models.CampaignStatusPaused, "", "", false); err != nil {
		t.Fatal(err)
	}
	for _, id := range people {
		e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at = NULL WHERE id = $1`, camp)
	if _, err := e.a.core.UpdateCampaignStatus(camp, models.CampaignStatusRunning, "", "", false); err != nil {
		t.Fatal(err)
	}
	if subs, err := e.store.NextSubscribers(camp, 0, 100); err != nil || len(subs) != 0 {
		t.Fatalf("read after the resume: %d %v", len(subs), err)
	}
	wantSame(t, "left out", e.held(camp), people)
}


// Review 12 (1): the follow-up list went to the trash and came back before the send. The people of
// the restored list who opened the original are left out when the batch is read.
func TestFollowupListRestoredDuringTheCheckIsChecked(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	people := []int{e.ids[2], e.ids[3]}
	for _, id := range people {
		e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, id)
	}
	if code, _ := e.trash(got.List.ID, e.superAdmin); code != 200 {
		t.Fatalf("trash: %d", code)
	}
	if code, _ := e.restore(got.List.ID, e.superAdmin); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	wantSame(t, "sent to", e.send(camp), nil)
	wantSame(t, "left out", e.held(camp), people)
}

// Review 12 (2): every check before a send is in the audit log, with who it left out by reason
// and no person.
func TestFollowupCheckIsAudited(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	// A parent whose name holds an address: the audit row must not keep it.
	e.db.MustExec(`UPDATE campaigns SET name = 'November invite eli-followup@example.invalid' WHERE id = $1`, e.campID)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true,"name":"Followup list"}`)
	if !strings.Contains(got.Followup.Name, "eli-followup@example.invalid") {
		t.Fatalf("the draft has its default name: %s", got.Followup.Name)
	}
	camp := got.Followup.ID
	e.db.MustExec(`UPDATE campaigns SET name = 'Follow-up for eli-followup@example.invalid' WHERE id = $1`, camp)
	run := func() {
		t.Helper()
		e.send(camp)
		e.db.MustExec(`UPDATE campaigns SET status = 'paused' WHERE id = $1`, camp)
	}
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[2])
	run()
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[3])
	run()
	var rows []struct {
		UserType string `db:"user_type"`
		Count    int    `db:"count"`
		Detail   string `db:"detail"`
		Row      string `db:"row"`
	}
	if err := e.db.Select(&rows, `SELECT user_type, count, detail::TEXT AS detail, row_to_json(l)::TEXT AS row FROM axm_audit_log l
		WHERE action = $1 AND target_type = 'campaign' AND target_id = $2 ORDER BY id`, auditCampaignFollowupCk, camp); err != nil {
		t.Fatal(err)
	}
	// The row of making the draft too: its name is the default, "<parent> 후속".
	var made string
	e.get(&made, `SELECT row_to_json(l)::TEXT FROM axm_audit_log l WHERE action = $1 AND target_id = $2`, auditCampaignFollowup, camp)
	if strings.Contains(made, "@") || strings.Contains(made, "eli-followup") || !strings.Contains(made, "[email]") {
		t.Fatalf("an address in the row of making the draft: %s", made)
	}
	if len(rows) != 2 || rows[0].Count != 1 || rows[1].Count != 2 || rows[0].UserType != models.AuditUserTypeSystem {
		t.Fatalf("audit rows: %+v", rows)
	}
	var d struct {
		Parent struct {
			ID int `json:"id"`
		} `json:"parent"`
		Reacted int `json:"reacted"`
		Check   int `json:"check"`
	}
	if err := json.Unmarshal([]byte(rows[1].Detail), &d); err != nil || d.Parent.ID != e.campID || d.Reacted != 2 || d.Check != 2 {
		t.Fatalf("detail %s: %v", rows[1].Detail, err)
	}
	for _, r := range rows {
		if strings.Contains(r.Row, "@") || strings.Contains(r.Row, "eli-followup") || !strings.Contains(r.Row, "[email]") {
			t.Fatalf("an address in the audit row: %s", r.Row)
		}
	}
}

// Review 15/16 (1): the original's send records expired while the follow-up waited, and an admin
// took a person off the follow-up list and put them back. Who the draft was made for is still
// judged, and an open still counts.
func TestFollowupJudgedAfterTheSendRecordsExpired(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp, charlie := got.Followup.ID, e.ids[2]
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	// Off the list and back on: a new row, made after the draft.
	e.db.MustExec(`DELETE FROM subscriber_lists WHERE subscriber_id = $1 AND list_id = $2`, charlie, got.List.ID)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status, created_at) VALUES ($1, $2, 'confirmed', NOW() + INTERVAL '1 minute')`, charlie, got.List.ID)
	if _, info := e.callGet(e.admin, camp); info.Pending == nil || info.Pending.Reacted != 1 || info.Pending.Unknown != 0 {
		t.Fatalf("pending: %+v", info.Pending)
	}
	e.send(camp)
	wantSame(t, "held", e.held(camp), []int{charlie})
	// Someone added by hand afterwards, who never got the original, is still not judged.
	ian := e.sendsEnv.ids(`SELECT id FROM subscribers WHERE name = 'Ian'`)[0]
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, ian, got.List.ID)
	e.db.MustExec(`UPDATE campaigns SET status = 'paused' WHERE id = $1`, camp)
	e.send(camp)
	wantSame(t, "held", e.held(camp), []int{charlie})
}

// Review 15/16 (2): the send records expired and an admin cleared the statistics after Charlie
// opened the original; then someone else opens it. Charlie's open was kept for the follow-up
// when it happened, so he stays held back, and Daniel, who never opened it, is sent to.
func TestFollowupJudgedAfterTheStatisticsWereCleared(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp, charlie := got.Followup.ID, e.ids[2]
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, e.campID)
	run := func() {
		t.Helper()
		e.send(camp)
		e.db.MustExec(`UPDATE campaigns SET status = 'draft' WHERE id = $1`, camp)
	}
	if _, info := e.callGet(e.admin, camp); info.Pending == nil || info.Pending.Reacted != 1 || info.Pending.Unknown != 0 {
		t.Fatalf("pending: %+v", info.Pending)
	}
	run()
	wantSame(t, "held by the kept open", e.held(camp), []int{charlie})
	// Alice, not on the follow-up, opens the original: nothing changes for the follow-up's people.
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[0])
	run()
	wantSame(t, "held after another open", e.held(camp), []int{charlie})
}

// Review 16 (3): tracking is switched off (as a whole, or per subscriber). Opens and clicks are not
// recorded then, so nobody's reaction can be seen: everyone is held back, also after tracking is
// on again (the app notes the gap when it starts with tracking off).
func TestFollowupHeldWhileTrackingIsOff(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp, charlie, daniel := got.Followup.ID, e.ids[2], e.ids[3]
	run := func() {
		t.Helper()
		e.send(camp)
		e.db.MustExec(`UPDATE campaigns SET status = 'draft' WHERE id = $1`, camp)
	}
	run()
	wantSame(t, "held with tracking on", e.held(camp), nil)
	e.a.cfg.Privacy.DisableTracking = true
	if _, info := e.callGet(e.admin, camp); info.Pending == nil || info.Pending.Unknown != 2 {
		t.Fatalf("pending: %+v", info.Pending)
	}
	run()
	wantSame(t, "held, tracking off", e.held(camp), []int{charlie, daniel})
	// The app starts with tracking off and notes it; later it starts with tracking on again.
	if err := e.a.core.NoteTracking(false); err != nil {
		t.Fatal(err)
	}
	e.a.cfg.Privacy.DisableTracking = false
	if err := e.a.core.NoteTracking(true); err != nil {
		t.Fatal(err)
	}
	run()
	wantSame(t, "held after a time without tracking", e.held(camp), []int{charlie, daniel})
	if _, info := e.callGet(e.admin, camp); info.Recheck == nil || info.Recheck.Unknown != 2 {
		t.Fatalf("recorded: %+v", info.Recheck)
	}
}

// Review 15 (3): the pre-send confirmation of a follow-up that the check would leave with nobody
// stops it, as it stops any campaign that reaches nobody.
func TestFollowupPreflightLeavesOutWhoWouldBeHeld(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	path := fmt.Sprintf("/api/campaigns/%d/preflight", camp)
	read := func() (recipients int, zero string) {
		t.Helper()
		code, data := e.call(e.a.GetCampaignPreflight, e.admin, http.MethodGet, path, fmt.Sprint(camp), "")
		if code != http.StatusOK {
			t.Fatalf("preflight: %d %s", code, data)
		}
		var out struct {
			Recipients int                     `json:"recipients"`
			Checks     []models.PreflightCheck `json:"checks"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		for _, c := range out.Checks {
			if c.Key == "recipients.zero" {
				zero = c.Level
			}
		}
		return out.Recipients, zero
	}
	if n, zero := read(); n != 2 || zero != "" {
		t.Fatalf("before: %d %q", n, zero)
	}
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[2])
	if n, zero := read(); n != 1 || zero != "" {
		t.Fatalf("one opened: %d %q", n, zero)
	}
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[3])
	if n, zero := read(); n != 0 || zero != models.PreflightBlock {
		t.Fatalf("both opened: %d %q", n, zero)
	}
}


// Review 17/18 (2): the draft is made while tracking is switched off; tracking comes back on (the
// app starts with it on). The reactions of the time without tracking cannot be seen, so everyone
// is held back, also in a draft made again for the same original after tracking is on.
func TestFollowupMadeWhileTrackingIsOffHoldsEveryone(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	e.a.cfg.Privacy.DisableTracking = true
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.a.cfg.Privacy.DisableTracking = false
	if err := e.a.core.NoteTracking(true); err != nil {
		t.Fatal(err)
	}
	run := func(camp int) {
		t.Helper()
		e.send(camp)
		e.db.MustExec(`UPDATE campaigns SET status = 'draft' WHERE id = $1`, camp)
	}
	run(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{e.ids[2], e.ids[3]})
	// The draft is deleted and made again with tracking on: the original went out before the time
	// without tracking ended, so it is the same.
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, got.Followup.ID)
	again := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	run(again.Followup.ID)
	wantSame(t, "held in the draft made again", e.held(again.Followup.ID), []int{e.ids[2], e.ids[3]})
}

// Review 17 (3): Charlie's open is only in the original's send record when the draft is made (the
// view rows were cleared); then that record expires. The open was kept for the follow-up when it
// was made, so he stays held back.
func TestFollowupOpenOnlyInTheSendRecordIsKept(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	charlie := e.ids[2]
	e.db.MustExec(`UPDATE axm_campaign_sends SET opened_at = NOW() WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, charlie)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	wantSame(t, "made for", e.members(got.List.ID), []int{charlie, e.ids[3]})
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	e.send(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{charlie})
}

// Review 17/18 (1): on an "opened and did not click" follow-up, Daniel, who got the original but
// never opened it, is added by hand. He does not meet "opened" and is held back, also after the
// original's send records expired.
func TestFollowupAddedByHandMustMeetADidCondition(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"yes","click":"no","no_team":true,"followup":true}`)
	wantSame(t, "made for", e.members(got.List.ID), []int{e.ids[0]})
	daniel := e.ids[3]
	if n := e.count(`SELECT (SELECT COUNT(*) FROM campaign_views WHERE campaign_id = $1 AND subscriber_id = $2) + (SELECT COUNT(*) FROM link_clicks WHERE campaign_id = $1 AND subscriber_id = $2)`, e.campID, daniel); n != 0 {
		t.Fatalf("Daniel reacted to the original (%d)", n)
	}
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	e.send(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{daniel})
}

// Review 18 (2): Charlie opens the original while the draft is being made (after its snapshot,
// before its commit); then the statistics are cleared and the send records expire. The open is
// kept for the follow-up once the draft is committed, so he is held back.
func TestFollowupOpenWhileTheDraftIsMadeIsKept(t *testing.T) {
	e := newFollowupEnv(t)
	charlie := e.ids[2]
	lock, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec(`LOCK TABLE axm_campaign_followup_members IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		code int
		data json.RawMessage
	}
	done := make(chan result, 1)
	go func() {
		code, data := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true,"followup":true}`)
		done <- result{code, data}
	}()
	waitFor(t, "the draft to wait", e.lockWaiting("%INSERT INTO axm_campaign_followup_members%"))
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie)
	e.db.MustExec(`UPDATE axm_campaign_sends SET opened_at = NOW() WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, charlie)
	lock.Rollback()
	r := <-done
	var got models.ReactionResult
	if r.code != http.StatusOK || json.Unmarshal(r.data, &got) != nil || got.Followup == nil {
		t.Fatalf("made: %d %s", r.code, r.data)
	}
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	e.send(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{charlie})
}

// Review 19 (1): Charlie's open is being recorded (its trigger ran) while the draft is made, and is
// committed only after the draft's own copy would have run. The draft waits for it, so the open is
// kept and he stays held back once the statistics are cleared and the send records expire.
func TestFollowupOpenCommittedAfterTheDraftIsKept(t *testing.T) {
	e := newFollowupEnv(t)
	charlie := e.ids[2]
	view, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	if _, err := view.Exec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie); err != nil {
		t.Fatal(err)
	}
	type result struct {
		code int
		data json.RawMessage
	}
	done := make(chan result, 1)
	go func() {
		code, data := e.sendRequest(true, e.admin, e.campID, `{"open":"no","no_team":true,"followup":true}`)
		done <- result{code, data}
	}()
	waitFor(t, "the draft to wait for the open", e.lockWaiting("%pg_advisory_xact_lock(4747%"))
	if err := view.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	var got models.ReactionResult
	if r.code != http.StatusOK || json.Unmarshal(r.data, &got) != nil || got.Followup == nil {
		t.Fatalf("made: %d %s", r.code, r.data)
	}
	wantSame(t, "made for", e.members(got.List.ID), []int{charlie, e.ids[3]})
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	e.send(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{charlie})
}

// Review 19 (2): Daniel's send record of the original is written after the draft was made. He
// is added to the follow-up list by hand and opens the original: he is held back.
func TestFollowupSendRecordWrittenAfterTheDraftCounts(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	daniel := e.ids[3]
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, daniel)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	wantSame(t, "made for", e.members(got.List.ID), []int{e.ids[2]})
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	if _, info := e.callGet(e.admin, got.Followup.ID); info.Pending == nil || info.Pending.Reacted != 1 {
		t.Fatalf("pending: %+v", info.Pending)
	}
	e.send(got.Followup.ID)
	wantSame(t, "held", e.held(got.Followup.ID), []int{daniel})
}

// Review 20 (1): Charlie's open reaches the original's send record only after the draft was made
// (the view row was cleared before that). Writing the open time keeps the reaction for the
// follow-up, so he is left out at the send also once that record has expired.
func TestFollowupOpenWrittenLateOnTheSendRecordIsKept(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	charlie := e.ids[2]
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`UPDATE axm_campaign_sends SET opened_at = NOW() WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, charlie)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	wantSame(t, "sent to", e.send(got.Followup.ID), []int{e.ids[3]})
	wantSame(t, "left out", e.held(got.Followup.ID), []int{charlie})
}

// Review 20 (2): Daniel was added by hand and opened the original; his send record of the original
// is written after the draft was made. At the send he is a recipient of the original and is left out.
func TestFollowupSendRecordWrittenAfterACheckHoldsAtOnce(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	daniel := e.ids[3]
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, daniel)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	wantSame(t, "sent to", e.send(got.Followup.ID), []int{e.ids[2]})
	wantSame(t, "left out", e.held(got.Followup.ID), []int{daniel})
}

// Review 21 (1): Daniel opened the original before the draft was made, while his send record was
// not written yet. He is added by hand and his send record comes later: his open counts at the
// send, also after the statistics were cleared and the send records expired.
func TestFollowupOpenBeforeTheDraftWithALateSendRecordHolds(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	daniel := e.ids[3]
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, daniel)
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, e.campID)
	wantSame(t, "sent to", e.send(got.Followup.ID), []int{e.ids[2]})
	wantSame(t, "left out", e.held(got.Followup.ID), []int{daniel})
}

// Review 22: Daniel, added by hand, left the original's list before the follow-up is due, and his
// send record of the original is written after the draft was made. He is left out at the send for
// having opted out.
func TestFollowupOptOutWithALateSendRecordHolds(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	daniel := e.ids[3]
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, daniel)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, daniel, e.aList)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	wantSame(t, "sent to", e.send(got.Followup.ID), []int{e.ids[2]})
	var reason int
	e.get(&reason, `SELECT reason FROM axm_campaign_followup_holds WHERE campaign_id = $1 AND subscriber_id = $2`, got.Followup.ID, daniel)
	if reason != 2 {
		t.Fatalf("left out for %d, want 2 (opted out)", reason)
	}
}

// Review 23: Daniel, added by hand, got the original; the transaction writing his send record has
// judged him (still subscribed) but is not committed yet when he leaves the original's list and
// the check runs. When the sender reads his batch it waits for that record, judges him again and
// leaves him out.
func TestFollowupBatchIsJudgedAfterPendingSendRecords(t *testing.T) {
	e := newFollowupEnv(t)
	daniel := e.ids[3]
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, daniel)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, daniel, got.List.ID)
	send, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer send.Rollback()
	if _, err := send.Exec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, daniel, e.aList)
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		subs []models.Subscriber
		err  error
	}
	done := make(chan result, 1)
	go func() {
		subs, err := e.store.NextSubscribers(camp, 0, 100)
		done <- result{subs, err}
	}()
	waitFor(t, "the batch to wait for the send record", e.lockWaiting("%pg_advisory_xact_lock(4747%"))
	if err := send.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, s := range r.subs {
		if s.ID == daniel {
			t.Fatal("the batch has Daniel")
		}
	}
	wantSame(t, "held", e.held(camp), []int{daniel})
}

// Review 24 (1): Charlie's open of the original is being recorded when the sender reads the
// follow-up's first batch; its trigger holds him, committed only after the batch was read. The
// sender sees that hold before using the batch and reads it again without him.
func TestFollowupHoldCommittedAfterTheBatchReadIsSeen(t *testing.T) {
	e := newFollowupEnv(t)
	charlie := e.ids[2]
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	view, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	if _, err := view.Exec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie); err != nil {
		t.Fatal(err)
	}
	type result struct {
		subs []models.Subscriber
		err  error
	}
	done := make(chan result, 1)
	go func() {
		subs, err := e.store.NextSubscribers(camp, 0, 100)
		done <- result{subs, err}
	}()
	waitFor(t, "the batch to wait for the open", e.lockWaiting("%pg_advisory_xact_lock(4747%"))
	if err := view.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	ids := []int{}
	for _, s := range r.subs {
		ids = append(ids, s.ID)
	}
	wantSame(t, "the batch", ids, []int{e.ids[3]})
}


// Review 25: the follow-up also sends to a dynamic list, and Charlie, who opened the original,
// comes into it while the follow-up sends. The read ends (it used to read the same batch again
// without end); reached through a list added by hand, he is not judged.
func TestFollowupHeldPersonInADynamicListDoesNotStopTheSender(t *testing.T) {
	e := newFollowupEnv(t)
	charlie, daniel := e.ids[2], e.ids[3]
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	var base, dyn int
	e.get(&base, `INSERT INTO lists (uuid, name, type, optin) VALUES (gen_random_uuid(), 'Base', 'private', 'single') RETURNING id`)
	e.get(&dyn, `INSERT INTO lists (uuid, name, type, optin, axm_filter) VALUES (gen_random_uuid(), 'Dynamic', 'private', 'single', $1::JSONB) RETURNING id`,
		fmt.Sprintf(`{"version":2,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[],"exclude":{"lists":[]}}`, base))
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'Dynamic')`, camp, dyn)
	e.db.MustExec(`UPDATE campaigns SET status = 'scheduled', send_at = NOW() - INTERVAL '1 second' WHERE id = $1`, camp)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, charlie)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, charlie, base)
	type result struct {
		subs []models.Subscriber
		err  error
	}
	done := make(chan result, 1)
	go func() {
		subs, err := e.store.NextSubscribers(camp, 0, 100)
		done <- result{subs, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		ids := []int{}
		for _, s := range r.subs {
			ids = append(ids, s.ID)
		}
		// Reached through the dynamic list added by hand, Charlie is not judged (and not left out).
		wantSame(t, "the batch", ids, []int{charlie, daniel})
	case <-time.After(10 * time.Second):
		t.Fatal("the sender keeps reading the same batch")
	}
	// A batch of only held people is read past, not taken for the end.
	if subs, err := e.store.NextSubscribers(camp, daniel, 100); err != nil || len(subs) != 0 {
		t.Fatalf("after Daniel: %d %v", len(subs), err)
	}
}
