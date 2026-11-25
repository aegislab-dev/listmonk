package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"gopkg.in/volatiletech/null.v6"
)

// LST-67: a scheduled campaign that is saved is judged again by the pre-send checks. Nobody to
// send to refuses the save (nothing is saved, the schedule stays as it was); the warnings are
// read from the preflight, which now answers for a scheduled campaign.

// scheduledEnv is the preflight app with its campaign scheduled for tomorrow with three people on
// its list.
func scheduledEnv(t *testing.T) *teamsEnv {
	t.Helper()
	e := newPreflightApp(t)
	t.Cleanup(e.a.manager.Close)
	for i := 0; i < 3; i++ {
		e.pfSub("enabled", "", map[int]string{e.aList: "confirmed"})
	}
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '1 day' WHERE id=$1`, e.campID)
	return e
}

// save puts the campaign as the page does and returns the status and the answer's message.
func (e *teamsEnv) save(subject string, lists []int, sendAt *time.Time, typ string) (int, string) {
	e.t.Helper()
	return e.saveAs(e.as(e.superAdmin), subject, lists, sendAt, typ)
}

func (e *teamsEnv) saveAs(user auth.User, subject string, lists []int, sendAt *time.Time, typ string) (int, string) {
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
	code, data := e.call(e.a.UpdateCampaign, user, http.MethodPut,
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
	defer tx.Rollback()
	var pid int
	if err := tx.Get(&pid, `SELECT pg_backend_pid() FROM campaigns WHERE id=$1 FOR NO KEY UPDATE`, e.campID); err != nil {
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
	waitForScheduledBlock(t, e, pid)
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
	e.db.MustExec(`SET work_mem = '8MB'`)

	var o models.Campaign
	o.Name, o.Subject, o.FromEmail, o.Body, o.ContentType, o.Messenger = "N", "One connection", "Team <team@crevasse.ai>", "<p>x</p>", "richtext", "email"
	o.SendAt.Valid, o.SendAt.Time = true, time.Now().Add(time.Hour)
	o.ArchiveMeta = json.RawMessage("{}")
	o.Headers, o.Attribs = models.Headers{}, models.JSON{}
	asked := 0
	done := make(chan error, 1)
	go func() {
		out, err := e.core.UpdateScheduledCampaign(context.Background(), camp, o, []int{e.listID}, nil,
			func(lists []models.PreflightList, aud models.PreflightAudience) error { asked++; return nil })
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
	var workMem string
	e.get(&workMem, `SHOW work_mem`)
	if workMem != "8MB" {
		t.Fatalf("the save left work_mem at %q", workMem)
	}
	if _, _, err := e.core.GetPreflightAudience(context.Background(), camp, 0, true); err != nil {
		t.Fatal(err)
	}
	e.get(&workMem, `SHOW work_mem`)
	if workMem != "8MB" {
		t.Fatalf("the audience read left work_mem at %q", workMem)
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
	// One person received the follow-up, and their ledger entry expired. If they also open
	// the original, the preflight must not subtract them a second time as someone to hold.
	e.db.MustExec(`INSERT INTO axm_campaign_followup_claims (campaign_id, subscriber_id, sent) VALUES ($1, $2, TRUE)`, camp, e.ids[2])
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[2])
	if err := save("One left"); err != nil {
		t.Fatalf("one unsent recipient is left: %v", err)
	}
	sample := e.a.core.GetPreflightSample(context.Background(), camp, 10, 0, true)
	if len(sample) != 1 || sample[0].ID != e.ids[3] {
		t.Fatalf("expected the unsent recipient in the sample, got %v", sample)
	}
	e.db.MustExec(`INSERT INTO campaign_views (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.ids[3])
	if err := save("Nobody left"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("nobody is left: %v", err)
	}
	var subject string
	e.get(&subject, `SELECT subject FROM campaigns WHERE id=$1`, camp)
	if subject != "One left" {
		t.Fatalf("the refused save changed the subject: %q", subject)
	}
}

