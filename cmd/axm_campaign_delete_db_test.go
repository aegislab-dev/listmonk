package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/core"
	"github.com/labstack/echo/v4"
)

// LST-55: a campaign that is sending or has sent mail is not deleted (409). Deleting it
// drops its send records, which "exclude recent sends" and the reaction counts read.

// campaignWith adds a campaign of the given status that has sent n of its 100 messages, and
// started the given number of months ago (0 = just now, -1 = never started).
func (e *teamsEnv) campaignWith(status string, sent, startedMonthsAgo int) int {
	e.t.Helper()
	id, _ := e.addCampaign(status)
	e.db.MustExec(`UPDATE campaigns SET sent = $2, to_send = 100,
		started_at = CASE WHEN $3 < 0 THEN NULL ELSE NOW() - MAKE_INTERVAL(months => $3) END,
		created_at = NOW() - MAKE_INTERVAL(months => GREATEST($3, 0)) WHERE id = $1`, id, sent, startedMonthsAgo)
	return id
}

// withRecords gives the campaign a send record, as a sent mail does.
func (e *teamsEnv) withRecords(campaign int) {
	e.t.Helper()
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, campaign, e.addSub("enabled", "confirmed"))
}

func (e *teamsEnv) campaignExists(id int) bool {
	e.t.Helper()
	n := 0
	e.get(&n, `SELECT COUNT(*) FROM campaigns WHERE id = $1`, id)
	return n == 1
}

func (e *teamsEnv) sendRecords(id int) int {
	e.t.Helper()
	n := 0
	e.get(&n, `SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id = $1`, id)
	return n
}

func (e *teamsEnv) deleteOne(id int) int {
	e.t.Helper()
	code, _ := e.call(e.a.DeleteCampaign, e.as(e.admin), "DELETE", "/x", itoa(id), "")
	return code
}

func (e *teamsEnv) deleteMany(ids ...int) int {
	e.t.Helper()
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = fmt.Sprintf("id=%d", id)
	}
	code, _ := e.call(e.a.DeleteCampaigns, e.as(e.admin), "DELETE", "/api/campaigns?"+strings.Join(q, "&"), "", "")
	return code
}

func TestDeleteRefusedForCampaignsThatSendOrSent(t *testing.T) {
	e := newAuditEnv(t)
	refused := map[string]int{
		"running":                    e.campaignWith("running", 30, 0),
		"paused":                     e.campaignWith("paused", 30, 0),
		"finished with records":      e.campaignWith("finished", 100, 1),
		"cancelled with records":     e.campaignWith("cancelled", 40, 1),
		"cancelled, records pending": e.campaignWith("cancelled", 40, 0),
		"finished within a year":     e.campaignWith("finished", 100, 11),
	}
	for _, name := range []string{"finished with records", "cancelled with records"} {
		e.withRecords(refused[name])
	}
	for name, id := range refused {
		if code := e.deleteOne(id); code != http.StatusConflict {
			t.Errorf("%s: deleting answered %d, want 409", name, code)
		}
		if !e.campaignExists(id) {
			t.Errorf("%s: the campaign is gone", name)
		}
	}
	if n := e.sendRecords(refused["finished with records"]); n != 1 {
		t.Fatalf("send records kept: %d", n)
	}
	if rows := e.audits("action = $1", auditCampaignDelete); len(rows) != 0 {
		t.Fatalf("a refused delete is in the audit log: %+v", rows)
	}
}

func TestDeleteAllowedForCampaignsThatNeverSentOrAreOld(t *testing.T) {
	e := newAuditEnv(t)
	ok := map[string]int{
		"draft":               e.campaignWith("draft", 0, -1),
		"scheduled":           e.campaignWith("scheduled", 0, -1),
		"cancelled, no mail":  e.campaignWith("cancelled", 0, 0),
		"old, records purged": e.campaignWith("finished", 100, 13),
	}
	for name, id := range ok {
		if code := e.deleteOne(id); code != http.StatusOK {
			t.Errorf("%s: deleting answered %d, want 200", name, code)
		}
		if e.campaignExists(id) {
			t.Errorf("%s: the campaign is still there", name)
		}
	}
	if code := e.deleteOne(999999); code != http.StatusBadRequest {
		t.Fatalf("a campaign that does not exist: %d, want 400", code)
	}
}

