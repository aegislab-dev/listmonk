package manager

import (
	"errors"
	"io"
	"log"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/paulbellamy/ratecounter"
)

func TestProgressCheckpoint(t *testing.T) {
	type step struct {
		queue int  // Subscriber to queue, or 0.
		done  int  // Subscriber whose message was settled, or 0.
		sent  bool // Whether that message was sent.
		want  int  // The checkpoint after the step.
	}

	for _, tc := range []struct {
		name  string
		start int
		steps []step
	}{
		{"in order", 0, []step{
			{queue: 1}, {queue: 2}, {queue: 3},
			{done: 1, sent: true, want: 1}, {done: 2, sent: true, want: 2}, {done: 3, sent: true, want: 3},
		}},
		{"starts at the saved checkpoint", 40, []step{
			{queue: 41, want: 40}, {queue: 45, want: 40}, {done: 41, sent: true, want: 41}, {done: 45, sent: true, want: 45},
		}},
		{"a later message finishes first", 0, []step{
			{queue: 1}, {queue: 2}, {queue: 3},
			{done: 3, sent: true, want: 0}, {done: 2, sent: true, want: 0}, {done: 1, sent: true, want: 3},
		}},
		{"a dropped message holds the checkpoint for good", 0, []step{
			{queue: 1}, {queue: 2}, {queue: 3}, {queue: 4},
			{done: 1, sent: true, want: 1}, {done: 3, sent: true, want: 1}, {done: 4, sent: true, want: 1},
		}},
		{"a failure is passed over once a later message is sent", 0, []step{
			{queue: 1}, {queue: 2}, {queue: 3},
			{done: 1, sent: true, want: 1}, {done: 2, sent: false, want: 1}, {done: 3, sent: true, want: 3},
		}},
		{"failures at the end stay above the checkpoint", 0, []step{
			{queue: 1}, {queue: 2}, {queue: 3}, {queue: 4},
			{done: 1, sent: true, want: 1}, {done: 2, sent: false, want: 1}, {done: 3, sent: false, want: 1},
			{done: 4, sent: false, want: 1},
		}},
		{"a failure first, then successes", 7, []step{
			{queue: 8, want: 7}, {queue: 9, want: 7}, {queue: 10, want: 7},
			{done: 8, sent: false, want: 7}, {done: 10, sent: true, want: 7}, {done: 9, sent: true, want: 10},
		}},
		{"an unknown or repeated subscriber changes nothing", 0, []step{
			{queue: 1}, {queue: 2},
			{done: 5, sent: true, want: 0}, {done: 1, sent: true, want: 1}, {done: 1, sent: true, want: 1},
			{done: 2, sent: true, want: 2},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newProgress(tc.start)
			for i, s := range tc.steps {
				if s.queue != 0 {
					g.queued(s.queue)
				}
				if s.done != 0 {
					g.done(s.done, s.sent)
				}
				if got := g.get(); got != s.want {
					t.Fatalf("step %d (%+v): checkpoint %d, want %d", i, s, got, s.want)
				}
			}
		})
	}
}

// Messages settled in any order by several goroutines end with the checkpoint on the
// last one, and nothing is kept in memory.
func TestProgressConcurrent(t *testing.T) {
	const n = 5000
	g := newProgress(0)
	ch := make(chan int, n)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for id := range ch {
				if r.Intn(4) == 0 {
					time.Sleep(time.Microsecond)
				}
				// Every seventh message fails, the last one is sent.
				g.done(id, id%7 != 0 || id == n)
			}
		}(int64(w))
	}
	for id := 1; id <= n; id++ {
		g.queued(id)
		ch <- id
	}
	close(ch)
	wg.Wait()

	if got := g.get(); got != n {
		t.Fatalf("checkpoint %d, want %d", got, n)
	}
	if len(g.ids) != 0 || len(g.state) != 0 || g.settled != 0 {
		t.Fatalf("progress still holds %d subscribers (settled %d)", len(g.ids), g.settled)
	}
}