// blockScheduledListCount holds only this dynamic list's reads until the test releases them.
func blockScheduledListCount(t *testing.T, e *teamsEnv, list int) (*sqlx.Tx, int) {
	t.Helper()
	e.db.MustExec(`ALTER FUNCTION axm_dynamic_list_members(INT, INT, INT, INT) RENAME TO test_dynamic_list_members`)
	e.db.MustExec(fmt.Sprintf(`CREATE FUNCTION axm_dynamic_list_members(
			p_list INT, p_after INT DEFAULT 0, p_upto INT DEFAULT 2147483647, p_limit INT DEFAULT NULL)
		RETURNS TABLE(subscriber_id INT) LANGUAGE plpgsql STABLE AS $$
		BEGIN
			IF p_list = %d THEN
				PERFORM pg_advisory_xact_lock(6767, p_list);
			END IF;
			RETURN QUERY SELECT * FROM test_dynamic_list_members(p_list, p_after, p_upto, p_limit);
		END;
		$$`, list))
	tx := e.db.MustBegin()
	t.Cleanup(func() { tx.Rollback() })
	tx.MustExec(`SELECT pg_advisory_xact_lock(6767, $1)`, list)
	var pid int
	if err := tx.Get(&pid, `SELECT pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

func waitForScheduledBlock(t *testing.T, e *teamsEnv, blocker int) int {
	t.Helper()
	var pid int
	waitFor(t, fmt.Sprintf("a connection blocked by PID %d", blocker), func() bool {
		e.get(&pid, `SELECT COALESCE(MIN(pid), 0) FROM pg_stat_activity
			WHERE datname = current_database() AND $1 = ANY(pg_blocking_pids(pid))`, blocker)
		return pid != 0
	})
	return pid
}

func waitForScheduledSave(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("scheduled save did not return")
		return 0
	}
}

// The actual sender is counting the old dynamic list when a PUT commits a new title, list
// and time. Releasing that scan must not start the campaign with either version.
func TestScheduledSavePostponesCampaignWhileSenderCounts(t *testing.T) {
	e := scheduledEnv(t)
	e.pfSub("enabled", "pro", map[int]string{e.aList: "confirmed"})
	dynamic := e.pfList("single", dynFilter(e.aList))
	gate, blocker := blockScheduledListCount(t, e, dynamic)
	defer gate.Rollback()
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, dynamic)
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() - INTERVAL '1 second' WHERE id=$1`, e.campID)
	waitForScheduledBlock(t, e, blocker)

	tomorrow := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	done := make(chan int, 1)
	go func() {
		code, _ := e.save("Postponed", []int{e.aList}, &tomorrow, "regular")
		done <- code
	}()
	if code := waitForScheduledSave(t, done); code != http.StatusOK {
		t.Fatalf("postpone: HTTP %d", code)
	}

	// A campaign made runnable after the gate was reached can only finish in a later scan.
	probe := e.pfCampaign("regular", "running", e.aList)
	if err := gate.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next scan to send the probe", func() bool {
		var status string
		e.get(&status, `SELECT status FROM campaigns WHERE id=$1`, probe)
		return status == "finished"
	})

	subject, status, lists := e.saved()
	var sendAt time.Time
	e.get(&sendAt, `SELECT send_at FROM campaigns WHERE id=$1`, e.campID)
	if subject != "Postponed" || status != "scheduled" || !sendAt.Equal(tomorrow) || !slices.Equal(lists, []int{e.aList}) {
		t.Fatalf("postponed campaign: %q %s %s %v", subject, status, sendAt, lists)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id=$1`, e.campID)
	if n != 0 {
		t.Fatalf("the postponed campaign delivered %d messages", n)
	}
}

// B starts its list replacement while A is counting its new dynamic list. B must count
// just its own empty list and fail, even though A's list was not visible when B started.
func TestScheduledSaveConcurrentListReplacement(t *testing.T) {
	e := scheduledEnv(t)
	e.pfSub("enabled", "pro", map[int]string{e.aList: "confirmed"})
	dynamic := e.pfList("single", dynFilter(e.aList))
	empty := e.pfList("single", "")
	gate, blocker := blockScheduledListCount(t, e, dynamic)
	defer gate.Rollback()

	tomorrow := time.Now().Add(24 * time.Hour)
	editorA, editorB := e.as(e.superAdmin), e.as(e.admin)
	a, b := make(chan int, 1), make(chan int, 1)
	go func() {
		code, _ := e.saveAs(editorA, "A", []int{dynamic}, &tomorrow, "regular")
		a <- code
	}()
	editor := waitForScheduledBlock(t, e, blocker)
	go func() {
		code, _ := e.saveAs(editorB, "B", []int{empty}, &tomorrow, "regular")
		b <- code
	}()
	waitForScheduledBlock(t, e, editor)
	if err := gate.Rollback(); err != nil {
		t.Fatal(err)
	}
	if code := waitForScheduledSave(t, a); code != http.StatusOK {
		t.Fatalf("A: HTTP %d", code)
	}
	if code := waitForScheduledSave(t, b); code != http.StatusBadRequest {
		t.Fatalf("B: HTTP %d", code)
	}
	if subject, status, lists := e.saved(); subject != "A" || status != "scheduled" || !slices.Equal(lists, []int{dynamic}) {
		t.Fatalf("saved from A: %q %s %v", subject, status, lists)
	}
}

// The ledger can contain sends even before the sender has saved its count and checkpoint.
func TestScheduledSaveOfAPausedThenScheduledCampaign(t *testing.T) {
	for _, savedProgress := range []bool{true, false} {
		t.Run(fmt.Sprintf("saved progress %t", savedProgress), func(t *testing.T) {
			e := scheduledEnv(t)
			member := e.as(e.superAdmin)
			l1, l2 := e.pfList("single", ""), e.pfList("single", "")
			var xs []int
			const total, sentBefore = 40, 20
			for i := 0; i < total; i++ {
				xs = append(xs, e.pfSub("enabled", "", map[int]string{l1: "confirmed"}))
			}
			e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, l1)
			e.db.MustExec(`UPDATE campaigns SET status='paused', to_send=$2 WHERE id=$1`, e.campID, total)
			if savedProgress {
				e.db.MustExec(`UPDATE campaigns SET last_subscriber_id=$2, sent=$3 WHERE id=$1`, e.campID, xs[sentBefore-1], sentBefore)
			}
			for _, id := range xs[:sentBefore] {
				e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, id, l2)
				e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, e.campID, id)
			}

			path, id := fmt.Sprintf("/api/campaigns/%d", e.campID), strconv.Itoa(e.campID)
			if code, data := e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, path+"/status", id, `{"status":"running"}`); code != http.StatusOK {
				t.Fatalf("resume: %d %s", code, data)
			}
			data := pfRead(t, e, member, path+"/preflight", id)
			var out preflightResponse
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if out.Mode != "scheduled" || !out.Resumed || out.Recipients != total ||
				out.Remaining != total-sentBefore || out.Sent != sentBefore || out.ToSend != total {
				t.Fatalf("answer: %s", data)
			}
			if !savedProgress {
				var sent int
				e.get(&sent, `SELECT sent FROM campaigns WHERE id=$1`, e.campID)
				if sent != 0 {
					t.Fatalf("preflight wrote the campaign's progress: %d", sent)
				}
			}

			tomorrow := time.Now().Add(24 * time.Hour)
			if code, _ := e.save("Only sent", []int{l2}, &tomorrow, "regular"); code != http.StatusBadRequest {
				t.Fatalf("only the sent: %d", code)
			}
			if subject, _, lists := e.saved(); subject != "Subject" || !slices.Equal(lists, []int{l1}) {
				t.Fatalf("the refused save changed the campaign: %q %v", subject, lists)
			}
			if code, data := e.save("Some left", []int{l1}, &tomorrow, "regular"); code != http.StatusOK {
				t.Fatalf("%d %s", code, data)
			}
			e.db.MustExec(`UPDATE campaigns SET send_at=NOW() - INTERVAL '1 second' WHERE id=$1`, e.campID)
			waitFor(t, "the rescheduled send and its delivery records", func() bool {
				var done bool
				e.get(&done, `SELECT status = 'finished' AND
					(SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id=$1) = $2
					FROM campaigns WHERE id=$1`, e.campID, len(xs))
				return done
			})
			var sent int
			e.get(&sent, `SELECT sent FROM campaigns WHERE id=$1`, e.campID)
			got := e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id=$1 ORDER BY subscriber_id`, e.campID)
			if sent != len(xs) || !slices.Equal(got, xs) {
				t.Fatalf("send after reschedule: count %d, ledger %v, want %v", sent, got, xs)
			}
		})
	}
}

