package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// LST-65: a campaign with a recent-send rule leaves out, when the sender reads each batch, the
// people who got a campaign of the rule's scope within the rule's days. These tests run the real
// sender (manager and store) and compare who it sends to with the rule.

// rsPrior makes a finished campaign on the lists of listIDs with a send record for each person
// at the given age.
func (e *sendsEnv) rsPrior(typ string, listID int, ago map[int]time.Duration) int {
	e.t.Helper()
	id := e.pfCampaign(typ, "finished", listID)
	for sub, d := range ago {
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at) VALUES($1,$2,NOW()-make_interval(secs => $3))`,
			id, sub, d.Seconds())
	}
	return id
}

// rsRule saves the rule of the running campaign (it is a draft while the rule is saved).
func (e *sendsEnv) rsRule(rule models.RecentRule) {
	e.t.Helper()
	e.setStatus(models.CampaignStatusDraft)
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, rule, 0); err != nil {
		e.t.Fatal(err)
	}
	e.setStatus(models.CampaignStatusRunning)
}

func hours(d int) time.Duration { return time.Duration(d) * 24 * time.Hour }

// rsRun starts the sender and waits until the campaign is finished. It returns who got it.
func (e *sendsEnv) rsRun(t *testing.T, batch int) []int {
	t.Helper()
	msgr, logs := &gateMessenger{}, &syncBuffer{}
	m := e.startManagerWith(e.store, msgr, logs, func(c *manager.Config) { c.BatchSize = batch })
	t.Cleanup(m.Close)
	waitFor(t, "the campaign to finish and its records to be written", func() bool {
		s, _, _ := e.campaign()
		return s == models.CampaignStatusFinished && strings.Contains(logs.String(), "send records:")
	})
	got := msgr.sentIDs()
	noRepeats(t, slices.Clone(got))
	return got
}

func (e *sendsEnv) held() []int {
	e.t.Helper()
	return e.ids(`SELECT subscriber_id FROM axm_campaign_recent_holds WHERE campaign_id=$1 ORDER BY subscriber_id`, e.campID)
}

// A campaign with the rule does not send to the people who got a campaign of the rule's scope
// within its days, at the day boundaries, for each scope; the people it sends to are the ones the
// preview and the new-list maker (axm_recent_exclusion) keep. The campaign's own records, a
// campaign of another team and an opt-in campaign do not count where the scope does not take them.
func TestRecentRuleLeavesOutRecentRecipientsOnSend(t *testing.T) {
	cases := []struct {
		scope string
		days  int
		out   []int // positions in e.eligible
	}{
		{models.RecentScopeCampaigns, 7, []int{0}},
		{models.RecentScopeCampaigns, 14, []int{0, 1, 2}},
		{models.RecentScopeCampaigns, 30, []int{0, 1, 2, 3, 4}},
		{models.RecentScopeTeam, 14, []int{0, 1, 2}},
		{models.RecentScopeTeam, 30, []int{0, 1, 2, 3, 4}},
		{models.RecentScopeAll, 7, []int{0, 6}},
		{models.RecentScopeAll, 14, []int{0, 1, 2, 6}},
		{models.RecentScopeAll, 30, []int{0, 1, 2, 3, 4, 6}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %d days", tc.scope, tc.days), func(t *testing.T) {
			e := newSendsEnv(t, 24)
			e.db.MustExec(`UPDATE lists SET tags='{팀:가}' WHERE id=$1`, e.listID)
			other := e.pfList("single", "")
			e.db.MustExec(`UPDATE lists SET tags='{팀:나}' WHERE id=$1`, other)
			e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) SELECT id,$1,'confirmed' FROM subscribers`, other)

			same := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{
				e.eligible[0]: hours(6), e.eligible[1]: hours(8), e.eligible[2]: hours(13) + time.Hour,
				e.eligible[3]: hours(15), e.eligible[4]: hours(29), e.eligible[5]: hours(31),
			})
			e.rsPrior(models.CampaignTypeRegular, other, map[int]time.Duration{e.eligible[6]: hours(1)})
			e.rsPrior(models.CampaignTypeOptin, e.listID, map[int]time.Duration{e.eligible[7]: hours(1)})

			rule := models.RecentRule{Enabled: true, Days: tc.days, Scope: tc.scope}
			if tc.scope == models.RecentScopeCampaigns {
				rule.CampaignIDs = pq.Int64Array{int64(same)}
			}
			e.rsRule(rule)

			want := e.eligible
			for i := len(tc.out) - 1; i >= 0; i-- {
				want = slices.Delete(slices.Clone(want), tc.out[i], tc.out[i]+1)
			}
			// What the preview keeps is what is sent (the same people, by the same function).
			var kept []int
			e.db.Select(&kept, `SELECT subscriber_id FROM axm_recent_exclusion($1, $2, $3, $4, $5) WHERE NOT excluded ORDER BY 1`,
				pq.Array([]int{e.listID}), tc.days, tc.scope, pq.Array([]int{same}), e.campID)
			wantSame(t, "the preview keeps", kept, want)

			got := e.rsRun(t, 5)
			wantSame(t, "delivered", got, want)
			wantSame(t, "recorded", e.recorded(), want)
			var wantHeld []int
			for _, p := range tc.out {
				wantHeld = append(wantHeld, e.eligible[p])
			}
			wantSame(t, "left out", e.held(), wantHeld)
		})
	}
}