// A nil progress, as in a pipe that a test assembled by hand, does nothing.
func TestProgressNil(t *testing.T) {
	var g *progress
	g.queued(1)
	g.done(1, true)
	if got := g.get(); got != 0 {
		t.Fatalf("checkpoint %d", got)
	}
}

// countStore keeps what a campaign's progress saves, and can hold the lock or not.
type countStore struct {
	Store

	mu       sync.Mutex
	sent     int
	lastID   int
	saves    int
	fail     bool
	released bool
}

func (s *countStore) UpdateCampaignCounts(campID, toSend, sent, lastSubID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("test store failure")
	}
	s.saves++
	s.sent += sent
	if lastSubID > 0 {
		s.lastID = lastSubID
	}
	return nil
}

func (s *countStore) RecordCampaignSends([]models.CampaignSend) error { return nil }
func (s *countStore) ReleaseCampaignLock() {
	s.mu.Lock()
	s.released = true
	s.mu.Unlock()
}

func (s *countStore) get() (sent, lastID, saves int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent, s.lastID, s.saves
}

// heldMessenger keeps every push waiting until it is let go.
type heldMessenger struct {
	entered chan int
	release chan struct{}
}

func (h *heldMessenger) Name() string { return "email" }
func (h *heldMessenger) Push(m models.Message) error {
	h.entered <- m.Subscriber.ID
	<-h.release
	return nil
}
func (h *heldMessenger) Flush() error { return nil }
func (h *heldMessenger) Close() error { return nil }

func testPipe(m *Manager, camp *models.Campaign, checkpoint int) *pipe {
	p := &pipe{
		camp: camp, rate: ratecounter.NewRateCounter(time.Minute), wg: &sync.WaitGroup{}, m: m,
		readID: checkpoint, prog: newProgress(checkpoint), saved: checkpoint,
	}
	m.pipesMut.Lock()
	m.pipes[camp.ID] = p
	m.pipesMut.Unlock()
	return p
}

func queueMessage(m *Manager, p *pipe, subID int) {
	p.wg.Add(1)
	p.prog.queued(subID)
	m.campMsgQ <- CampaignMessage{Campaign: p.camp, Subscriber: models.Subscriber{Base: models.Base{ID: subID}}, pipe: p}
}

// saveProgress writes the count once, writes the checkpoint only when it moved, and
// keeps the count for the next save when the store fails.
func TestPipeSaveProgress(t *testing.T) {
	store := &countStore{}
	m := New(Config{}, store, nil, log.New(io.Discard, "", 0))
	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test", Messenger: "email"}, 100)

	p.saveProgress()
	if _, _, saves := store.get(); saves != 0 {
		t.Fatalf("%d saves with nothing to save", saves)
	}

	p.prog.queued(101)
	p.prog.queued(102)
	p.prog.done(101, true)
	p.sent.Add(1)
	p.saveProgress()
	p.saveProgress()
	if sent, lastID, saves := store.get(); sent != 1 || lastID != 101 || saves != 1 {
		t.Fatalf("after one message: sent=%d lastID=%d saves=%d", sent, lastID, saves)
	}

	store.fail = true
	p.prog.done(102, true)
	p.sent.Add(1)
	p.saveProgress()
	store.fail = false
	p.saveProgress()
	if sent, lastID, saves := store.get(); sent != 2 || lastID != 102 || saves != 2 {
		t.Fatalf("after a failed save: sent=%d lastID=%d saves=%d", sent, lastID, saves)
	}
}

