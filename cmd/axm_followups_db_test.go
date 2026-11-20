package main

import (
	"encoding/json"
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
	e.db.MustExec(`UPDATE campaigns SET from_email = 'Synthetic <sender@example.invalid>' WHERE id = $1`, e.campID)
	e.a.cfg.FromEmail = "Synthetic <sender@example.invalid>"
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
		List      int
		Condition string
		Counts    string
	}
	row = e.db.QueryRowx(`SELECT parent_campaign_id, list_id, condition::TEXT, counts::TEXT FROM axm_campaign_followups WHERE campaign_id = $1`, got.Followup.ID)
	if err := row.Scan(&link.Parent, &link.List, &link.Condition, &link.Counts); err != nil {
		t.Fatal(err)
	}
	if link.Parent != parent || link.List != got.List.ID || !strings.Contains(link.Condition, `"open": "no"`) || !strings.Contains(link.Counts, `"eligible": 2`) ||
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

func TestFollowupRecheckRemovesWhoReactedOrOptedOut(t *testing.T) {
	e := newFollowupEnv(t)
	got, manual := e.followupWithManualMember(`{"open":"no","no_team":true,"followup":true}`)
	camp, list := got.Followup.ID, got.List.ID
	charlie, daniel := e.ids[2], e.ids[3]
	wantSame(t, "before", e.members(list), []int{charlie, daniel, manual})

	// Something changed since the list was made.
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, daniel)
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, charlie, e.aList)

	run := func() {
		t.Helper()
		if _, err := e.store.NextCampaigns(nil); err != nil {
			t.Fatal(err)
		}
	}
	state := func(status string, sendAt string) {
		e.db.MustExec(fmt.Sprintf(`UPDATE campaigns SET status = $2::campaign_status, send_at = %s WHERE id = $1`, sendAt), camp, status)
	}

	// Not before the campaign is about to start: a draft, a schedule that is not due, a
	// paused campaign, and a running one the sender already holds.
	state("draft", "NULL")
	run()
	state("scheduled", "NOW() + INTERVAL '1 hour'")
	run()
	state("paused", "NULL")
	run()
	wantSame(t, "untouched", e.members(list), []int{charlie, daniel, manual})
	state("running", "NULL")
	if _, err := e.store.NextCampaigns([]int64{int64(camp)}); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "held", e.members(list), []int{charlie, daniel, manual})
	state("draft", "NULL")
	if code, info := e.callGet(e.admin, camp); code != 200 || info.Pending == nil || info.Pending.Reacted != 1 || info.Pending.OptedOut != 1 || info.Pending.Blocked != 0 {
		t.Fatalf("pending: %d %+v", code, info.Pending)
	}
	wantSame(t, "reading does not remove", e.members(list), []int{charlie, daniel, manual})

	// A schedule that is due runs it, once.
	state("scheduled", "NOW() - INTERVAL '1 second'")
	run()
	wantSame(t, "after the due schedule", e.members(list), []int{manual})
	code, info := e.callGet(e.admin, camp)
	if code != 200 || info.Recheck == nil || info.Recheck.Reacted != 1 || info.Recheck.OptedOut != 1 || info.Recheck.Blocked != 0 || info.Recheck.Runs != 1 || info.RecheckedAt == nil {
		t.Fatalf("recorded: %d %+v", code, info.Recheck)
	}
	// Two more people leave before a resume: it runs again, and counts add up.
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = $1`, charlie)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, charlie, list)
	state("running", "NULL")
	run()
	wantSame(t, "after the resume", e.members(list), []int{manual})
	if _, info := e.callGet(e.admin, camp); info.Recheck.Blocked != 1 || info.Recheck.Runs != 2 {
		t.Fatalf("second run: %+v", info.Recheck)
	}
}

func TestFollowupConditionsStayTrue(t *testing.T) {
	e := newFollowupEnv(t)
	// Opened and did not click: Alice. Brenda clicked and Charlie never opened.
	got := e.makeFollowup(e.admin, `{"open":"yes","click":"no","no_team":true,"followup":true}`)
	wantSame(t, "members", e.members(got.List.ID), []int{e.ids[0]})
	e.db.MustExec(`UPDATE campaigns SET status = 'running' WHERE id = $1`, got.Followup.ID)
	if _, err := e.store.NextCampaigns(nil); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "still matching", e.members(got.List.ID), []int{e.ids[0]})
	// She clicks: the condition no longer holds.
	var link int
	e.get(&link, `SELECT id FROM links LIMIT 1`)
	e.db.MustExec(`INSERT INTO link_clicks (campaign_id, link_id, subscriber_id) VALUES ($1, $2, $3)`, e.campID, link, e.ids[0])
	e.db.MustExec(`UPDATE campaigns SET status = 'paused' WHERE id = $1`, got.Followup.ID)
	e.db.MustExec(`UPDATE campaigns SET status = 'running' WHERE id = $1`, got.Followup.ID)
	if _, err := e.store.NextCampaigns(nil); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "after the click", e.members(got.List.ID), []int{})
}

func TestFollowupCheckFailureStartsNothing(t *testing.T) {
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	e.db.MustExec(`UPDATE campaigns SET status = 'running' WHERE id = $1`, got.Followup.ID)
	e.db.MustExec(`UPDATE axm_campaign_followups SET condition = '{"open":"bogus"}' WHERE campaign_id = $1`, got.Followup.ID)
	e.db.MustExec(`UPDATE campaigns SET status = 'running' WHERE id = $1`, e.addRunning())
	camps, err := e.store.NextCampaigns(nil)
	if err == nil || len(camps) != 0 {
		t.Fatalf("a failed check let campaigns through: %v %d", err, len(camps))
	}
}

func (e *followupEnv) addRunning() int {
	id, _ := e.addCampaign("running")
	return id
}

// The whole way, with the real sender: the follow-up goes to the people left on the list after
// the check, and not to the one who opened the campaign in between.
func TestFollowupSendsToWhoIsLeft(t *testing.T) {
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
	e := newFollowupEnv(t)
	root := repoRoot(t)
	schema, _ := os.ReadFile(filepath.Join(root, "schema.sql"))
	mig, _ := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.15.go"))
	const start, end = "-- AXM campaign follow-ups (LST-47). Keep this block identical to V6_2_1_axm_15.", "-- End of AXM campaign follow-ups."
	_, tail, ok := strings.Cut(string(schema), start)
	block, _, endOK := strings.Cut(tail, end)
	if !ok || !endOK || !strings.Contains(string(mig), "`"+start+block+end+"`") {
		t.Fatal("schema and migration SQL of axm_campaign_followups differ")
	}
	e.db.MustExec(`DROP TABLE axm_campaign_followups`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_15(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if e.count(`SELECT COUNT(*) FROM axm_campaign_followups`) != 0 {
		t.Fatal("rows after the migration")
	}
	if e.count(`SELECT COUNT(*) FROM pg_depend d JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S' WHERE d.refobjid = 'axm_campaign_followups'::regclass`) != 0 {
		t.Fatal("the table owns a sequence")
	}
	var last string
	last = migList[len(migList)-1].version
	if last != "v6.2.1-axm.15" {
		t.Fatal(last)
	}
}

// A reader who cannot open the other campaign of a link learns nothing about it.
func TestFollowupHidesWhatTheReaderCannotOpen(t *testing.T) {
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