func TestScheduledPreflightIncludesExpiredSends(t *testing.T) {
	const total, checkpoint, delivered = 40, 10, 30
	for _, tc := range []struct {
		name        string
		saved       int
		decided     int
		deleteUsers bool
		legacyPurge int
	}{
		{name: "stored count covers the expired deliveries", saved: checkpoint},
		{name: "stored count already includes out-of-order deliveries", saved: delivered},
		{name: "sent follow-up decisions survive retention", saved: checkpoint, decided: 4},
		{name: "subscriber API deletes the oldest recipients", saved: checkpoint, deleteUsers: true},
		{name: "legacy purge leaves the checkpoint record", saved: checkpoint, legacyPurge: 9},
		{name: "legacy purge removes through the checkpoint", saved: checkpoint, legacyPurge: 10},
		{name: "legacy purge removes a record beyond the checkpoint", saved: checkpoint, legacyPurge: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := scheduledEnv(t)
			list := e.pfList("single", "")
			var ids []int
			for i := 0; i < total; i++ {
				ids = append(ids, e.pfSub("enabled", "", map[int]string{list: "confirmed"}))
			}
			e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, list)
			e.db.MustExec(`UPDATE campaigns SET status='paused', started_at=NOW()-INTERVAL '13 months',
				to_send=$2, sent=$3, last_subscriber_id=$4 WHERE id=$1`,
				e.campID, total, tc.saved, ids[checkpoint-1])
			purge := checkpoint
			if tc.legacyPurge > 0 {
				purge = tc.legacyPurge
			}
			for i, id := range ids[:delivered] {
				age := "1 day"
				if i < purge {
					age = "13 months"
				}
				e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at) VALUES($1,$2,NOW()-$3::INTERVAL)`,
					e.campID, id, age)
				if i < tc.decided {
					e.db.MustExec(`INSERT INTO axm_campaign_followup_claims(campaign_id,subscriber_id,sent) VALUES($1,$2,TRUE)`, e.campID, id)
				}
			}
			member := e.as(e.superAdmin)
			path := fmt.Sprintf("/api/campaigns/%d", e.campID)
			id := strconv.Itoa(e.campID)
			check := func(mode string, sent, remaining int, minimum bool) {
				t.Helper()
				data := pfRead(t, e, member, path+"/preflight", id)
				var out preflightResponse
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatal(err)
				}
				if out.Mode != mode || out.Sent != sent || out.SentIsMinimum != minimum || out.ToSend != total || out.Remaining != remaining ||
					(mode == "scheduled" && !out.Resumed) {
					t.Fatalf("%s: sent=%d, sent_is_minimum=%t, total=%d, remaining=%d, resumed=%t", out.Mode, out.Sent, out.SentIsMinimum, out.ToSend, out.Remaining, out.Resumed)
				}
			}
			check("resume", delivered, total-delivered, false)
			wantExpired, sent, remaining := purge-tc.decided, delivered, total-delivered
			minimum := tc.legacyPurge > 0
			switch {
			case minimum:
				e.db.MustExec(`DROP TRIGGER axm_count_removed_campaign_sends ON axm_campaign_sends;
					DROP TRIGGER axm_lock_send_campaigns_on_subscriber_delete ON subscribers;
					DROP TABLE axm_campaign_expired_sends;
					ALTER TABLE campaigns DROP COLUMN axm_legacy_send_history`)
				e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE sent_at < NOW()-INTERVAL '12 months'`)
				e.db.MustExec(`INSERT INTO settings (key, value) VALUES ('migrations', '["v6.2.1-axm.17"]')
					ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
				upgrade(e.db, nil, false, true)
				wantExpired, sent = 0, delivered-purge
				// Once a record after the checkpoint is gone, the sender can queue that
				// recipient again. The pending count follows the sender's available evidence.
				remaining += max(0, purge-checkpoint)
			case tc.deleteUsers:
				for _, subID := range ids[:checkpoint] {
					if code, _ := e.call(e.a.DeleteSubscriber, member, http.MethodDelete,
						fmt.Sprintf("/api/subscribers/%d", subID), strconv.Itoa(subID), ""); code != http.StatusOK {
						t.Fatalf("delete subscriber %d: %d", subID, code)
					}
				}
			default:
				if n, err := e.core.PurgeOldCampaignSends(); err != nil || n != checkpoint {
					t.Fatalf("deleted %d expired records: %v", n, err)
				}
				if n, err := e.core.PurgeOldCampaignSends(); err != nil || n != 0 {
					t.Fatalf("second purge deleted %d records: %v", n, err)
				}
			}
			var expired, retained int
			e.get(&expired, `SELECT COALESCE((SELECT sent FROM axm_campaign_expired_sends WHERE campaign_id=$1), 0)`, e.campID)
			e.get(&retained, `SELECT COUNT(*) FROM axm_campaign_sends WHERE campaign_id=$1`, e.campID)
			if expired != wantExpired || retained != delivered-purge {
				t.Fatalf("expired tally %d, retained ledger %d", expired, retained)
			}
			check("resume", sent, remaining, minimum)
			if code, data := e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, path+"/status", id, `{"status":"running"}`); code != http.StatusOK {
				t.Fatalf("reschedule: %d %s", code, data)
			}
			tomorrow := time.Now().Add(24 * time.Hour)
			if code, data := e.save("Edited", []int{list}, &tomorrow, "regular"); code != http.StatusOK {
				t.Fatalf("save: %d %s", code, data)
			}
			check("scheduled", sent, remaining, minimum)
			if minimum {
				// Once tracked removals begin, a surviving delivery moving into the
				// tally cannot make the earlier history complete or lower this count.
				subID := ids[delivered-1]
				if code, data := e.call(e.a.DeleteSubscriber, member, http.MethodDelete,
					fmt.Sprintf("/api/subscribers/%d", subID), strconv.Itoa(subID), ""); code != http.StatusOK {
					t.Fatalf("delete subscriber %d: %d %s", subID, code, data)
				}
				check("scheduled", sent, remaining, true)
			}
			var stored int
			e.get(&stored, `SELECT sent FROM campaigns WHERE id=$1`, e.campID)
			if stored != tc.saved {
				t.Fatalf("read or deletion changed saved progress to %d", stored)
			}
		})
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

func TestScheduledUnscheduleRejectsStaleVersion(t *testing.T) {
	e := scheduledEnv(t)
	member := e.as(e.superAdmin)
	id := strconv.Itoa(e.campID)
	path := "/api/campaigns/" + id
	read := func() preflightResponse {
		t.Helper()
		var result preflightResponse
		if err := json.Unmarshal(pfRead(t, e, member, path+"/preflight", id), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	unschedule := func(version null.Time) int {
		t.Helper()
		body, err := json.Marshal(map[string]any{"status": "draft", "updated_at": version})
		if err != nil {
			t.Fatal(err)
		}
		code, _ := e.call(e.a.UpdateCampaignStatus, member, http.MethodPut, path+"/status", id, string(body))
		return code
	}
	old := read()
	later := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	if code, response := e.save("Other editor's subject", []int{e.aList}, &later, "regular"); code != http.StatusOK {
		t.Fatalf("second editor save: %d %s", code, response)
	}
	if code := unschedule(old.UpdatedAt); code != http.StatusConflict {
		t.Fatalf("outdated unschedule: %d", code)
	}
	fresh := read()
	if fresh.Status != "scheduled" || fresh.Subject != "Other editor's subject" || !fresh.SendAt.Time.Equal(later) {
		t.Fatalf("outdated request changed the campaign: %s %q %v", fresh.Status, fresh.Subject, fresh.SendAt)
	}
	if code := unschedule(fresh.UpdatedAt); code != http.StatusOK {
		t.Fatalf("current unschedule: %d", code)
	}
	if got := read(); got.Status != "draft" || got.Subject != fresh.Subject {
		t.Fatalf("current request: %s %q", got.Status, got.Subject)
	}
}
