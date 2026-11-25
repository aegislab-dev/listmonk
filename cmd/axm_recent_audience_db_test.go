package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

func TestRecentAudienceRescheduledListUsesHolds(t *testing.T) {
	e := newRecentListEnv(t)
	if _, err := e.a.core.SaveRecentRule(context.Background(), e.draft,
		models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll}, 0); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"list_ids":[%d],"campaign_id":%d,"days":14,"scope":"all"}`, e.aList, e.draft)
	want := []int{e.a2}
	initial := e.make(e.admin, body)
	if !sameInts(e.members(initial.List.ID), want) {
		t.Fatalf("initial members: got %v, want %v", e.members(initial.List.ID), want)
	}

	held, err := e.a.core.HoldRecentBatch(context.Background(), e.draft, []int{e.a1, e.a2, e.a5, e.both})
	if err != nil || len(held) != 3 {
		t.Fatalf("initial holds = %v, err = %v", held, err)
	}
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.draft, e.a2)
	e.db.MustExec(`UPDATE campaigns SET status='paused', started_at=NOW(), sent=1, send_at=NOW()+INTERVAL '1 day' WHERE id=$1`, e.draft)
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at=NOW()-INTERVAL '35 days' WHERE campaign_id IN ($1,$2)`, e.sentA, e.sentB)
	if _, err := e.q.UpdateCampaignStatus.Exec(e.draft, models.CampaignStatusRunning); err != nil {
		t.Fatal(err)
	}
	var status string
	e.get(&status, `SELECT status FROM campaigns WHERE id=$1`, e.draft)
	if status != models.CampaignStatusScheduled {
		t.Fatalf("resumed campaign is %s", status)
	}

	// The current window no longer contains any historical exclusions. Decision history still
	// makes the preview and list agree on the same remaining person.
	rescheduled := e.make(e.admin, body)
	if got := e.members(rescheduled.List.ID); !sameInts(got, want) || rescheduled.Counts != initial.Counts {
		t.Fatalf("rescheduled list: members %v, counts %+v; want %v and %+v", got, rescheduled.Counts, want, initial.Counts)
	}

	// A later campaign also reaches the person this campaign already sent to. That person
	// cannot become a recent exclusion when viewing or saving this campaign's audience.
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.sentA, e.a2)
	code, raw := e.call(e.a.PreviewRecentExclusion, e.admin, http.MethodPost, "/api/campaigns/recent-exclude/preview", "", body)
	var preview models.RecentCounts
	if code != http.StatusOK || json.Unmarshal(raw, &preview) != nil || preview.Excluded != 3 || preview.WillSend != 1 {
		t.Fatalf("preview = %d %s", code, raw)
	}
	saved := e.make(e.admin, body)
	if got := e.members(saved.List.ID); !sameInts(got, want) || saved.Counts != initial.Counts {
		t.Fatalf("list after later send: members %v, counts %+v; want %v and %+v", got, saved.Counts, want, initial.Counts)
	}
}

func TestRecentAudienceCountsSurviveDeletionDuringSend(t *testing.T) {
	deleteHeld := func(e *recentListEnv, held []int) {
		e.t.Helper()
		sub := strconv.Itoa(held[0])
		if code, raw := e.call(e.a.DeleteSubscriber, e.admin, http.MethodDelete, "/api/subscribers/"+sub, sub, ""); code != http.StatusOK {
			e.t.Fatalf("delete held subscriber: HTTP %d %s", code, raw)
		}
		wantSame(e.t, "remaining holds", e.held(), held[1:])
	}
	t.Run("uninterrupted", func(t *testing.T) { testRecentAudienceChangeDuringSend(t, recentAudienceChange{}, deleteHeld) })
	t.Run("pause and resume", func(t *testing.T) {
		testRecentAudienceChangeDuringSend(t, recentAudienceChange{pauseAfter: 21}, deleteHeld)
	})
}