// A campaign without a rule is sent as before, whatever others sent.
func TestRecentRuleOffSendsToEveryone(t *testing.T) {
	e := newSendsEnv(t, 12)
	e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[0]: hours(1), e.eligible[1]: hours(2)})
	wantSame(t, "delivered", e.rsRun(t, 5), e.eligible)
	if n := e.count(`SELECT COUNT(*) FROM axm_campaign_recent_holds`); n != 0 {
		t.Fatalf("%d held", n)
	}
}

func TestRecentRuleCapsOnlyItsOwnCandidateBatches(t *testing.T) {
	e := newSendsEnv(t, 1003)
	prior := e.rsPrior(models.CampaignTypeRegular, e.listID, nil)
	e.rsRule(models.RecentRule{
		Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)},
	})
	ordinary := e.pfCampaign(models.CampaignTypeRegular, models.CampaignStatusRunning, e.listID)
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	read := func(campID, limit int, want []int) {
		t.Helper()
		batch, err := e.store.NextSubscribers(campID, 0, limit)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int, 0, len(batch))
		for _, sub := range batch {
			ids = append(ids, sub.ID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("campaign %d, limit %d: got %d IDs, want %d in the requested range", campID, limit, len(ids), len(want))
		}
	}
	record := func(ids []int) {
		t.Helper()
		e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id)
			SELECT $1, unnest($2::INT[])`, prior, pq.Array(ids))
	}

	read(ordinary, 1000, e.eligible[:1000])
	read(e.campID, 1000, e.eligible[:500])
	read(e.campID, 300, e.eligible[:300])

	// A recipient just beyond the first 500 must not be decided until the next query.
	record(e.eligible[499:501])
	read(e.campID, 1000, e.eligible[:499])
	wantSame(t, "held in the first batch", e.held(), e.eligible[499:500])

	// When all 500 are held, the next read still applies the cap and advances by ID.
	record(e.eligible[:499])
	read(e.campID, 1000, e.eligible[501:1000])
	wantSame(t, "held after reading past the first batch", e.held(), e.eligible[:501])
	read(ordinary, 1000, e.eligible[:1000])

	// The manager is configured for 1,000. It must keep reading after a shorter batch.
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, ordinary)
	wantSame(t, "delivered across capped batches", e.rsRun(t, 1000), e.eligible[501:])
	wantSame(t, "recorded across capped batches", e.recorded(), e.eligible[501:])
}

func (e *sendsEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	e.get(&n, query, args...)
	return n
}

// A batch of only people left out is read past: the campaign goes on to the rest and ends. With
// everybody left out it ends having sent nothing.
func TestRecentRuleWholeBatchesLeftOut(t *testing.T) {
	e := newSendsEnv(t, 12)
	ago := map[int]time.Duration{}
	for _, id := range e.eligible[:5] {
		ago[id] = hours(1)
	}
	prior := e.rsPrior(models.CampaignTypeRegular, e.listID, ago)
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
	wantSame(t, "delivered", e.rsRun(t, 2), e.eligible[5:])
	wantSame(t, "left out", e.held(), e.eligible[:5])

	e = newSendsEnv(t, 6)
	ago = map[int]time.Duration{}
	for _, id := range e.eligible {
		ago[id] = hours(2)
	}
	prior = e.rsPrior(models.CampaignTypeRegular, e.listID, ago)
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
	wantSame(t, "delivered", e.rsRun(t, 2), nil)
	wantSame(t, "left out", e.held(), e.eligible)
	if s, sent, _ := e.campaign(); s != "finished" || sent != 0 {
		t.Fatalf("%s sent %d", s, sent)
	}
}

// Who the campaign left out stays out: when the send records expire or the days pass while it is
// paused, or the sender starts again. The rule stays locked after the first start, including when
// a paused campaign is scheduled to resume later.
func TestRecentRuleKeepsWhoWasLeftOut(t *testing.T) {
	e := newSendsEnv(t, 10)
	prior := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[1]: hours(6), e.eligible[4]: hours(6)})
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
	if _, err := e.store.NextCampaigns([]int64{}); err != nil {
		t.Fatal(err)
	}
	read := func() []int {
		t.Helper()
		var ids []int
		last := 0
		for {
			batch, err := e.store.NextSubscribers(e.campID, last, 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(batch) == 0 {
				return ids
			}
			for _, s := range batch {
				ids = append(ids, s.ID)
				last = s.ID
			}
		}
	}
	want := minus(e.eligible, []int{e.eligible[1], e.eligible[4]})
	wantSame(t, "first read", read(), want)

	// A long pause: the days pass and the old send records are gone.
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '40 days' WHERE campaign_id=$1`, prior)
	wantSame(t, "after the days passed", read(), want)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id=$1`, prior)
	wantSame(t, "after the records are gone", read(), want)

	// A person who was not read yet and now has no record of the prior campaign is sent to.
	// A pause does not open the rule: it cannot be turned off from the page or the API.
	e.setStatus(models.CampaignStatusPaused)
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, models.RecentRule{}, 0); err == nil {
		t.Fatal("the rule of a paused campaign was changed")
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '1 hour' WHERE id=$1`, e.campID)
	if _, err := e.q.UpdateCampaignStatus.Exec(e.campID, models.CampaignStatusRunning); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := e.campaign(); s != models.CampaignStatusScheduled {
		t.Fatalf("campaign status = %s, want scheduled", s)
	}
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, models.RecentRule{}, 0); err == nil {
		t.Fatal("the rule of a rescheduled campaign was changed")
	}
}