func TestBulkDeleteRefusedAsAWhole(t *testing.T) {
	e := newAuditEnv(t)
	draft := e.campaignWith("draft", 0, -1)
	scheduled := e.campaignWith("scheduled", 0, -1)
	sent := e.campaignWith("finished", 100, 1)
	e.withRecords(sent)
	running := e.campaignWith("running", 10, 0)

	if code := e.deleteMany(draft, scheduled, sent, running); code != http.StatusConflict {
		t.Fatalf("bulk with a sent and a running campaign: %d, want 409", code)
	}
	for name, id := range map[string]int{"draft": draft, "scheduled": scheduled, "sent": sent, "running": running} {
		if !e.campaignExists(id) {
			t.Errorf("%s was deleted although the request was refused", name)
		}
	}
	if rows := e.audits("action = $1", auditCampaignDelete); len(rows) != 0 {
		t.Fatalf("audit rows after a refusal: %+v", rows)
	}
	if code := e.deleteMany(draft, scheduled); code != http.StatusOK {
		t.Fatalf("bulk of drafts: %d", code)
	}
	if e.campaignExists(draft) || e.campaignExists(scheduled) {
		t.Fatal("the drafts are still there")
	}
	if n := len(e.audits("action = $1", auditCampaignDelete)); n != 2 {
		t.Fatalf("audit rows: %d, want 2", n)
	}
}

// Several refused campaigns are one 409 that counts them (the message names the first three).
func TestBulkRefusalIsOne409(t *testing.T) {
	e := newAuditEnv(t)
	var ids []int
	for i := 0; i < 4; i++ {
		ids = append(ids, e.campaignWith("paused", 5, 0))
	}
	_, err := e.core.DeleteCampaignsGuarded(ids, "", true, nil)
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusConflict || !strings.Contains(fmt.Sprint(he.Message), "campaigns.axmDeleteBlockedMany") {
		t.Fatalf("refusal: %#v", err)
	}
}

// A user who may not reach a campaign (its lists are not theirs) neither deletes it nor is it
// counted: the campaign outside their lists does not make the delete refuse.
func TestDeleteGuardRespectsListPermission(t *testing.T) {
	e := newAuditEnv(t)
	mine := e.campaignWith("draft", 0, -1)
	foreign := e.campaignWith("running", 5, 0)
	e.db.MustExec(`UPDATE campaign_lists SET list_id = $2 WHERE campaign_id = $1`, mine, e.aList)
	e.db.MustExec(`UPDATE campaign_lists SET list_id = $2 WHERE campaign_id = $1`, foreign, e.bList)

	got, err := e.core.DeleteCampaignsGuarded([]int{mine, foreign}, "", false, []int{e.aList})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != mine || !got[0].Deleted {
		t.Fatalf("reached: %+v", got)
	}
	if !e.campaignExists(foreign) || e.campaignExists(mine) {
		t.Fatal("wrong campaigns deleted")
	}
}

// The audit row of a delete says what the campaign was: status, sent and to_send.
func TestDeleteAuditRecordsStatusAndCounts(t *testing.T) {
	e := newAuditEnv(t)
	id := e.campaignWith("scheduled", 0, -1)
	e.db.MustExec(`UPDATE campaigns SET to_send = 1872 WHERE id = $1`, id)
	if code := e.deleteOne(id); code != http.StatusOK {
		t.Fatal(code)
	}
	r := e.oneAudit(auditCampaignDelete)
	if r.Count == nil || *r.Count != 0 {
		t.Fatalf("count: %v", r.Count)
	}
	var d map[string]any
	if err := json.Unmarshal(r.Detail, &d); err != nil {
		t.Fatal(err)
	}
	if d["status"] != "scheduled" || d["sent"] != float64(0) || d["to_send"] != float64(1872) {
		t.Fatalf("detail: %s", r.Detail)
	}

	// An old campaign that sent: the numbers are the ones it had.
	old := e.campaignWith("finished", 98, 14)
	if code := e.deleteOne(old); code != http.StatusOK {
		t.Fatal(code)
	}
	var found bool
	for _, row := range e.audits("action = $1", auditCampaignDelete) {
		if row.TargetID == nil || *row.TargetID != old {
			continue
		}
		found = true
		var d map[string]any
		json.Unmarshal(row.Detail, &d)
		if row.Count == nil || *row.Count != 98 || d["status"] != "finished" || d["sent"] != float64(98) || d["to_send"] != float64(100) {
			t.Fatalf("old campaign row: %v %s", row.Count, row.Detail)
		}
	}
	if !found {
		t.Fatal("no audit row for the old campaign")
	}
}

// The guard is one statement: what it returns for a deleted campaign is its value at the time.
func TestGuardedDeleteReturnsValuesAtDeletion(t *testing.T) {
	e := newAuditEnv(t)
	id := e.campaignWith("draft", 0, -1)
	got, err := e.core.DeleteCampaignsGuarded([]int{id}, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := core.DeletedCampaign{ID: id, Status: "draft", Sent: 0, ToSend: 100, Deleted: true}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Status != want.Status || got[0].ToSend != want.ToSend || !got[0].Deleted || got[0].Block != "" {
		t.Fatalf("got %+v", got)
	}
}
