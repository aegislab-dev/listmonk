package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
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