// A person this campaign sent to is not left out afterwards, because another campaign sent to
// them since (a restart reads the batch again).
func TestRecentRuleDoesNotCountOwnSends(t *testing.T) {
	e := newSendsEnv(t, 4)
	other := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[0]: hours(1)})
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeAll})
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2),($1,$3)`, e.campID, e.eligible[0], e.eligible[1])
	var got []int
	e.db.Select(&got, `SELECT subscriber_id FROM axm_recent_received($1, $2) ORDER BY 1`, e.campID, pq.Array(e.eligible))
	wantSame(t, "left out", got, nil)
	_ = other
}

// The rule is decided before a batch is sent: if it cannot be decided, nothing of the batch is
// sent, and the campaign goes on once it can.
func TestRecentRuleFailureSendsNothing(t *testing.T) {
	e := newSendsEnv(t, 8)
	prior := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[2]: hours(1)})
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
	e.db.MustExec(`DROP FUNCTION axm_recent_received(INT, INT[])`)

	msgr, logs := &gateMessenger{}, &syncBuffer{}
	m := e.startManagerWith(e.store, msgr, logs, func(c *manager.Config) { c.BatchSize = 3 })
	t.Cleanup(m.Close)
	waitFor(t, "the read to fail", func() bool { return strings.Contains(logs.String(), "error fetching campaign subscribers") })
	time.Sleep(200 * time.Millisecond)
	if got := msgr.sentIDs(); len(got) != 0 {
		t.Fatalf("sent to %v while the rule could not be decided", got)
	}

	if err := migrations.V6_2_1_axm_21(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the campaign to finish", func() bool { s, _, _ := e.campaign(); return s == models.CampaignStatusFinished })
	wantSame(t, "delivered", msgr.sentIDs(), minus(e.eligible, []int{e.eligible[2]}))
}

// The sender with a rule, paused and resumed, and killed and restarted (the same scenarios as the
// campaigns without a rule): nobody left out gets it, everybody else gets it exactly once.
func TestRecentRulePauseResumeAndRestart(t *testing.T) {
	setup := func(t *testing.T, n int) (*sendsEnv, []int, []int) {
		e := newSendsEnv(t, n)
		ago := map[int]time.Duration{}
		var out []int
		for i, id := range e.eligible {
			if i%3 == 0 {
				ago[id] = hours(2)
				out = append(out, id)
			}
		}
		prior := e.rsPrior(models.CampaignTypeRegular, e.listID, ago)
		e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
		return e, out, minus(e.eligible, out)
	}

	t.Run("pause during first read and resume twice", func(t *testing.T) {
		e, out, want := setup(t, 24)
		msgr := &gateMessenger{}
		// Block the recent exclusion query after the sender has read eligible subscribers but before
		// the first batch is passed to the workers.
		e.setStatus(models.CampaignStatusPaused)
		m := e.startManager(e.store, msgr, io.Discard)
		t.Cleanup(m.Close)
		for i := 0; i < 2; i++ {
			readLock := e.db.MustBegin()
			defer readLock.Rollback()
			readLock.MustExec(`LOCK TABLE axm_campaign_recent_holds IN ACCESS EXCLUSIVE MODE`)
			e.setStatus(models.CampaignStatusRunning)
			waitFor(t, "first batch waiting for holds table", func() bool {
				var waiting bool
				e.get(&waiting, `SELECT EXISTS (SELECT 1 FROM pg_locks
					WHERE relation='axm_campaign_recent_holds'::regclass AND NOT granted)`)
				return waiting
			})
			e.setStatus(models.CampaignStatusPaused)
			m.StopCampaign(e.campID)
			if err := readLock.Rollback(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the pause to take effect", func() bool { return !m.HasRunningCampaigns() })
			if got := msgr.sentIDs(); len(got) != 0 {
				t.Fatalf("delivered %v before pause %d", got, i+1)
			}
		}
		e.setStatus(models.CampaignStatusRunning)
		e.waitCampaign("finished", len(want))
		got := msgr.sentIDs()
		noRepeats(t, slices.Clone(got))
		wantSame(t, "delivered", got, want)
		wantSame(t, "held", e.held(), out)
	})

	t.Run("pause and resume", func(t *testing.T) {
		e, out, want := setup(t, 45)
		msgr := &gateMessenger{allow: make(chan struct{})}
		logs := &syncBuffer{}
		m := e.startManager(e.store, msgr, logs)
		t.Cleanup(m.Close)
		for i := 0; i < 9; i++ {
			select {
			case msgr.allow <- struct{}{}:
			case <-time.After(20 * time.Second):
				t.Fatalf("the campaign did not start sending:\n%s", logs.String())
			}
		}
		waitFor(t, "9 deliveries", func() bool { return len(msgr.sentIDs()) == 9 })
		e.setStatus(models.CampaignStatusPaused)
		m.StopCampaign(e.campID)
		close(msgr.allow)
		waitFor(t, "the pause to take effect", func() bool { return !m.HasRunningCampaigns() })
		e.waitCampaign("paused", 9)
		// Paused, the days pass for the people already left out (the ones not read yet still count).
		e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '60 days'
			WHERE subscriber_id IN (SELECT subscriber_id FROM axm_campaign_recent_holds WHERE campaign_id=$1)`, e.campID)
		e.setStatus(models.CampaignStatusRunning)
		waitFor(t, "the campaign to finish", func() bool { s, _, _ := e.campaign(); return s == models.CampaignStatusFinished })
		got := msgr.sentIDs()
		noRepeats(t, slices.Clone(got))
		wantSame(t, "delivered", got, want)
		if left := e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id=$1 AND subscriber_id = ANY($2)`, e.campID, pq.Array(out)); len(left) != 0 {
			t.Fatalf("people left out were sent to: %v", left)
		}
	})

	t.Run("killed and restarted", func(t *testing.T) {
		e, out, want := setup(t, 45)
		first := &gateMessenger{hangAfter: 11}
		dying := e.dyingInstance(first)
		e.startManager(dying, first, io.Discard)
		waitFor(t, "11 deliveries", func() bool { return len(first.sentIDs()) == 11 })
		waitFor(t, "their records", func() bool { return len(e.recorded()) == 11 })
		dying.kill()
		before := first.sentIDs()

		second := &gateMessenger{}
		logs := &syncBuffer{}
		resumed := e.startManagerWith(e.newStore(), second, logs, nil)
		t.Cleanup(resumed.Close)
		waitFor(t, "the campaign to finish", func() bool { s, _, _ := e.campaign(); return s == models.CampaignStatusFinished })
		delivered := append(slices.Clone(before), second.sentIDs()...)
		noRepeats(t, delivered)
		wantSame(t, "delivered over both instances", delivered, want)
		wantSame(t, "left out", e.held(), out)
	})

	t.Run("closed and restarted", func(t *testing.T) {
		e, _, want := setup(t, 45)
		first := &gateMessenger{allow: make(chan struct{})}
		m := e.startManager(e.store, first, io.Discard)
		for i := 0; i < 7; i++ {
			first.allow <- struct{}{}
		}
		waitFor(t, "7 deliveries", func() bool { return len(first.sentIDs()) == 7 })
		close(first.allow)
		m.Close()
		before := first.sentIDs()
		second := &gateMessenger{}
		resumed := e.startManagerWith(e.newStore(), second, io.Discard, nil)
		t.Cleanup(resumed.Close)
		waitFor(t, "the campaign to finish", func() bool { s, _, _ := e.campaign(); return s == models.CampaignStatusFinished })
		delivered := append(slices.Clone(before), second.sentIDs()...)
		noRepeats(t, delivered)
		wantSame(t, "delivered over both instances", delivered, want)
	})
}

// A follow-up campaign does not take the rule: it is refused when saved, and a row that is there
// leaves out nobody (the follow-up's own judgement of each batch stays the only one).
func TestRecentRuleNotForFollowups(t *testing.T) {
	e := newSendsEnv(t, 4)
	parent := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[0]: hours(1)})
	e.setStatus(models.CampaignStatusDraft)
	e.db.MustExec(`INSERT INTO axm_campaign_followups(campaign_id,parent_campaign_id,list_uuid,condition) SELECT $1,$2,uuid,'{}' FROM lists WHERE id=$3`,
		e.campID, parent, e.listID)
	_, err := e.core.SaveRecentRule(context.Background(), e.campID, models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeAll}, 0)
	if he, ok := err.(interface{ Error() string }); !ok || err == nil || !strings.Contains(he.Error(), "400") {
		t.Fatalf("saving a rule on a follow-up: %v", err)
	}
	// Turning a rule off is fine, and a row that exists anyway does nothing.
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, models.RecentRule{}, 0); err != nil {
		t.Fatal(err)
	}
	e.db.MustExec(`INSERT INTO axm_campaign_recent_rules(campaign_id,days,scope) VALUES($1,7,'all')`, e.campID)
	var got []int
	e.db.Select(&got, `SELECT subscriber_id FROM axm_recent_received($1, $2)`, e.campID, pq.Array(e.eligible))
	wantSame(t, "left out", got, nil)
}

// Saving a rule changes the campaign's updated_at: a send window opened before is stale.
func TestRecentRuleSaveTouchesCampaign(t *testing.T) {
	e := newSendsEnv(t, 1)
	e.setStatus(models.CampaignStatusDraft)
	var before, after time.Time
	e.get(&before, `SELECT updated_at FROM campaigns WHERE id=$1`, e.campID)
	time.Sleep(20 * time.Millisecond)
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeAll}, 0); err != nil {
		t.Fatal(err)
	}
	e.get(&after, `SELECT updated_at FROM campaigns WHERE id=$1`, e.campID)
	if !after.After(before) {
		t.Fatalf("updated_at %v did not move from %v", after, before)
	}
	_ = http.StatusOK
}

// The send window counts what the sender leaves out: the audience count, the people still to be
// sent when resuming and the sample the mail is rendered for agree with who is really sent to.
func TestPreflightRecentMatchesSend(t *testing.T) {
	e := newSendsEnv(t, 0)
	ctx := context.Background()
	e.db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, e.campID)
	l := e.pfList("single", "")
	var subs []int
	for i := 0; i < 12; i++ {
		subs = append(subs, e.pfSub("enabled", "", map[int]string{l: "confirmed"}))
	}
	prior := e.rsPrior(models.CampaignTypeRegular, l, map[int]time.Duration{subs[0]: hours(2), subs[1]: hours(3), subs[8]: hours(1), subs[9]: hours(40)})
	camp := e.pfCampaign("regular", "draft", l)
	if _, err := e.core.SaveRecentRule(ctx, camp, models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}}, 0); err != nil {
		t.Fatal(err)
	}

	sample := e.core.GetPreflightSample(ctx, camp, 3, 0, false)
	var sampleIDs []int
	for _, s := range sample {
		sampleIDs = append(sampleIDs, s.ID)
	}
	wantSame(t, "the sample leaves them out", sampleIDs, []int{subs[2], subs[3], subs[4]})

	_, aud, err := e.core.GetPreflightAudience(ctx, camp, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if aud.Total != 12 || aud.Recent != 3 {
		t.Fatalf("total %d recent %d", aud.Total, aud.Recent)
	}

	// The sender reads the first part (the checkpoint is after the 6th), is paused, and the days
	// pass for the people left out meanwhile: they stay out.
	ids, _ := e.reach(camp, 0)
	wantSame(t, "the sender sends", ids, minus(subs, []int{subs[0], subs[1], subs[8]}))
	if len(ids) != aud.Total-aud.Recent {
		t.Fatalf("the window says %d, the sender sends %d", aud.Total-aud.Recent, len(ids))
	}
	e.db.MustExec(`UPDATE campaigns SET status='paused', last_subscriber_id=$2, to_send=12, sent=3 WHERE id=$1`, camp, subs[5])
	for _, id := range subs[2:6] {
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, camp, id)
	}
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '90 days' WHERE campaign_id=$1`, prior)
	_, aud, err = e.core.GetPreflightAudience(ctx, camp, subs[5], true)
	if err != nil {
		t.Fatal(err)
	}
	// Still to be sent: 6..11 less subs[8] (left out earlier); subs[9] had no record in the days.
	if aud.Remaining != 5 || aud.Recent != 3 {
		t.Fatalf("resume: remaining %d recent %d", aud.Remaining, aud.Recent)
	}
	ids, _ = e.reach(camp, subs[5])
	fresh := 0
	for _, id := range ids {
		if !slices.Contains(subs[2:6], id) {
			fresh++
		}
	}
	if fresh != aud.Remaining {
		t.Fatalf("remaining %d, the sender sends %d", aud.Remaining, fresh)
	}

	// Someone held before has unsubscribed, and a different person now matches the rule.
	e.db.MustExec(`UPDATE subscriber_lists SET status='unsubscribed' WHERE list_id=$1 AND subscriber_id=$2`, l, subs[8])
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES($1,$2)`, prior, subs[10])
	_, aud, err = e.core.GetPreflightAudience(ctx, camp, subs[5], true)
	if err != nil || aud.RecentFresh != 1 || aud.Remaining != 4 {
		t.Fatalf("resume with new exclusion: %+v: %v", aud, err)
	}
}

// The migration's SQL is the block of schema.sql, runs twice without harm, and a plain install
// starts without holds.
func TestRecentHoldsMigration(t *testing.T) {
	root := repoRoot(t)
	schema, err := os.ReadFile(filepath.Join(root, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.21.go"))
	if err != nil {
		t.Fatal(err)
	}
	const start = "-- AXM recent-send exclusion at send time (LST-65). Keep this block identical to V6_2_1_axm_21."
	const end = "-- End of AXM recent-send exclusion at send time."
	_, tail, ok := strings.Cut(string(schema), start)
	block, _, endOK := strings.Cut(tail, end)
	if !ok || !endOK || !strings.Contains(string(migration), "`"+start+block+end+"`") {
		t.Fatal("schema and migration SQL differ")
	}

	e := newSendsEnv(t, 2)
	e.db.MustExec(`DROP FUNCTION axm_recent_received(INT, INT[]); DROP TABLE axm_campaign_recent_holds`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_21(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.db.MustExec(`INSERT INTO axm_campaign_recent_holds(campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, e.eligible[0])
	if n := e.count(`SELECT COUNT(*) FROM pg_constraint WHERE conrelid = 'axm_campaign_recent_holds'::regclass AND contype = 'f'`); n != 2 {
		t.Fatalf("%d foreign keys, want 2", n)
	}
	e.reinstall()
	if n := e.count(`SELECT COUNT(*) FROM axm_campaign_recent_holds`); n != 0 {
		t.Fatal("a reinstall kept holds")
	}
}