func TestRecentAudienceCountsSurviveUnsubscribeDuringSend(t *testing.T) {
	unsubscribeHeld := func(e *recentListEnv, held []int) {
		unsubscribeRecentAudienceSubscriber(e, held)
		wantSame(e.t, "remaining holds", e.held(), held)
	}
	t.Run("static audience", func(t *testing.T) {
		testRecentAudienceChangeDuringSend(t, recentAudienceChange{pauseAfter: 21}, unsubscribeHeld)
	})
	t.Run("overlapping static and dynamic audience", func(t *testing.T) {
		testRecentAudienceChangeDuringSend(t, recentAudienceChange{pauseAfter: 21, dynamic: true}, unsubscribeHeld)
	})
}

func TestRecentAudienceIncludesDeliveredUnsubscribesOnResume(t *testing.T) {
	t.Run("static audience", func(t *testing.T) {
		testRecentAudienceChangeDuringSend(t, recentAudienceChange{pauseAfter: 16, changeDelivered: true}, unsubscribeRecentAudienceSubscriber)
	})
	t.Run("overlapping static and dynamic audience", func(t *testing.T) {
		testRecentAudienceChangeDuringSend(t, recentAudienceChange{pauseAfter: 16, dynamic: true, changeDelivered: true}, unsubscribeRecentAudienceSubscriber)
	})
}

func unsubscribeRecentAudienceSubscriber(e *recentListEnv, subs []int) {
	e.t.Helper()
	body := fmt.Sprintf(`{"ids":[%d],"action":"unsubscribe","target_list_ids":[%d]}`, subs[0], e.listID)
	if code, raw := e.call(e.a.ManageSubscriberLists, e.admin, http.MethodPut, "/api/subscribers/lists", "", body); code != http.StatusOK {
		e.t.Fatalf("unsubscribe subscriber: HTTP %d %s", code, raw)
	}
	var status string
	e.get(&status, `SELECT status FROM subscriber_lists WHERE subscriber_id=$1 AND list_id=$2`, subs[0], e.listID)
	if status != "unsubscribed" {
		e.t.Fatalf("subscription is %s, want unsubscribed", status)
	}
}

func TestRecentAudienceRescansWithoutLedgerTable(t *testing.T) {
	e := newSendsEnv(t, 2)
	e.rsRule(models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll})
	e.db.MustExec(`UPDATE campaigns SET started_at=NOW() WHERE id=$1;`, e.campID)
	e.db.MustExec(`DROP TABLE axm_campaign_sends`)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	if _, _, target := e.campaign(); target != 2 {
		t.Fatalf("target=%d, want 2 eligible subscribers", target)
	}
}

type recentAudiencePauseStore struct {
	*store
	lastID int
	resume <-chan struct{}
}

func (s *recentAudiencePauseStore) NextSubscribers(campaignID, lastID, limit int) ([]models.Subscriber, error) {
	if lastID == s.lastID {
		<-s.resume
	}
	return s.store.NextSubscribers(campaignID, lastID, limit)
}

type recentAudienceChange struct {
	pauseAfter      int
	dynamic         bool
	changeDelivered bool
}

