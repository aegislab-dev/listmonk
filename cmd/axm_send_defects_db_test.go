package main

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// LST-25: one test per sending defect that LST-23 listed. They run the real manager,
// the real queries and the real store against AXM_TEST_DB_URL.

// Reading a batch of subscribers must not move the campaign's saved position
// (last_subscriber_id). It means "done up to here", and the app that goes down between
// reading and sending is what that protects.
func TestReadingBatchLeavesCheckpointAlone(t *testing.T) {
	e := newSendsEnv(t, 30)
	// The scan sets the upper bound when it picks a campaign up. Do it by hand here.
	e.db.MustExec(`UPDATE campaigns SET max_subscriber_id = $2 WHERE id = $1`, e.campID, e.eligible[len(e.eligible)-1])

	read := 0
	for i := 0; i < 3; i++ {
		subs, err := e.store.NextSubscribers(e.campID, read, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 10 {
			t.Fatalf("batch %d has %d subscribers", i, len(subs))
		}
		read = subs[len(subs)-1].ID
		if cp := e.checkpoint(); cp != 0 {
			t.Fatalf("after reading up to subscriber %d the campaign's checkpoint is %d, want 0", read, cp)
		}
	}
}

// A campaign with many recipients, many workers, a scan that runs all the time and a
// checkpoint save that does too ends with every recipient sent once, recorded once and
// counted exactly. This is an end-to-end check against the real queries. It can only catch
// a miscount that happens by chance; the two orderings behind it are checked without
// chance in internal/manager/progress_test.go (TestWorkerCountsMessageBeforeMarkingItDone,
// TestSentCountIsWrittenOnce).
func TestBigCampaignEndsWithExactCounts(t *testing.T) {
	const n = 3000
	e := newSendsEnv(t, n)
	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	e.startManagerWith(e.store, msgr, logs, func(c *manager.Config) {
		c.BatchSize = 50
		c.Concurrency = 12
		c.ScanInterval = 3 * time.Millisecond
	})

	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})

	delivered := msgr.sentIDs()
	noRepeats(t, delivered)
	wantSame(t, "delivered", delivered, e.eligible)
	wantSame(t, "recorded", e.recorded(), e.eligible)

	waitFor(t, "campaign finished", func() bool {
		s, _, _ := e.campaign()
		return s == models.CampaignStatusFinished
	})
	// Let any late count writes land before comparing.
	time.Sleep(500 * time.Millisecond)
	if status, sent, toSend := e.campaign(); status != models.CampaignStatusFinished || sent != n || toSend != n {
		t.Fatalf("status=%s sent=%d to_send=%d, want finished with %d for both", status, sent, toSend, n)
	}
	if cp := e.checkpoint(); cp != e.eligible[n-1] {
		t.Fatalf("checkpoint=%d, want the last recipient %d", cp, e.eligible[n-1])
	}
}

// Pausing and resuming over and over while four workers send never gives a recipient
// the campaign twice, and nobody is left out when it finishes.
func TestPauseResumeManyTimesSendsOnce(t *testing.T) {
	const n = 1500
	e := newSendsEnv(t, n)
	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	m := e.startManagerWith(e.store, msgr, logs, func(c *manager.Config) {
		c.BatchSize = 40
		c.Concurrency = 4
		c.ScanInterval = 5 * time.Millisecond
	})

	for i := 0; i < 25; i++ {
		time.Sleep(time.Duration(5+i%7) * time.Millisecond)
		if s, _, _ := e.campaign(); s != models.CampaignStatusRunning {
			break
		}
		e.setStatus(models.CampaignStatusPaused)
		m.StopCampaign(e.campID)
		waitFor(t, "the paused campaign to wind down", func() bool { return !m.HasRunningCampaigns() })
		e.setStatus(models.CampaignStatusRunning)
	}

	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})
	delivered := msgr.sentIDs()
	noRepeats(t, delivered)
	if miss := minus(e.eligible, delivered); len(miss) > 0 {
		st, sent, _ := e.campaign()
		t.Fatalf("%d recipients missed (first %d, last %d); delivered %d; campaign %s sent=%d checkpoint=%d\n%s",
			len(miss), miss[0], miss[len(miss)-1], len(delivered), st, sent, e.checkpoint(), logs.String())
	}
	wantSame(t, "recorded", e.recorded(), e.eligible)
	time.Sleep(500 * time.Millisecond)
	if status, sent, _ := e.campaign(); status != models.CampaignStatusFinished || sent != n {
		t.Fatalf("status=%s sent=%d, want finished with %d", status, sent, n)
	}
}

