package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// LST-67: a scheduled campaign that is saved is judged again by the pre-send checks. Nobody to
// send to refuses the save (nothing is saved, the schedule stays as it was); the warnings are
// read from the preflight, which now answers for a scheduled campaign.

// scheduledEnv is the preflight app with its campaign scheduled for tomorrow with three people on
// its list.
func scheduledEnv(t *testing.T) *teamsEnv {
	t.Helper()
	e := newPreflightApp(t)
	for i := 0; i < 3; i++ {
		e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	}
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	return e
}

// save puts the campaign as the page does and returns the status and the answer's message.
func (e *teamsEnv) save(subject string, lists []int, sendAt *time.Time, typ string) (int, string) {
	e.t.Helper()
	body := map[string]any{
		"name": "Renamed", "subject": subject, "lists": lists, "type": typ, "content_type": "richtext",
		"messenger": "email", "from_email": "Team <team@crevasse.ai>", "attribs": map[string]any{}, "headers": []any{},
		"body": `<p>Edited <a href="{{ UnsubscribeURL }}">x</a></p>`,
	}
	// The page always says it: null takes the send time off.
	body["send_at"] = nil
	if sendAt != nil {
		body["send_at"] = sendAt.Format(time.RFC3339)
	}
	b, _ := json.Marshal(body)
	code, data := e.call(e.a.UpdateCampaign, e.as(e.superAdmin), http.MethodPut,
		fmt.Sprintf("/api/campaigns/%d", e.campID), strconv.Itoa(e.campID), string(b))
	return code, string(data)
}

// saveErr runs the save with a request body and returns the error the handler gave.
func (e *teamsEnv) saveErr(body string) error {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/campaigns/%d", e.campID), strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := echo.New().NewContext(req, httptest.NewRecorder())
	c.Set(auth.UserHTTPCtxKey, e.as(e.superAdmin))
	c.Set("id", e.campID)
	return e.a.UpdateCampaign(c)
}

func (e *teamsEnv) saved() (subject, status string, lists []int) {
	e.t.Helper()
	e.get(&subject, `SELECT subject FROM campaigns WHERE id=$1`, e.campID)
	e.get(&status, `SELECT status::TEXT FROM campaigns WHERE id=$1`, e.campID)
	rows, err := e.db.Query(`SELECT list_id FROM campaign_lists WHERE campaign_id=$1 AND list_id IS NOT NULL ORDER BY list_id`, e.campID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		rows.Scan(&id)
		lists = append(lists, id)
	}
	return
}

