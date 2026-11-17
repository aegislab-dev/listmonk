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
	next func([]int64) ([]*models.Campaign, error)
}

func (s *scanStore) HoldCampaignLock() (bool, error) { return true, nil }
func (s *scanStore) NextCampaigns(ids []int64) ([]*models.Campaign, error) {
	return s.next(ids)
}

// No sleeps or race to win: the first query fails after new sends arrive; the
// next scan must leave both the original batch and the new sends for progress
// saves, without needing to know whether the failed query committed.
func TestScanCampaignsRetainsCountsAfterFailure(t *testing.T) {
	store := &scanStore{}
	m := New(Config{}, store, nil, log.New(io.Discard, "", 0))
	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test"}, 0)
	other := testPipe(m, &models.Campaign{Base: models.Base{ID: 9}, Name: "other"}, 0)
	p.sent.Add(54)
	other.sent.Add(11)
	calls := 0
	store.next = func(ids []int64) ([]*models.Campaign, error) {
		calls++
		if len(ids) != 2 || ids[0]+ids[1] != 16 {
			t.Errorf("scan IDs = %v, want campaigns 7 and 9", ids)
		}
		if calls == 1 {
			p.sent.Add(3)
			return nil, errors.New("test scan failure")
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
	if calls != 2 || p.sent.Load() != 57 || other.sent.Load() != 11 {
		t.Fatalf("after %d scans: pending counts=%d and %d, want 57 and 11", calls, p.sent.Load(), other.sent.Load())
	}
	p.saveProgress()
	other.saveProgress()
	p.saveProgress()
	other.saveProgress()
	if sent, _, _ := store.get(); sent != 68 {
		t.Fatalf("saved %d messages, want 68 without loss or duplication", sent)
	}
}

// Cleanup can save and remove the pipe while a scan is still outstanding. A
// query error after that must not restore counts to a pipe that has gone away.
func TestCleanupSavesCountsDuringFailedScan(t *testing.T) {
	store := &scanStore{}
	m := New(Config{}, store, nil, log.New(io.Discard, "", 0))
	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test"}, 0)
	p.Stop(false)
	p.sent.Add(54)
	p.prog.queued(57)
	p.prog.done(57, true)
	cleaned := make(chan struct{})
	queryErr := errors.New("test scan failure")
	store.next = func(ids []int64) ([]*models.Campaign, error) {
		if len(ids) != 1 || ids[0] != 7 {
			t.Errorf("scan received IDs %v, want [7]", ids)
		}
		p.sent.Add(3)
		go func() {
			defer close(cleaned)
			p.cleanup()
		}()
		select {
		case <-cleaned:
		case <-time.After(5 * time.Second):
			t.Error("cleanup blocked on the outstanding scan")
		}
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
	// A later save must not count any of the batch twice.
	p.saveProgress()
	if sent, _, _ := store.get(); sent != 57 {
		t.Fatalf("saved %d after retry, want 57", sent)
	}
}
