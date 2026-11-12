package manager

import (
	"bytes"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/paulbellamy/ratecounter"
)

// sendStore is a Store that only keeps send records.
type sendStore struct {
	Store

	mu      sync.Mutex
	batches [][]models.CampaignSend
	fail    int           // Fail this many calls first. -1 fails every call.
	block   chan struct{} // When set, every call waits here.
	calls   int
}

func (s *sendStore) RecordCampaignSends(sends []models.CampaignSend) error {
	if s.block != nil {
		<-s.block
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail < 0 {
		return errors.New("test record failure")
	}
	if s.fail > 0 {
		s.fail--
		return errors.New("test record failure")
	}
	s.batches = append(s.batches, append([]models.CampaignSend(nil), sends...))
	return nil
}

func (s *sendStore) CountCampaignSends(int) (int, error) { return len(s.subIDs()), nil }

func (s *sendStore) subIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, b := range s.batches {
		for _, r := range b {
			out = append(out, r.SubscriberID)
		}
	}
	return out
}

func testSendLog(store Store, w io.Writer) *sendLog {
	s := newSendLog(store, log.New(w, "", 0))
	s.waits = []time.Duration{time.Millisecond, time.Millisecond}
	return s
}

func TestSendLogWritesEveryRecordInBatches(t *testing.T) {
	store := &sendStore{}
	s := testSendLog(store, io.Discard)

	// Queue before the writer starts so that the records have to be batched.
	const n = sendLogBatchSize*2 + 37
	for i := 1; i <= n; i++ {
		s.add(7, i)
	}
	go s.run()
	if !s.flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}

	ids := store.subIDs()
	if len(ids) != n {
		t.Fatalf("recorded %d of %d", len(ids), n)
	}
	for i, id := range ids {
		if id != i+1 {
			t.Fatalf("record %d is subscriber %d: order was not kept", i, id)
		}
	}
	if len(store.batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(store.batches))
	}
	for _, b := range store.batches {
		if len(b) > sendLogBatchSize {
			t.Fatalf("batch of %d exceeds %d", len(b), sendLogBatchSize)
		}
		for _, r := range b {
			if r.CampaignID != 7 || time.Since(r.SentAt) > time.Minute {
				t.Fatalf("bad record %+v", r)
			}
		}
	}

	// A record that arrives alone is written on its own, without waiting for more.
	s.add(7, n+1)
	if !s.flush(5*time.Second) || len(store.subIDs()) != n+1 {
		t.Fatal("single record was not written")
	}
}

func TestSendLogRetriesThenDrops(t *testing.T) {
	var logs bytes.Buffer
	store := &sendStore{fail: 2}
	s := testSendLog(store, &logs)
	go s.run()

	// Two failures are within the retries: nothing is lost and nothing is logged.
	s.add(1, 1)
	s.add(1, 2)
	if !s.flush(5*time.Second) || len(store.subIDs()) != 2 || s.dropped.Load() != 0 || logs.Len() != 0 {
		t.Fatalf("retry lost records: %v dropped=%d log=%q", store.subIDs(), s.dropped.Load(), logs.String())
	}

	// A store that keeps failing loses the batch, says so, and does not get stuck.
	store.mu.Lock()
	store.fail = -1
	store.mu.Unlock()
	s.add(3, 10)
	s.add(2, 11)
	if !s.flush(5 * time.Second) {
		t.Fatal("flush timed out on a failing store")
	}
	if s.dropped.Load() != 2 {
		t.Fatalf("dropped %d, want 2", s.dropped.Load())
	}
	if got := logs.String(); !strings.Contains(got, "dropped 2 send records of campaigns [2 3]") || !strings.Contains(got, "test record failure") {
		t.Fatalf("unexpected log %q", got)
	}

	// Once the store works again, recording carries on.
	store.mu.Lock()
	store.fail = 0
	store.mu.Unlock()
	s.add(1, 3)
	if !s.flush(5*time.Second) || len(store.subIDs()) != 3 {
		t.Fatalf("did not recover: %v", store.subIDs())
	}
}

func TestSendLogNeverBlocksSending(t *testing.T) {
	var logs bytes.Buffer
	store := &sendStore{block: make(chan struct{})}
	s := testSendLog(store, &logs)
	go s.run()

	// The store hangs. Far more records than the queue holds must still return at once.
	const n = sendLogQueueSize*2 + 5000
	start := time.Now()
	for i := 1; i <= n; i++ {
		s.add(1, i)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("add() waited on a stalled store: %v for %d records", d, n)
	}
	if s.dropped.Load() == 0 || !strings.Contains(logs.String(), "queue is full") {
		t.Fatalf("overflow was not counted or logged: dropped=%d log=%q", s.dropped.Load(), logs.String())
	}
	if s.flush(50 * time.Millisecond) {
		t.Fatal("flush reported success while the store was stalled")
	}

	// When the store comes back, what was queued is written and the rest is accounted for.
	close(store.block)
	if !s.flush(10 * time.Second) {
		t.Fatal("flush timed out after the store recovered")
	}
	if got := int64(len(store.subIDs())) + s.dropped.Load(); got != n {
		t.Fatalf("recorded + dropped = %d, want %d", got, n)
	}
}

