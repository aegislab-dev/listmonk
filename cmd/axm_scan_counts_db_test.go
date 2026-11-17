package main

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
)

// Control scans and defer progress saves so the first failing query sees all
// ten unsaved deliveries, regardless of when the periodic saver runs.
type scanErrorStore struct {
	*store
	permits    chan struct{}
	scanErrs   chan error
	calls      atomic.Int64
	holdSaves  atomic.Bool
	stopped    atomic.Bool
	dropResult bool
}

func (s *scanErrorStore) HoldCampaignLock() (bool, error) {
	if s.calls.Load() > 0 {
		<-s.permits
	}
	if s.stopped.Load() {
		return false, nil
	}
	return s.store.HoldCampaignLock()
}

func (s *scanErrorStore) NextCampaigns(ids []int64) ([]*models.Campaign, error) {
	n := s.calls.Add(1)
	camps, err := s.store.NextCampaigns(ids)
	if err == nil && n > 1 && s.dropResult {
		// The real SQL has committed, but its successful response is lost.
		camps, err = nil, errors.New("test: result lost after commit")
	}
	if err != nil {
		s.scanErrs <- err
	}
	return camps, err
}

func (s *scanErrorStore) UpdateCampaignCounts(id, toSend, sent, lastID int) error {
	if s.holdSaves.Load() {
		return errors.New("test: progress save deferred")
	}
	return s.store.UpdateCampaignCounts(id, toSend, sent, lastID)
}

// A valid JSONB value that Headers.Scan cannot decode fails on the Go side,
// after Postgres has committed the next-campaigns statement. Repeating the
// scan must neither replay a committed sent increment nor lose unsaved sends.
func TestScanRowErrorsDoNotReplaySentCounts(t *testing.T) {
	testScanErrorsDoNotReplaySentCounts(t, false)
}

func TestScanLostResultsDoNotReplaySentCounts(t *testing.T) {
	testScanErrorsDoNotReplaySentCounts(t, true)
}

func testScanErrorsDoNotReplaySentCounts(t *testing.T, dropResult bool) {
	t.Helper()
	e := newSendsEnv(t, 40)
	st := &scanErrorStore{store: e.store, permits: make(chan struct{}), scanErrs: make(chan error, 1), dropResult: dropResult}
	st.holdSaves.Store(true)
	msgr := &gateMessenger{allow: make(chan struct{}, 10)}
	for i := 0; i < 10; i++ {
		msgr.allow <- struct{}{}
	}
	m := e.startManager(st, msgr, &syncBuffer{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			st.stopped.Store(true)
			st.holdSaves.Store(false)
			m.StopCampaign(e.campID)
			close(msgr.allow)
			close(st.permits)
			m.Close()
		})
	}
	defer stop()
	// Both workers are blocked in their next Push, so all ten prior messages
	// have been counted by the worker, rather than merely accepted by Push.
	waitFor(t, "ten settled deliveries and two held messages", func() bool { return msgr.pushes.Load() == 12 })
	bad, _ := e.addCampaign("running")
	wantError := "result lost after commit"
	if !dropResult {
		e.db.MustExec(`UPDATE campaigns SET headers = '{"x":"y"}' WHERE id = $1`, bad)
		wantError = "headers"
	}
	scan := func() {
		t.Helper()
		select {
		case st.permits <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("scanner did not request another scan")
		}
		select {
		case err := <-st.scanErrs:
			if !strings.Contains(err.Error(), wantError) {
				t.Fatalf("expected %s, got %v", wantError, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("scan did not return its error")
		}
	}
	for i := 0; i < 5; i++ {
		scan()
		if _, sent, _ := e.campaign(); sent > 10 {
			t.Errorf("scan %d: sent=%d with only ten settled deliveries", i+1, sent)
		}
	}
	// The periodic saver must make progress even while every scan is failing.
	st.holdSaves.Store(false)
	waitFor(t, "the periodic sent-count save", func() bool { _, sent, _ := e.campaign(); return sent >= 10 })
	scan()
	if _, sent, _ := e.campaign(); sent != 10 {
		t.Errorf("after periodic save and another failed scan: sent=%d, want 10", sent)
	}
	stop()
	// The two already with the messenger may also finish during Close.
	if _, sent, _ := e.campaign(); sent != len(msgr.sentIDs()) {
		t.Fatalf("after Close: sent=%d, delivered=%d", sent, len(msgr.sentIDs()))
	}
}
