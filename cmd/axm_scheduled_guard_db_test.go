package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/labstack/echo/v4"
)

// LST-71: scheduled sending safety. Starting or scheduling with nobody to send to is refused by
// the server (not only by the confirmation window), a due scheduled campaign with nobody on its
// lists leaves the schedule, a draft's save is made under the campaign lock, and a template or
// the From list of the settings is not changed under a scheduled campaign that needs it.

// statusErr puts a status change as the API does and returns the HTTP code and the error text.
func (e *teamsEnv) statusErr(id int, body string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/campaigns/%d/status", id), strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set("id", id)
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	if err := e.a.UpdateCampaignStatus(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he.Code, fmt.Sprint(he.Message)
		}
		e.t.Fatal(err)
	}
	return rec.Code, rec.Body.String()
}

func (e *teamsEnv) statusOf(id int) string {
	var s string
	e.get(&s, `SELECT status::TEXT FROM campaigns WHERE id=$1`, id)
	return s
}

func TestStatusRefusesNobodyToSendTo(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	empty := e.pfList("single", "")
	left := e.pfList("single", "")
	e.pfSub("enabled", "", map[int]string{left: "unsubscribed"})
	blocked := e.pfList("single", "")
	e.pfSub("blocklisted", "", map[int]string{blocked: "confirmed"})
	double := e.pfList("double", "")
	e.pfSub("enabled", "", map[int]string{double: "unconfirmed"})
	withOne := e.pfList("single", "")
	e.pfSub("enabled", "", map[int]string{withOne: "confirmed"})

	for _, status := range []string{"running", "scheduled"} {
		for name, lists := range map[string][]int{"empty list": {empty}, "only unsubscribed": {left},
			"only blocklisted": {blocked}, "only unconfirmed on a double list": {double}, "no list": {}} {
			t.Run(status+" "+name, func(t *testing.T) {
				id := e.pfCampaign("regular", "draft", lists...)
				if status == "scheduled" {
					e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, id)
				}
				code, msg := e.statusErr(id, fmt.Sprintf(`{"status":%q}`, status))
				want := "campaigns.start.zero"
				if len(lists) == 0 {
					want = "campaigns.start.noLists"
				}
				if code != http.StatusBadRequest || !strings.Contains(msg, want) {
					t.Fatalf("got HTTP %d %q, want 400 %s", code, msg, want)
				}
				if s := e.statusOf(id); s != "draft" {
					t.Fatalf("the refused change left status %s", s)
				}
			})
		}
	}

	// A list that was deleted: the campaign keeps its row with no list.
	gone := e.pfCampaign("regular", "draft", withOne)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=NULL WHERE campaign_id=$1`, gone)
	if code, msg := e.statusErr(gone, `{"status":"running"}`); code != http.StatusBadRequest || !strings.Contains(msg, "noLists") {
		t.Fatalf("deleted list: HTTP %d %q", code, msg)
	}

	// Somebody to send to: started and scheduled as before.
	run := e.pfCampaign("regular", "draft", withOne)
	if code, msg := e.statusErr(run, `{"status":"running"}`); code != http.StatusOK {
		t.Fatalf("start: HTTP %d %q", code, msg)
	}
	sched := e.pfCampaign("regular", "draft", withOne, empty)
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, sched)
	if code, msg := e.statusErr(sched, `{"status":"scheduled"}`); code != http.StatusOK || e.statusOf(sched) != "scheduled" {
		t.Fatalf("schedule: HTTP %d %q status %s", code, msg, e.statusOf(sched))
	}

	// An opt-in campaign is not judged by people (the confirmation only warns), nor is a resume.
	optin := e.pfCampaign("optin", "draft", empty)
	if code, msg := e.statusErr(optin, `{"status":"running"}`); code != http.StatusOK {
		t.Fatalf("opt-in start: HTTP %d %q", code, msg)
	}
}

// A campaign started from the window carries the version it showed: the check does not depend on it.
func TestStatusRefusesNobodyWithConfirmationVersion(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, e.pfList("single", ""))
	var seen string
	e.get(&seen, `SELECT updated_at::TEXT FROM campaigns WHERE id=$1`, e.campID)
	code, msg := e.statusErr(e.campID, fmt.Sprintf(`{"status":"running","updated_at":%q}`, seen))
	if code != http.StatusBadRequest || !strings.Contains(msg, "campaigns.start.zero") {
		t.Fatalf("HTTP %d %q", code, msg)
	}
}

// A due campaign with nobody on its lists leaves the schedule instead of being found again at
// every scan and going to whoever subscribes later.
func TestDueScheduledWithNobodyReturnsToDraft(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	due := func(c int) { e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() - INTERVAL '1 minute' WHERE id=$1`, c) }
	count := func(action string, id int) (n int) {
		e.get(&n, `SELECT COUNT(*) FROM axm_audit_log WHERE action=$1 AND target_id=$2 AND user_type='system'`, action, id)
		return n
	}

	left := e.pfList("single", "")
	e.pfSub("enabled", "", map[int]string{left: "unsubscribed"})
	withOne := e.pfList("single", "")
	e.pfSub("enabled", "", map[int]string{withOne: "confirmed"})

	noList := e.pfCampaign("regular", "draft", withOne)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=NULL WHERE campaign_id=$1`, noList) // the list was deleted
	nobody := e.pfCampaign("regular", "draft", left)
	someone := e.pfCampaign("regular", "draft", withOne)
	later := e.pfCampaign("regular", "draft", left)
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 hour' WHERE id=$1`, later)
	optin := e.pfCampaign("optin", "draft", left)
	resumed := e.pfCampaign("regular", "draft", withOne)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=NULL WHERE campaign_id=$1`, resumed)
	e.db.MustExec(`UPDATE campaigns SET started_at=NOW() - INTERVAL '1 day' WHERE id=$1`, resumed)
	resumedLeft := e.pfCampaign("regular", "draft", left)
	e.db.MustExec(`UPDATE campaigns SET started_at=NOW() - INTERVAL '1 day' WHERE id=$1`, resumedLeft)
	for _, c := range []int{noList, nobody, someone, optin, resumed, resumedLeft} {
		due(c)
	}

	got, err := e.store.NextCampaigns([]int64{})
	if err != nil {
		t.Fatal(err)
	}
	picked := map[int]bool{}
	for _, c := range got {
		picked[c.ID] = true
	}
	for id, want := range map[int]bool{noList: false, nobody: false, someone: true, later: false, optin: true, resumed: false, resumedLeft: true} {
		if picked[id] != want {
			t.Errorf("campaign %d handed to the manager: %v, want %v", id, picked[id], want)
		}
	}
	for id, want := range map[int]string{noList: "draft", nobody: "draft", someone: "running", later: "scheduled",
		resumed: "paused", resumedLeft: "scheduled"} {
		if s := e.statusOf(id); s != want {
			t.Errorf("campaign %d is %s, want %s", id, s, want)
		}
	}
	for id, want := range map[int]int{noList: 1, nobody: 1, resumed: 1, someone: 0, later: 0, resumedLeft: 0} {
		if n := count("campaign.unschedule", id); n != want {
			t.Errorf("campaign %d: %d audit rows, want %d", id, n, want)
		}
	}

	// A scan again finds nothing to return: they do not come back.
	again, err := e.store.NextCampaigns([]int64{int64(someone), int64(optin)})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.ID == noList || c.ID == nobody || c.ID == resumed {
			t.Errorf("campaign %d found again", c.ID)
		}
	}
	// Somebody subscribes afterwards: the campaign is a draft, nothing goes out until it is scheduled again.
	e.pfSub("enabled", "", map[int]string{left: "confirmed"})
	if got, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	} else {
		for _, c := range got {
			if c.ID == nobody {
				t.Errorf("the draft was sent when somebody subscribed")
			}
		}
	}
}

// An edit judged for a draft is not saved into a campaign that is no longer a draft.
func TestDraftSaveUnderLock(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	ctx := context.Background()
	camp, err := e.core.GetCampaign(e.campID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	lists := []int{e.aList}
	camp.Subject = "Edited"
	subject := func() (s string) { e.get(&s, `SELECT subject FROM campaigns WHERE id=$1`, e.campID); return }

	// The campaign was scheduled or started after the handler read it.
	for status, wantCode := range map[string]int{"scheduled": http.StatusConflict, "running": http.StatusBadRequest, "finished": http.StatusBadRequest} {
		e.db.MustExec(`UPDATE campaigns SET status=$2::campaign_status WHERE id=$1`, e.campID, status)
		_, err := e.core.UpdateDraftCampaign(ctx, e.campID, camp, lists, nil, "draft")
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != wantCode {
			t.Fatalf("%s: %v, want HTTP %d", status, err, wantCode)
		}
		if s := subject(); s != "Subject" {
			t.Fatalf("%s: the refused edit was saved (%q)", status, s)
		}
	}

	// Still a draft (or paused, as read): saved.
	for _, status := range []string{"draft", "paused"} {
		e.db.MustExec(`UPDATE campaigns SET status=$2::campaign_status WHERE id=$1`, e.campID, status)
		if _, err := e.core.UpdateDraftCampaign(ctx, e.campID, camp, lists, nil, status); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if s := subject(); s != "Edited" {
			t.Fatalf("%s: %q", status, s)
		}
		e.db.MustExec(`UPDATE campaigns SET subject='Subject' WHERE id=$1`, e.campID)
	}

	// It is serialized with the status change: a save that waits for the lock sees the new status.
	e.db.MustExec(`UPDATE campaigns SET status='draft' WHERE id=$1`, e.campID)
	tx := e.db.MustBegin()
	if _, err := tx.Exec(`SELECT id FROM campaigns WHERE id=$1 FOR UPDATE`, e.campID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var saveErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, saveErr = e.core.UpdateDraftCampaign(ctx, e.campID, camp, lists, nil, "draft")
	}()
	time.Sleep(300 * time.Millisecond)
	tx.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if he, ok := saveErr.(*echo.HTTPError); !ok || he.Code != http.StatusConflict {
		t.Fatalf("the save that waited for the status change: %v, want 409", saveErr)
	}
	if s := subject(); s != "Subject" {
		t.Fatalf("saved into the scheduled campaign: %q", s)
	}
}

// The handler's draft save goes through the locked path and still saves and reports as before.
func TestDraftSaveThroughHandler(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	code, data := e.save("Draft edit", []int{e.aList}, nil, "regular")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, data)
	}
	if subject, status, _ := e.saved(); subject != "Draft edit" || status != "draft" {
		t.Fatalf("%q %s", subject, status)
	}
	e.db.MustExec(`UPDATE campaigns SET status='running' WHERE id=$1`, e.campID)
	if code, _ := e.save("Running edit", []int{e.aList}, nil, "regular"); code != http.StatusBadRequest {
		t.Fatalf("a running campaign was saved: %d", code)
	}
	if subject, _, _ := e.saved(); subject != "Draft edit" {
		t.Fatalf("%q", subject)
	}
}

func TestTemplateSaveKeepsScheduledUnsubscribeLink(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	link := `{{ template "content" . }}<a href="{{ UnsubscribeURL }}">x</a>`
	var tpl int
	e.get(&tpl, `INSERT INTO templates(name,type,subject,body,is_default) VALUES('T','campaign','', $1, false) RETURNING id`, link)
	// The campaign's own body has no link: the template carries it.
	e.db.MustExec(`UPDATE campaigns SET template_id=$2, body='<p>Hi</p>', status='scheduled', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID, tpl)
	var already int
	e.get(&already, `INSERT INTO templates(name,type,subject,body,is_default) VALUES('Bare','campaign','', '{{ template "content" . }}', false) RETURNING id`)
	other := e.pfCampaign("regular", "scheduled", e.aList)
	e.db.MustExec(`UPDATE campaigns SET template_id=$2, body='<p>Hi</p>', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, other, already)

	put := func(id int, body string) (int, string) {
		req := httptest.NewRequest(http.MethodPut, "/api/templates/"+strconv.Itoa(id),
			strings.NewReader(fmt.Sprintf(`{"name":"T","type":"campaign","body":%q}`, body)))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.Set("id", id)
		if err := e.a.UpdateTemplate(c); err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return he.Code, fmt.Sprint(he.Message)
			}
			t.Fatal(err)
		}
		return http.StatusOK, ""
	}
	bodyOf := func(id int) (b string) { e.get(&b, `SELECT body FROM templates WHERE id=$1`, id); return }

	// Taking the link away from a scheduled campaign: refused, and it names the campaign.
	code, msg := put(tpl, `{{ template "content" . }}<p>no link</p>`)
	if code != http.StatusBadRequest || !strings.Contains(msg, "#"+strconv.Itoa(e.campID)) || strings.Contains(msg, "#"+strconv.Itoa(other)) {
		t.Fatalf("HTTP %d %q", code, msg)
	}
	if bodyOf(tpl) != link {
		t.Fatalf("the refused template was saved: %q", bodyOf(tpl))
	}
	// A change that keeps the link: saved.
	keep := `<div>{{ template "content" . }}</div><a href="{{ UnsubscribeURL }}">leave</a>`
	if code, msg := put(tpl, keep); code != http.StatusOK || bodyOf(tpl) != keep {
		t.Fatalf("HTTP %d %q", code, msg)
	}
	// A campaign that does not have the link already is not a reason to refuse a template change.
	if code, msg := put(already, `<div>{{ template "content" . }}</div>`); code != http.StatusOK {
		t.Fatalf("HTTP %d %q", code, msg)
	}
	// Once nothing is scheduled with it, any change is saved.
	e.db.MustExec(`UPDATE campaigns SET status='draft' WHERE id=$1`, e.campID)
	if code, msg := put(tpl, `{{ template "content" . }}`); code != http.StatusOK {
		t.Fatalf("HTTP %d %q", code, msg)
	}
}

func TestFromListChangeKeepsScheduledSenders(t *testing.T) {
	e := newPreflightAppScan(t, false)
	t.Cleanup(e.a.manager.Close)
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day', from_email='Team <team@crevasse.ai>' WHERE id=$1`, e.campID)
	draft := e.pfCampaign("regular", "draft", e.aList)
	e.db.MustExec(`UPDATE campaigns SET from_email='Draft <draft@crevasse.ai>' WHERE id=$1`, draft)

	team, other := "Team <team@crevasse.ai>", "News <news@crevasse.ai>"
	for _, c := range []struct {
		name                string
		oldDomains, oldList []string
		newDomains, newList []string
		refused             bool
	}{
		{"the scheduled From leaves the list", nil, []string{team}, nil, []string{other}, true},
		{"the same address in another spelling stays", nil, []string{team}, nil, []string{"team@crevasse.ai", other}, false},
		{"a list where there was none", nil, nil, nil, []string{other}, false},
		{"the list is taken away", nil, []string{team}, nil, nil, false},
		{"account senders on", nil, []string{team}, []string{"crevasse.ai"}, []string{other}, false},
		{"account senders off with a list", []string{"crevasse.ai"}, nil, nil, []string{other}, false},
		{"a list that never had it is not a reason to refuse", nil, []string{other}, nil, []string{other, "Z <z@crevasse.ai>"}, false},
		{"an old list that did not have it, changed", nil, []string{other}, nil, []string{"Z <z@crevasse.ai>"}, false},
	} {
		err := e.a.checkScheduledSenders(c.oldDomains, c.oldList, c.newDomains, c.newList)
		he, ok := err.(*echo.HTTPError)
		if c.refused {
			if !ok || he.Code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(he.Message), "#"+strconv.Itoa(e.campID)) ||
				strings.Contains(fmt.Sprint(he.Message), "#"+strconv.Itoa(draft)) {
				t.Errorf("%s: %v, want a refusal that names the scheduled campaign only", c.name, err)
			}
		} else if err != nil {
			t.Errorf("%s: %v, want it saved", c.name, err)
		}
	}
}