func TestScheduledSaveIsChecked(t *testing.T) {
	e := scheduledEnv(t)
	tomorrow := time.Now().Add(24 * time.Hour)
	empty := e.pfList("single", "")
	// A list whose only member left: nobody to send to as well.
	left := e.pfList("single", "")
	e.pfSub("enabled", "", map[int]string{left: "unsubscribed"})

	// Nobody on the new list: refused, and nothing is saved.
	for _, l := range []int{empty, left} {
		code, _ := e.save("Changed", []int{l}, &tomorrow, "regular")
		if code != http.StatusBadRequest {
			t.Fatalf("list %d: %d", l, code)
		}
		subject, status, lists := e.saved()
		if subject != "Subject" || status != "scheduled" || len(lists) != 1 || lists[0] != e.aList {
			t.Fatalf("the refused save changed the campaign: %q %s %v", subject, status, lists)
		}
	}

	// People on the lists: saved, still scheduled.
	code, data := e.save("Changed", []int{e.aList}, &tomorrow, "regular")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, data)
	}
	subject, status, _ := e.saved()
	if subject != "Changed" || status != "scheduled" {
		t.Fatalf("saved: %q %s", subject, status)
	}

	// Taking the send time off makes it a draft: not judged.
	if code, _ := e.save("Draft again", []int{empty}, nil, "regular"); code != http.StatusOK {
		t.Fatalf("a draft again: %d", code)
	}
	if _, status, _ := e.saved(); status != "draft" {
		t.Fatalf("status %s", status)
	}

	// Every list of the campaign deleted: no list to send to (the save asks for a list that is not there).
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=NULL WHERE campaign_id=$1`, e.campID)
	if code, _ := e.save("No list", []int{999999}, &tomorrow, "regular"); code != http.StatusBadRequest {
		t.Fatalf("no live list: %d", code)
	}
	if subject, _, _ := e.saved(); subject == "No list" {
		t.Fatal("saved without a list")
	}
}

// The words of the refusals are the ones of the screen's red notice.
func TestScheduledSaveRefusalWords(t *testing.T) {
	e := scheduledEnv(t)
	tomorrow := time.Now().Add(24 * time.Hour)
	empty := e.pfList("single", "")
	for _, c := range []struct {
		lists []int
		key   string
	}{{[]int{empty}, "campaigns.recheck.zero"}, {[]int{999999}, "campaigns.recheck.noLists"}} {
		b, _ := json.Marshal(map[string]any{"name": "N", "subject": "S", "lists": c.lists, "type": "regular", "content_type": "richtext",
			"messenger": "email", "from_email": "Team <team@crevasse.ai>", "attribs": map[string]any{}, "headers": []any{}, "body": "<p>x</p>", "send_at": tomorrow.Format(time.RFC3339)})
		err := e.saveErr(string(b))
		if err == nil || !strings.Contains(err.Error(), e.a.i18n.T(c.key)) {
			t.Fatalf("%s: %v", c.key, err)
		}
	}
}

// An opt-in campaign only warns in the window, so its save is not refused.
func TestScheduledSaveOptinNotRefused(t *testing.T) {
	e := scheduledEnv(t)
	empty := e.pfList("double", "")
	e.db.MustExec(`UPDATE campaigns SET type='optin' WHERE id=$1`, e.campID)
	tomorrow := time.Now().Add(24 * time.Hour)
	if code, data := e.save("Opt-in", []int{empty}, &tomorrow, "optin"); code != http.StatusOK {
		t.Fatalf("%d %s", code, data)
	}
}

// A draft and a paused campaign are saved as before: nobody on the list does not refuse it.
func TestDraftAndPausedSaveNotChecked(t *testing.T) {
	e := scheduledEnv(t)
	empty := e.pfList("single", "")
	for _, st := range []string{"draft", "paused"} {
		e.db.MustExec(`UPDATE campaigns SET status=$2::campaign_status, send_at=NULL WHERE id=$1`, e.campID, st)
		if code, data := e.save("Saved as "+st, []int{empty}, nil, "regular"); code != http.StatusOK {
			t.Fatalf("%s: %d %s", st, code, data)
		}
		if subject, status, lists := e.saved(); subject != "Saved as "+st || status != st || len(lists) != 1 || lists[0] != empty {
			t.Fatalf("%s: %q %s %v", st, subject, status, lists)
		}
	}
}

// The sender took the campaign up (running) between the page's read and the save: refused, the
// body does not change under a running send.
func TestScheduledSaveRaceWithSender(t *testing.T) {
	e := scheduledEnv(t)
	tx := e.db.MustBegin()
	var st string
	if err := tx.Get(&st, `SELECT status FROM campaigns WHERE id=$1 FOR UPDATE`, e.campID); err != nil {
		t.Fatal(err)
	}
	var o models.Campaign
	o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", "Raced", "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
	o.SendAt.Valid, o.SendAt.Time = true, time.Now().Add(time.Hour)
	o.ArchiveMeta = json.RawMessage("{}")
	o.Headers, o.Attribs = models.Headers{}, models.JSON{}
	done := make(chan error, 1)
	go func() {
		_, err := e.core.UpdateScheduledCampaign(context.Background(), e.campID, o, []int{e.aList}, nil, nil)
		done <- err
	}()
	// The save waits for the row; the sender takes the campaign up meanwhile.
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("did not wait for the row: %v", err)
	default:
	}
	tx.MustExec(`UPDATE campaigns SET status='running' WHERE id=$1`, e.campID)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("a running campaign was saved: %v", err)
	}
	if subject, _, _ := e.saved(); subject != "Subject" {
		t.Fatalf("the body changed under the send: %q", subject)
	}
}

// With one connection in the pool (the sender holds the other) the save does not wait for a
// second one (LST-48's rule for status changes).
func TestScheduledSaveOneConnection(t *testing.T) {
	e := newSendsEnv(t, 1)
	camp, _ := e.addCampaign("scheduled")
	e.db.MustExec(`UPDATE campaigns SET send_at = NOW() + INTERVAL '1 day' WHERE id = $1`, camp)
	e.db.MustExec(`UPDATE campaigns SET type='regular' WHERE id = $1`, camp)
	e.db.SetMaxOpenConns(1)
	defer e.db.SetMaxOpenConns(8)

	var o models.Campaign
	o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", "One connection", "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
	o.SendAt.Valid, o.SendAt.Time = true, time.Now().Add(time.Hour)
	o.ArchiveMeta = json.RawMessage("{}")
	o.Headers, o.Attribs = models.Headers{}, models.JSON{}
	asked := 0
	done := make(chan error, 1)
	go func() {
		out, err := e.core.UpdateScheduledCampaign(context.Background(), camp, o, []int{e.listID}, nil,
			func(lists []models.PreflightList, aud models.PreflightAudience, resumed bool) error { asked++; return nil })
		if err == nil && out.Subject != "One connection" {
			err = fmt.Errorf("subject %q", out.Subject)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the save waits for a second connection")
	}
	if asked != 1 {
		t.Fatalf("the check ran %d times", asked)
	}
}

// The preflight answers for a scheduled campaign: its mode, the warnings the save did not refuse,
// and a count that is who the send reaches.
func TestPreflightOfScheduledCampaign(t *testing.T) {
	e := scheduledEnv(t)
	member := e.as(e.ua)
	target := fmt.Sprintf("/api/campaigns/%d/preflight", e.campID)
	id := strconv.Itoa(e.campID)
	// The unsubscribe link is gone from the body, and the send time is ahead.
	e.db.MustExec(`UPDATE campaigns SET body='<p>No link here</p>' WHERE id=$1`, e.campID)

	var out struct {
		Mode       string                  `json:"mode"`
		Status     string                  `json:"status"`
		Recipients int                     `json:"recipients"`
		Checks     []models.PreflightCheck `json:"checks"`
		Tracking   *struct{ State string } `json:"tracking"`
	}
	data := pfRead(t, e, member, target, id)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	level := map[string]string{}
	for _, c := range out.Checks {
		level[c.Key] = c.Level
	}
	if out.Mode != "scheduled" || out.Status != "scheduled" || out.Tracking == nil {
		t.Fatalf("answer: %s", data)
	}
	if level["unsubscribe.missing"] != "warn" || level["schedule.past"] != "" || level["recipients.zero"] != "" {
		t.Fatalf("checks: %v", level)
	}

	// The count is what the sender reaches at the send time.
	ids, _ := e.reach(e.campID, 0)
	if out.Recipients != len(ids) || out.Recipients != 3 {
		t.Fatalf("says %d, the send reaches %d", out.Recipients, len(ids))
	}
}

// A follow-up (LST-47) whose check would hold everyone back reaches nobody: saving it while it is
// scheduled is refused, as the window stops it, and is saved while somebody is left.
func TestScheduledSaveOfFollowupLeavesOutWhoWouldBeHeld(t *testing.T) {
	t.Parallel()
	e := newFollowupEnv(t)
	got := e.makeFollowup(e.admin, `{"open":"no","no_team":true,"followup":true}`)
	camp := got.Followup.ID
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day', content_type='richtext', messenger='email', from_email='Team <team@crevasse.ai>' WHERE id=$1`, camp)
	save := func(subject string) error {
		var o models.Campaign
		o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", subject, "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
		o.SendAt.Valid, o.SendAt.Time = true, time.Now().Add(time.Hour)
		o.ArchiveMeta = json.RawMessage("{}")
		o.Headers, o.Attribs = models.Headers{}, models.JSON{}
		var lists []int
		if err := e.db.Select(&lists, `SELECT list_id FROM campaign_lists WHERE campaign_id=$1`, camp); err != nil {
			t.Fatal(err)
		}
		_, err := e.a.core.UpdateScheduledCampaign(context.Background(), camp, o, lists, nil, e.a.scheduledSaveCheck)
		return err
	}
	if err := save("Both still there"); err != nil {
		t.Fatalf("somebody is left: %v", err)
	}
	// Both of the two on its list opened the original: the check would hold both back.
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2), ($1, $3)`, e.campID, e.ids[2], e.ids[3])
	if err := save("Nobody left"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("nobody is left: %v", err)
	}
	var subject string
	e.get(&subject, `SELECT subject FROM campaigns WHERE id=$1`, camp)
	if subject != "Both still there" {
		t.Fatalf("the refused save changed the subject: %q", subject)
	}
}

// The campaign's row is not locked while the people are counted: the sender picks a campaign by the
// committed row, and a lock held for a count of seconds would make it wait with a decision made on
// the old send time (review: a save that moved the time a day on sent the old subject to 40 people).
func TestScheduledSaveDoesNotHoldTheRowWhileCounting(t *testing.T) {
	e := scheduledEnv(t)
	tomorrow := time.Now().Add(24 * time.Hour)
	var lockErr error
	check := func(lists []models.PreflightList, aud models.PreflightAudience, resumed bool) error {
		// What the sender's update takes: the row for a change of a non-key column.
		tx := e.db.MustBegin()
		defer tx.Rollback()
		_, lockErr = tx.Exec(`SELECT id FROM campaigns WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, e.campID)
		return nil
	}
	var o models.Campaign
	o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", "Moved", "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
	o.SendAt.Valid, o.SendAt.Time = true, tomorrow
	o.ArchiveMeta = json.RawMessage("{}")
	o.Headers, o.Attribs = models.Headers{}, models.JSON{}
	if _, err := e.core.UpdateScheduledCampaign(context.Background(), e.campID, o, []int{e.aList}, nil, check); err != nil {
		t.Fatal(err)
	}
	if lockErr != nil {
		t.Fatalf("the row is locked while the people are counted: %v", lockErr)
	}
}

