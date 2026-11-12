package manager

import (
	"sort"
	"sync"
	"time"
)

const (
	// progressSaveInterval is how often the checkpoint and the sent count of every
	// running campaign are saved while it is being sent.
	progressSaveInterval = time.Second

	// closeDrainWait is how long Close() waits for the messages that are with a
	// messenger at that moment.
	closeDrainWait = 1500 * time.Millisecond

	// sentIDsFlushWait is how long a campaign that is being picked up waits for the
	// send records of its earlier run in this process to be written.
	sentIDsFlushWait = 5 * time.Second
)

const (
	msgPending uint8 = iota
	msgSent
	msgFailed
)

// progress keeps the checkpoint of one run of a campaign (AXM, LST-20).
//
// Upstream moved campaigns.last_subscriber_id forward when a batch of subscribers was
// read. Whatever was read but not sent when the app went down was skipped on the next
// start, and the campaign still ended as finished. Here the checkpoint only moves over
// subscribers the campaign is done with.
//
// Messages are queued in ascending subscriber ID order, and several workers send them, so
// they do not finish in order. The checkpoint is the highest subscriber ID that was sent
// such that nothing queued before it is still waiting. A message that failed is passed
// over once a later one is sent, as upstream never sent it again either. Failures at the
// very end stay above the checkpoint, so a campaign that was paused because sending kept
// failing tries those subscribers again when it is resumed. A message that was dropped
// because the campaign was stopped never counts as done.
//
// Subscribers above the checkpoint can still have been sent (a later message finished
// while an earlier one did not). The send records tell those apart on the next run.
type progress struct {
	mu sync.Mutex

	// ids holds the subscribers that were queued and are above the checkpoint, in
	// ascending order, and state what became of each.
	ids   []int
	state []uint8

	// Everything before this index in ids is settled (sent or failed).
	settled int

	checkpoint int
}

// A nil *progress (a pipe that a test assembled by hand) tracks nothing.
func newProgress(checkpoint int) *progress {
	return &progress{checkpoint: checkpoint}
}

// queued notes a subscriber whose message is about to be queued. Calls must come in
// ascending subscriber ID order.
func (g *progress) queued(subID int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.ids = append(g.ids, subID)
	g.state = append(g.state, msgPending)
	g.mu.Unlock()
}

// done notes that the messenger accepted (sent) or rejected the message of a subscriber
// and moves the checkpoint as far as it can go.
func (g *progress) done(subID int, sent bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	i := sort.SearchInts(g.ids, subID)
	if i == len(g.ids) || g.ids[i] != subID {
		return
	}
	if sent {
		g.state[i] = msgSent
	} else {
		g.state[i] = msgFailed
	}

	// Walk over what is settled. The checkpoint goes to the last sent one.
	upto := 0
	for g.settled < len(g.ids) && g.state[g.settled] != msgPending {
		if g.state[g.settled] == msgSent {
			g.checkpoint = g.ids[g.settled]
			upto = g.settled + 1
		}
		g.settled++
	}

	// Forget what is at or below the checkpoint.
	if upto > 0 {
		g.ids, g.state = g.ids[upto:], g.state[upto:]
		g.settled -= upto
	}
}

// get returns the checkpoint.
func (g *progress) get() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkpoint
}