// The manager's store deletes a subscriber (bounce action "delete"). The query takes the
// IDs and the UUIDs as two arguments.
func TestStoreDeleteSubscriber(t *testing.T) {
	e := newSendsEnv(t, 3)
	id := e.eligible[1]
	if err := e.store.DeleteSubscriber(int64(id)); err != nil {
		t.Fatal(err)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE id = $1`, id)
	if n != 0 {
		t.Fatal("the subscriber is still there")
	}
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE id = ANY($1)`, pq.Array([]int{e.eligible[0], e.eligible[2]}))
	if n != 2 {
		t.Fatalf("%d of the other 2 subscribers are left", n)
	}
}

// The manager's store blocklists a subscriber (bounce action "blocklist").
func TestStoreBlocklistSubscriber(t *testing.T) {
	e := newSendsEnv(t, 3)
	id := e.eligible[1]
	if err := e.store.BlocklistSubscriber(int64(id)); err != nil {
		t.Fatal(err)
	}
	var st string
	e.get(&st, `SELECT status FROM subscribers WHERE id = $1`, id)
	if st != "blocklisted" {
		t.Fatalf("status=%s", st)
	}
}

// The bounce action "delete" goes through the record-bounce query that core.RecordBounce
// uses (the manager store has no bounce method of its own any more): the subscriber that
// reaches the bounce count is deleted, one that does not is kept.
func TestBounceActionDelete(t *testing.T) {
	e := newSendsEnv(t, 3)
	bounce := func(id, count int) {
		t.Helper()
		if _, err := e.q.RecordBounce.Exec(e.subUUID(id), "", e.campUUID, models.BounceTypeHard, "test", []byte("{}"),
			time.Now(), count, "delete"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	bounce(e.eligible[0], 2)
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE id = $1`, e.eligible[0])
	if n != 1 {
		t.Fatal("deleted below the bounce count")
	}
	bounce(e.eligible[1], 1)
	e.get(&n, `SELECT COUNT(*) FROM subscribers WHERE id = $1`, e.eligible[1])
	if n != 0 {
		t.Fatal("not deleted at the bounce count")
	}
}

// pausedWhileReading makes the first read of the campaign's subscribers happen while the
// campaign is paused in the DB, and puts it back to running right after, as an operator
// who pauses and resumes within a moment does while the scan has just picked the campaign up.
type pausedWhileReading struct {
	*store
	e    *sendsEnv
	once atomic.Bool
}

func (p *pausedWhileReading) NextSubscribers(campID, lastSubID, limit int) ([]models.Subscriber, error) {
	if p.once.CompareAndSwap(false, true) {
		p.e.setStatus(models.CampaignStatusPaused)
		defer p.e.setStatus(models.CampaignStatusRunning)
	}
	return p.store.NextSubscribers(campID, lastSubID, limit)
}

// A campaign that is paused at the moment its subscribers are read, and running again when
// the read has come back empty, is not finished: nobody was sent yet.
func TestPauseDuringFirstReadDoesNotFinishCampaign(t *testing.T) {
	e := newSendsEnv(t, 40)
	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	e.startManager(&pausedWhileReading{store: e.store, e: e}, msgr, logs)

	waitFor(t, "the campaign to be sent to everyone", func() bool {
		s, _, _ := e.campaign()
		if s == models.CampaignStatusFinished && len(msgr.sentIDs()) < len(e.eligible) {
			t.Fatalf("campaign finished after %d of %d recipients\n%s", len(msgr.sentIDs()), len(e.eligible), logs.String())
		}
		return len(msgr.sentIDs()) == len(e.eligible)
	})
	wantSame(t, "delivered", msgr.sentIDs(), e.eligible)
	waitFor(t, "the send records", func() bool { return len(e.recorded()) == len(e.eligible) })
	wantSame(t, "recorded", e.recorded(), e.eligible)
}

// A running campaign whose lists are all gone has nobody to send to and is finished, as
// before. Only a campaign that is not running is left alone.
func TestRunningCampaignWithoutListsFinishes(t *testing.T) {
	e := newSendsEnv(t, 5)
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, e.campID)
	msgr := &gateMessenger{}
	e.startManager(e.store, msgr, io.Discard)

	waitFor(t, "campaign finished", func() bool {
		s, _, _ := e.campaign()
		return s == models.CampaignStatusFinished
	})
	if got := msgr.sentIDs(); len(got) != 0 {
		t.Fatalf("%d messages went out", len(got))
	}
}

// A campaign that is not running gives the sentinel error, not an empty batch.
func TestReadingPausedCampaignIsNotExhaustion(t *testing.T) {
	e := newSendsEnv(t, 5)
	e.db.MustExec(`UPDATE campaigns SET max_subscriber_id = $2, status = 'paused' WHERE id = $1`, e.campID, e.eligible[4])
	if _, err := e.store.NextSubscribers(e.campID, 0, 10); !errors.Is(err, manager.ErrCampaignNotRunning) {
		t.Fatalf("err=%v, want ErrCampaignNotRunning", err)
	}
}