// The holds go with the campaign and with the person.
func TestRecentHoldsGoWithCampaignAndSubscriber(t *testing.T) {
	e := newSendsEnv(t, 3)
	other, _ := e.addCampaign("draft")
	for _, c := range []int{e.campID, other} {
		for _, s := range e.eligible {
			e.db.MustExec(`INSERT INTO axm_campaign_recent_holds(campaign_id, subscriber_id) VALUES ($1, $2)`, c, s)
		}
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, e.eligible[0])
	if n := e.count(`SELECT COUNT(*) FROM axm_campaign_recent_holds`); n != 4 {
		t.Fatalf("%d holds after a subscriber went", n)
	}
	e.db.MustExec(`DELETE FROM campaigns WHERE id=$1`, other)
	if n := e.count(`SELECT COUNT(*) FROM axm_campaign_recent_holds`); n != 2 {
		t.Fatalf("%d holds after a campaign went", n)
	}
}

func TestRecentHoldsFinishedCountMigration(t *testing.T) {
	e := newSendsEnv(t, 2)
	e.db.MustExec(`DROP TRIGGER axm_recent_holds_removed ON axm_campaign_recent_holds;
		DROP FUNCTION axm_count_removed_recent_holds();
		DROP TRIGGER axm_recent_holds_finished ON campaigns;
		DROP FUNCTION axm_snapshot_recent_holds();
		ALTER TABLE axm_campaign_recent_rules DROP COLUMN finished_held_count, DROP COLUMN removed_held_count;`)
	rule := models.RecentRule{Enabled: true, Days: 14, Scope: models.RecentScopeAll}
	e.rsRule(rule)
	active, _ := e.addCampaign("draft")
	if _, err := e.core.SaveRecentRule(context.Background(), active, rule, 0); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{e.campID, active} {
		e.db.MustExec(`INSERT INTO axm_campaign_recent_holds (campaign_id, subscriber_id) VALUES ($1, $2), ($1, $3)`,
			id, e.eligible[0], e.eligible[1])
	}
	e.setStatus(models.CampaignStatusFinished)

	migrate := func() {
		t.Helper()
		if err := migrations.V6_2_1_axm_22(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertHeld := func(id, want int) {
		t.Helper()
		if got, err := e.core.RecentHeldCount(id); err != nil || got != want {
			t.Fatalf("campaign %d: held %d, want %d, err %v", id, got, want, err)
		}
	}
	migrate()
	// A delete before .20 is recoverable for the finished campaign from the .19 snapshot.
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, e.eligible[0])
	migrate()
	if err := migrations.V6_2_1_axm_23(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertHeld(e.campID, 2)
	assertHeld(active, 1)

	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, e.eligible[1])
	assertHeld(active, 1)
	if err := migrations.V6_2_1_axm_23(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.UpdateCampaignStatus.Exec(active, models.CampaignStatusFinished); err != nil {
		t.Fatal(err)
	}
	assertHeld(e.campID, 2)
	assertHeld(active, 1)
}

// The rule is "on" the same way for the sender, the people it left out, the page and the window: a
// rule on chosen campaigns that were all deleted has no new matches. Previously held people stay
// out, and a follow-up has no rule whatever its row says.
func TestRecentRuleOn(t *testing.T) {
	e := newSendsEnv(t, 4)
	ctx := context.Background()
	prior := e.rsPrior(models.CampaignTypeRegular, e.listID, map[int]time.Duration{e.eligible[1]: hours(1)})
	e.rsRule(models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}})
	on := func() bool {
		t.Helper()
		v, err := e.core.RecentRuleOn(e.campID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !on() {
		t.Fatal("the rule is not on")
	}
	if got, err := e.core.HoldRecentBatch(ctx, e.campID, e.eligible); err != nil || !got[e.eligible[1]] || len(got) != 1 {
		t.Fatalf("held %v: %v", got, err)
	}
	// The preview of a started campaign counts the people it left out already, whatever the records say.
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = NOW() - INTERVAL '90 days'`)
	counts, err := e.core.RecentExcludeCounts(ctx, []int{e.listID}, models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeCampaigns, CampaignIDs: pq.Int64Array{int64(prior)}}, e.campID)
	if err != nil || counts.Excluded != 1 || counts.WillSend != counts.Target-1 {
		t.Fatalf("preview %+v: %v", counts, err)
	}
	// The rule has no live sources, but its history still applies.
	e.db.MustExec(`DELETE FROM campaigns WHERE id=$1`, prior)
	if !on() {
		t.Fatal("the sender no longer applies the saved rule's holds")
	}
	rule, err := e.core.GetRecentRule(e.campID)
	if err != nil || rule.Enabled {
		t.Fatalf("page rule %+v: %v", rule, err)
	}
	if got, err := e.core.HoldRecentBatch(ctx, e.campID, e.eligible); err != nil || !got[e.eligible[1]] || len(got) != 1 {
		t.Fatalf("holds after source deletion = %v: %v", got, err)
	}
	_, aud, err := e.core.GetPreflightAudience(ctx, e.campID, 0, true)
	if err != nil || aud.Remaining != len(e.eligible)-1 || aud.RecentFresh != 0 {
		t.Fatalf("resume after source deletion: %+v: %v", aud, err)
	}

	// A follow-up with a row from before this version: off.
	e.db.MustExec(`INSERT INTO axm_campaign_recent_rules(campaign_id,days,scope) VALUES ($1,7,'all') ON CONFLICT (campaign_id) DO UPDATE SET scope='all', campaign_ids='{}'`, e.campID)
	if !on() {
		t.Fatal("a rule on all campaigns is not on")
	}
	parent := e.rsPrior(models.CampaignTypeRegular, e.listID, nil)
	e.db.MustExec(`INSERT INTO axm_campaign_followups(campaign_id,parent_campaign_id,list_uuid,condition) SELECT $1,$2,uuid,'{}' FROM lists WHERE id=$3`, e.campID, parent, e.listID)
	if on() {
		t.Fatal("a follow-up has the rule on")
	}
}