// Close() stops the running campaigns, waits for the messages that are with the
// messenger, saves the checkpoint and the count, and gives the lock up. Queued messages
// are dropped and stay above the checkpoint.
func TestCloseStopsCampaignsAndSavesProgress(t *testing.T) {
	store := &countStore{}
	m := New(Config{Concurrency: 2, MessageRate: 1000}, store, nil, log.New(io.Discard, "", 0))
	m.sends.waits = nil
	msgr := &heldMessenger{entered: make(chan int, 10), release: make(chan struct{})}
	if err := m.AddMessenger(msgr); err != nil {
		t.Fatal(err)
	}
	go m.sends.run()
	go m.worker()
	go m.worker()

	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test", Messenger: "email"}, 0)
	for id := 1; id <= 6; id++ {
		queueMessage(m, p, id)
	}
	// Two messages are with the messenger, four are queued.
	<-msgr.entered
	<-msgr.entered

	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()

	// Close() waits for the two messages.
	select {
	case <-closed:
		t.Fatal("Close() returned while messages were being sent")
	case <-time.After(100 * time.Millisecond):
	}
	if !p.stopped.Load() {
		t.Fatal("the campaign was not stopped")
	}
	close(msgr.release)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return")
	}
	p.wg.Wait()

	if sent, lastID, _ := store.get(); sent != 2 || lastID != 2 {
		t.Fatalf("saved sent=%d lastID=%d, want 2 and 2", sent, lastID)
	}
	if !store.released {
		t.Fatal("the campaign sending lock was not released")
	}
	select {
	case id := <-msgr.entered:
		t.Fatalf("subscriber %d was sent after closing", id)
	default:
	}
}

// A mail server that takes seconds to answer: Close() waits for the answer, so the
// message is behind the checkpoint and is not sent again on the next start. The wait
// must cover a server that is slower than upstream's whole 3 second reload allowance.
func TestCloseWaitsForSlowMessenger(t *testing.T) {
	if closeDrainWait < 5*time.Second {
		t.Fatalf("closeDrainWait is %v", closeDrainWait)
	}

	store := &countStore{}
	m := New(Config{Concurrency: 2, MessageRate: 1000}, store, nil, log.New(io.Discard, "", 0))
	m.sends.waits = nil
	msgr := &heldMessenger{entered: make(chan int, 10), release: make(chan struct{})}
	if err := m.AddMessenger(msgr); err != nil {
		t.Fatal(err)
	}
	go m.sends.run()
	go m.worker()
	go m.worker()

	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test", Messenger: "email"}, 0)
	for id := 1; id <= 4; id++ {
		queueMessage(m, p, id)
	}
	<-msgr.entered
	<-msgr.entered

	// The server answers 2.5 seconds into the shutdown.
	time.AfterFunc(2500*time.Millisecond, func() { close(msgr.release) })
	start := time.Now()
	m.Close()
	if d := time.Since(start); d < 2400*time.Millisecond || d > 4500*time.Millisecond {
		t.Fatalf("Close() took %v, want about 2.5s", d)
	}
	if n := m.inflight.Load(); n != 0 {
		t.Fatalf("%d messages still with the messenger after Close()", n)
	}
	if sent, lastID, _ := store.get(); sent != 2 || lastID != 2 {
		t.Fatalf("saved sent=%d lastID=%d, want 2 and 2", sent, lastID)
	}
}

// Messages that are still with the messenger when Close() gives up waiting are not
// behind the saved checkpoint.
func TestCloseDoesNotWaitForeverForTheMessenger(t *testing.T) {
	store := &countStore{}
	m := New(Config{Concurrency: 1, MessageRate: 1000}, store, nil, log.New(io.Discard, "", 0))
	m.sends.waits = nil
	msgr := &heldMessenger{entered: make(chan int, 10), release: make(chan struct{})}
	if err := m.AddMessenger(msgr); err != nil {
		t.Fatal(err)
	}
	go m.sends.run()
	go m.worker()

	p := testPipe(m, &models.Campaign{Base: models.Base{ID: 7}, Name: "test", Messenger: "email"}, 0)
	queueMessage(m, p, 1)
	<-msgr.entered

	m.drainWait = 300 * time.Millisecond
	start := time.Now()
	m.Close()
	if d := time.Since(start); d < m.drainWait || d > m.drainWait+3*time.Second {
		t.Fatalf("Close() took %v", d)
	}
	if sent, lastID, saves := store.get(); sent != 0 || lastID != 0 || saves != 0 {
		t.Fatalf("saved sent=%d lastID=%d saves=%d for a message that was not done", sent, lastID, saves)
	}
	close(msgr.release)
}