func testRecentAudienceChangeDuringSend(t *testing.T, scenario recentAudienceChange, change func(*recentListEnv, []int)) {
	e := newRecentListEnv(t)
	audience := make([]int, 40)
	priorSends := make(map[int]time.Duration, 12)
	for i := range audience {
		audience[i] = e.addSub("enabled", "confirmed")
		if i < 12 {
			priorSends[audience[i]] = 3 * time.Hour
		}
	}
	if scenario.dynamic {
		e.db.MustExec(`UPDATE subscribers SET attribs = '{"plan":"pro"}' WHERE id = ANY($1)`, pq.Array(audience))
		list := e.pfList("single", dynFilter(e.listID))
		e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'Synthetic dynamic')`, e.campID, list)
	}
	e.rsPrior(models.CampaignTypeRegular, e.listID, priorSends)
	e.rsRule(models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll})

	// Wait at the messenger so the sender has recorded all 12 holds and cannot yet finish.
	allow := make(chan struct{})
	msgr, logs := &gateMessenger{allow: allow}, &syncBuffer{}
	resume := make(chan struct{})
	var readOnce sync.Once
	resumeReads := func() { readOnce.Do(func() { close(resume) }) }
	var st manager.Store = e.store
	batchSize := 33
	if scenario.pauseAfter > 0 {
		// Stop the second read after the first batch's 12 exclusions and initial deliveries.
		batchSize = 12 + scenario.pauseAfter
		st = &recentAudiencePauseStore{store: e.store, lastID: audience[batchSize-1], resume: resume}
	}
	m := e.startManagerWith(st, msgr, logs, func(c *manager.Config) { c.BatchSize = batchSize })
	var once sync.Once
	release := func() { once.Do(func() { close(allow) }) }
	t.Cleanup(m.Close)
	t.Cleanup(resumeReads)
	t.Cleanup(release)
	waitFor(t, "12 holds and a message waiting to send", func() bool {
		return len(e.held()) == 12 && msgr.pushes.Load() > 0
	})
	assertCounts := func(wantStatus string) {
		t.Helper()
		status, sent, target := e.campaign()
		path := fmt.Sprintf("/api/campaigns/%d/recent-exclude", e.campID)
		code, raw := e.call(e.a.GetRecentRule, e.admin, http.MethodGet, path, strconv.Itoa(e.campID), "")
		var rule models.RecentRule
		if code != http.StatusOK || json.Unmarshal(raw, &rule) != nil {
			t.Fatalf("get recent rule: HTTP %d %s", code, raw)
		}
		if status != wantStatus || target != 40 || rule.Held != 12 {
			t.Fatalf("status=%s sent=%d target=%d held=%d, want %s with 40 target and 12 held", status, sent, target, rule.Held, wantStatus)
		}
	}
	assertCounts(models.CampaignStatusRunning)
	if !scenario.changeDelivered {
		change(e, audience[:12])
		if status, _, _ := e.campaign(); status != models.CampaignStatusRunning {
			t.Fatalf("status after changing a held subscriber: %s, want running", status)
		}
	}
	release()
	if sent := scenario.pauseAfter; sent > 0 {
		waitFor(t, "first deliveries and ledger entries", func() bool {
			return len(msgr.sentIDs()) == sent && len(e.recorded()) == sent
		})
		if scenario.changeDelivered {
			wantSame(t, "first deliveries in the ledger", e.recorded(), audience[12:12+sent])
			change(e, audience[12:12+sent])
		}
		if status, _, _ := e.campaign(); status != models.CampaignStatusRunning {
			t.Fatalf("status before pause: %s, want running", status)
		}
		e.setStatus(models.CampaignStatusPaused)
		m.StopCampaign(e.campID)
		resumeReads()
		waitFor(t, "the pause to take effect", func() bool { return !m.HasRunningCampaigns() })
		e.waitCampaign(models.CampaignStatusPaused, sent)
		_, aud, err := e.core.GetPreflightAudience(context.Background(), e.campID, e.checkpoint(), true)
		if want := 28 - sent; err != nil || aud.Remaining != want {
			t.Fatalf("preflight at resume: remaining=%d, want %d, err=%v", aud.Remaining, want, err)
		}
		e.setStatus(models.CampaignStatusRunning)
	}
	e.waitCampaign(models.CampaignStatusFinished, 28)
	waitFor(t, "the send ledger to be written", func() bool { return len(e.recorded()) == 28 })
	wantSame(t, "delivered", msgr.sentIDs(), audience[12:])
	wantSame(t, "send ledger", e.recorded(), audience[12:])
	assertCounts(models.CampaignStatusFinished)
}

func TestRecentAudienceFinishedCoverage(t *testing.T) {
	e := newRecentListEnv(t)
	e.rsRule(models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll})
	e.setStatus(models.CampaignStatusFinished)
	first := time.Now().UTC().Truncate(time.Second)
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at=$1`, first)
	path := fmt.Sprintf("/api/campaigns/%d/recent-exclude", e.campID)
	call := func(url string) json.RawMessage {
		t.Helper()
		code, raw := e.call(e.a.GetRecentRule, e.admin, http.MethodGet, url, strconv.Itoa(e.campID), "")
		if code != http.StatusOK {
			t.Fatalf("get recent rule: HTTP %d %s", code, raw)
		}
		return raw
	}
	var ordinary map[string]json.RawMessage
	if err := json.Unmarshal(call(path), &ordinary); err != nil || ordinary["records_from"] != nil {
		t.Fatalf("rule with no coverage request: %s, err=%v", ordinary["records_from"], err)
	}
	path += "?include_coverage=true"
	var coverage struct {
		RecordsFrom time.Time `json:"records_from"`
	}
	if err := json.Unmarshal(call(path), &coverage); err != nil || !coverage.RecordsFrom.Equal(first) {
		t.Fatalf("records_from=%v, want %v, err=%v", coverage.RecordsFrom, first, err)
	}
	e.db.MustExec(`DELETE FROM axm_campaign_sends`)
	var empty map[string]json.RawMessage
	if err := json.Unmarshal(call(path), &empty); err != nil || string(empty["records_from"]) != "null" {
		t.Fatalf("empty ledger records_from=%s, err=%v", empty["records_from"], err)
	}
}