// waitSent waits for a pipe's sent counter. The worker marks a message as done before
// it counts it, so the counter can trail wg.Wait() by a moment.
func waitSent(t *testing.T, p *pipe, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.sent.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("sent counter %d, want %d", p.sent.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// pushMessenger accepts every message except those to the listed subscribers.
type pushMessenger struct {
	mu   sync.Mutex
	sent []int
	fail map[int]bool
}

func (p *pushMessenger) Name() string { return "email" }
func (p *pushMessenger) Push(m models.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail[m.Subscriber.ID] {
		return errors.New("test push failure")
	}
	p.sent = append(p.sent, m.Subscriber.ID)
	return nil
}
func (p *pushMessenger) Flush() error { return nil }
func (p *pushMessenger) Close() error { return nil }

func TestWorkerRecordsOnlyDeliveredCampaignMessages(t *testing.T) {
	store := &sendStore{}
	m := New(Config{Concurrency: 1, MessageRate: 1000}, store, nil, log.New(io.Discard, "", 0))
	m.sends.waits = nil
	msgr := &pushMessenger{fail: map[int]bool{3: true}}
	if err := m.AddMessenger(msgr); err != nil {
		t.Fatal(err)
	}
	go m.sends.run()
	go m.worker()

	camp := &models.Campaign{Base: models.Base{ID: 42}, Name: "test", Messenger: "email"}
	newPipe := func() *pipe {
		return &pipe{camp: camp, rate: ratecounter.NewRateCounter(time.Minute), wg: &sync.WaitGroup{}, m: m}
	}
	push := func(p *pipe, subID int) {
		if p != nil {
			p.wg.Add(1)
		}
		m.campMsgQ <- CampaignMessage{Campaign: camp, Subscriber: models.Subscriber{Base: models.Base{ID: subID}}, pipe: p}
	}

	live, stopped := newPipe(), newPipe()
	stopped.Stop(false)

	push(live, 1)    // Delivered.
	push(live, 2)    // Delivered.
	push(live, 3)    // The messenger rejects it.
	push(stopped, 4) // The campaign was paused: never sent.
	push(nil, 5)     // A test send outside a campaign run.
	push(live, 6)    // Delivered.
	live.wg.Wait()
	stopped.wg.Wait()

	// The test send has no pipe to wait on, so wait for the messenger to see it. The
	// worker counts and records a message just after marking it done, so wait for
	// the counter of the last message as well before flushing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		msgr.mu.Lock()
		n := len(msgr.sent)
		msgr.mu.Unlock()
		if n == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("messenger got %d messages, want 4", n)
		}
		time.Sleep(time.Millisecond)
	}
	waitSent(t, live, 3)
	for len(store.subIDs()) < 3 && time.Now().Before(deadline) {
		m.sends.flush(time.Second)
	}
	// Give a wrong extra record the chance to show up.
	time.Sleep(20 * time.Millisecond)
	if !m.sends.flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}

	got := store.subIDs()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 6 {
		t.Fatalf("recorded subscribers %v, want [1 2 6]", got)
	}
	for _, b := range store.batches {
		for _, r := range b {
			if r.CampaignID != 42 {
				t.Fatalf("bad campaign in %+v", r)
			}
		}
	}
}

// A Manager assembled without New(), as the tracking and footer tests do, has no send
// log. Sending must work the same.
func TestWorkerWithoutSendLog(t *testing.T) {
	msgr := &pushMessenger{}
	m := &Manager{
		cfg: Config{MessageRate: 1000}, log: log.New(io.Discard, "", 0),
		messengers: map[string]Messenger{"email": msgr}, campMsgQ: make(chan CampaignMessage, 1),
	}
	go m.worker()

	camp := &models.Campaign{Base: models.Base{ID: 1}, Messenger: "email"}
	p := &pipe{camp: camp, rate: ratecounter.NewRateCounter(time.Minute), wg: &sync.WaitGroup{}, m: m}
	p.wg.Add(1)
	m.campMsgQ <- CampaignMessage{Campaign: camp, Subscriber: models.Subscriber{Base: models.Base{ID: 9}}, pipe: p}
	p.wg.Wait()
	waitSent(t, p, 1)
}