// The sender takes the campaign up while the people are counted (its send time passed): the save
// is refused and the campaign is not changed, instead of moving the time under a campaign that is
// already going out (review: it answered 200 with the new time and the old subject was sent).
func TestScheduledSaveRefusedWhenTheSenderTookItDuringTheCount(t *testing.T) {
	e := scheduledEnv(t)
	check := func(lists []models.PreflightList, aud models.PreflightAudience, resumed bool) error {
		e.db.MustExec(`UPDATE campaigns SET send_at = NOW() - INTERVAL '1 second' WHERE id=$1`, e.campID)
		if _, err := e.store.NextCampaigns([]int64{}); err != nil {
			t.Error(err)
		}
		return nil
	}
	var o models.Campaign
	o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", "Moved", "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
	o.SendAt.Valid, o.SendAt.Time = true, time.Now().Add(24*time.Hour)
	o.ArchiveMeta = json.RawMessage("{}")
	o.Headers, o.Attribs = models.Headers{}, models.JSON{}
	_, err := e.core.UpdateScheduledCampaign(context.Background(), e.campID, o, []int{e.aList}, nil, check)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("saved a campaign that was taken up: %v", err)
	}
	var subject, status string
	e.get(&subject, `SELECT subject FROM campaigns WHERE id=$1`, e.campID)
	e.get(&status, `SELECT status::TEXT FROM campaigns WHERE id=$1`, e.campID)
	if subject != "Subject" || status != "running" {
		t.Fatalf("%q %s", subject, status)
	}
}