func TestRecentAudienceFinishedCountsSurviveSubscriberDeletion(t *testing.T) {
	e := newRecentListEnv(t)
	var audience []int
	priorSends := make(map[int]time.Duration)
	for i := 0; i < 40; i++ {
		id := e.addSub("enabled", "confirmed")
		audience = append(audience, id)
		if i < 12 {
			priorSends[id] = 3 * time.Hour
		}
	}
	e.rsPrior(models.CampaignTypeRegular, e.listID, priorSends)
	e.rsRule(models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll})
	var failingEmail string
	e.get(&failingEmail, `SELECT email FROM subscribers WHERE id=$1`, audience[39])
	body := fmt.Sprintf(`<p>{{ if eq .Subscriber.Email %q }}{{ index "a" 9 }}{{ else }}Hello{{ end }}</p>`, failingEmail)
	e.db.MustExec(`UPDATE campaigns SET body=$2 WHERE id=$1`, e.campID, body)
	wantSame(t, "delivered", e.rsRun(t, 5), audience[12:39])

	assertCounts := func() {
		t.Helper()
		status, sent, target := e.campaign()
		path := fmt.Sprintf("/api/campaigns/%d/recent-exclude", e.campID)
		code, raw := e.call(e.a.GetRecentRule, e.admin, http.MethodGet, path, strconv.Itoa(e.campID), "")
		var rule models.RecentRule
		if code != http.StatusOK || json.Unmarshal(raw, &rule) != nil {
			t.Fatalf("get recent rule: HTTP %d %s", code, raw)
		}
		if status != models.CampaignStatusFinished || sent != 27 || target != 40 || rule.Held != 12 {
			t.Fatalf("status=%s sent=%d target=%d held=%d, want finished/27/40/12", status, sent, target, rule.Held)
		}
	}
	assertCounts()
	sub := strconv.Itoa(audience[0])
	if code, raw := e.call(e.a.DeleteSubscriber, e.admin, http.MethodDelete, "/api/subscribers/"+sub, sub, ""); code != http.StatusOK {
		t.Fatalf("delete held subscriber: HTTP %d %s", code, raw)
	}
	wantSame(t, "remaining holds", e.held(), audience[1:12])
	wantSame(t, "send ledger", e.recorded(), audience[12:39])
	assertCounts()
}
