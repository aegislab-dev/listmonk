package manager

import (
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
)

// scanStore makes each scan succeed or fail at a controlled point.
type scanStore struct {
	countStore
	next func([]int64, []int64) ([]*models.Campaign, error)
}

func (s *scanStore) HoldCampaignLock() (bool, error) { return true, nil }
func (s *scanStore) NextCampaigns(ids, counts []int64) ([]*models.Campaign, error) {
	return s.next(ids, counts)
}

// No sleeps or race to win: the first query fails after new sends arrive; the
// second must persist both the original batch and those new sends exactly once.
func TestScanCampaignsRetainsCountsAfterFailure(t *testing.T) {
	store := &scanStore{}
	m := New(Config{}, store, nil, log.New(io.Discard, "", 0))
	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test"}, 0)
	other := testPipe(m, &models.Campaign{Base: models.Base{ID: 9}, Name: "other"}, 0)
	p.sent.Add(54)
	other.sent.Add(11)
	calls := 0
	saved := map[int64]int64{}
	store.next = func(ids, counts []int64) ([]*models.Campaign, error) {
		calls++
		if calls == 1 {
			p.sent.Add(3)
			return nil, errors.New("test scan failure")
		}
		for i, id := range ids {
			saved[id] += counts[i]
		}
		m.closing.Store(true)
		return nil, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.scanCampaigns(time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		m.closing.Store(true)
		t.Fatal("scanner did not stop")
	}
	if calls != 2 || saved[7] != 57 || saved[9] != 11 {
		t.Fatalf("after %d scans: saved=%v, want map[7:57 9:11]", calls, saved)
	}
	p.saveProgress()
	other.saveProgress()
	if sent, _, _ := store.get(); sent != 0 {
		t.Fatalf("saved %d messages again after the successful scan", sent)
	}
}

// Cleanup must wait for an outstanding scan, so a failed query cannot return
// counts to a pipe that has already saved for the last time and been removed.
func TestFailedScanRestoresCountsBeforeCleanup(t *testing.T) {
	store := &scanStore{}
	m := New(Config{}, store, nil, log.New(io.Discard, "", 0))
	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test"}, 0)
	p.Stop(false)
	p.sent.Add(54)
	p.prog.queued(57)
	p.prog.done(57, true)
	cleaned := make(chan struct{})
	queryErr := errors.New("test scan failure")
	store.next = func(ids, counts []int64) ([]*models.Campaign, error) {
		if len(ids) != 1 || ids[0] != 7 || counts[0] != 54 {
			t.Errorf("scan received IDs %v and counts %v, want [7] and [54]", ids, counts)
		}
		// Observe the exclusion without relying on which goroutine runs first.
		if p.saveMut.TryLock() {
			p.saveMut.Unlock()
			t.Error("cleanup can pass the outstanding scan")
		}
		p.sent.Add(3)
		go func() {
			defer close(cleaned)
			p.cleanup()
		}()
		return nil, queryErr
	}
	if _, err := m.nextCampaigns(); !errors.Is(err, queryErr) {
		t.Fatalf("scan error = %v, want %v", err, queryErr)
	}
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not return after the failed scan")
	}
	if sent, lastID, _ := store.get(); sent != 57 || lastID != 57 {
		t.Fatalf("cleanup saved sent=%d checkpoint=%d, want 57 and 57", sent, lastID)
	}
	if m.HasRunningCampaigns() {
		t.Fatal("cleanup did not remove the campaign")
	}
	// A later save must not count the restored batch twice.
	p.saveProgress()
	if sent, _, _ := store.get(); sent != 57 {
		t.Fatalf("saved %d after retry, want 57", sent)
	}
}