// A campaign that was paused and scheduled again goes on after its checkpoint and skips who was
// sent to (review: leaving only the people already mailed saved as "20 people" and sent nothing).
func TestScheduledSaveOfAPausedThenScheduledCampaign(t *testing.T) {
	e := scheduledEnv(t)
	member := e.as(e.superAdmin)
	l1, l2 := e.pfList("single", ""), e.pfList("single", "")
	var xs []int
	for i := 0; i < 4; i++ {
		xs = append(xs, e.pfSub("enabled", "", map[int]string{l1: "confirmed"}))
	}
	// l2 has only two of the people who were sent to.
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$3,'confirmed'),($2,$3,'confirmed')`, xs[0], xs[1], l2)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, l1)
	e.db.MustExec(`UPDATE campaigns SET last_subscriber_id=$2, to_send=4, sent=2 WHERE id=$1`, e.campID, xs[1])
	for _, id := range xs[:2] {
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, e.campID, id)
	}
	tomorrow := time.Now().Add(24 * time.Hour)

	// The window says what is left to send.
	var out struct {
		Mode       string `json:"mode"`
		Resumed    bool   `json:"resumed"`
		Recipients int    `json:"recipients"`
		Remaining  int    `json:"remaining"`
	}
	data := pfRead(t, e, member, fmt.Sprintf("/api/campaigns/%d/preflight", e.campID), strconv.Itoa(e.campID))
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "scheduled" || !out.Resumed || out.Recipients != 4 || out.Remaining != 2 {
		t.Fatalf("answer: %s", data)
	}

	// Only the people already sent to: nobody is left, refused.
	if code, _ := e.save("Only sent", []int{l2}, &tomorrow, "regular"); code != http.StatusBadRequest {
		t.Fatalf("only the sent: %d", code)
	}
	if _, _, lists := e.saved(); len(lists) != 1 || lists[0] != l1 {
		t.Fatalf("the refused save changed the lists: %v", lists)
	}
	// Somebody left to send to: saved.
	if code, data := e.save("Some left", []int{l1}, &tomorrow, "regular"); code != http.StatusOK {
		t.Fatalf("%d %s", code, data)
	}
}

// The last list taken off a scheduled campaign says so, with the way on, not "invalid list IDs".
func TestScheduledSaveWithoutAnyList(t *testing.T) {
	e := scheduledEnv(t)
	tomorrow := time.Now().Add(24 * time.Hour)
	b, _ := json.Marshal(map[string]any{"name": "N", "subject": "S", "lists": []int{}, "type": "regular", "content_type": "richtext",
		"messenger": "email", "from_email": "Team <team@crevasse.ai>", "attribs": map[string]any{}, "headers": []any{},
		"body": "<p>x</p>", "send_at": tomorrow.Format(time.RFC3339)})
	err := e.saveErr(string(b))
	if err == nil || !strings.Contains(err.Error(), e.a.i18n.T("campaigns.recheck.noLists")) {
		t.Fatalf("%v", err)
	}
	if _, status, lists := e.saved(); status != "scheduled" || len(lists) != 1 {
		t.Fatalf("%s %v", status, lists)
	}
}
