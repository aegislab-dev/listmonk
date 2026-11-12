package manager

import (
	"log"
	"sort"
	"sync/atomic"
	"time"

	"github.com/knadh/listmonk/models"
)

const (
	// sendLogQueueSize is how many delivered messages can wait to be recorded. It only
	// fills up while the database is failing or stalled.
	sendLogQueueSize = 20000

	// sendLogBatchSize is the most records written in one statement.
	sendLogBatchSize = 500

	// sendLogMinGap is the shortest time between the starts of two writes. One
	// database round trip per message was measured to cost about 5% of the sending
	// speed at several thousand messages a second, so records that arrive faster
	// than this are collected and written together. A record that arrives after a
	// quiet moment, as every record does at ordinary sending rates, is written at once.
	sendLogMinGap = 20 * time.Millisecond
)

// sendLogRetryWaits are the pauses before each retry of a failed write. After the last
// one fails, the batch is dropped and logged.
var sendLogRetryWaits = []time.Duration{time.Second, 3 * time.Second, 10 * time.Second}

// sendLog records which subscriber was sent which campaign and when (AXM send records).
//
// Recording never slows down or stops sending. Workers hand a record to a buffered
// queue without waiting, and a single writer goroutine saves whatever has piled up in
// one statement: one row at a time when sending is slow, batches (at most one every
// sendLogMinGap) when it is fast. If the store keeps failing, the batch is dropped after
// a few retries, and if the queue is full, new records are dropped. Both are logged
// with counts.
//
// A record is queued only after the messenger accepted the message, so the records can
// lack a message that went out (process killed before the write, or a dropped batch)
// but never hold one that did not.
type sendLog struct {
	store Store
	log   *log.Logger
	q     chan sendLogItem
	waits []time.Duration

	// minGap is sendLogMinGap, except in tests.
	minGap time.Duration

	// dropped counts the records lost to a full queue or to failed writes.
	// overflowed counts only those lost to a full queue, to pace that log line.
	dropped    atomic.Int64
	overflowed atomic.Int64
}

// sendLogItem is a record, or when flush is set, a request to be told once
// everything queued before it has been written (or dropped).
type sendLogItem struct {
	send  models.CampaignSend
	flush chan struct{}
}

func newSendLog(store Store, l *log.Logger) *sendLog {
	return &sendLog{
		store:  store,
		log:    l,
		q:      make(chan sendLogItem, sendLogQueueSize),
		waits:  sendLogRetryWaits,
		minGap: sendLogMinGap,
	}
}

// add queues the record of a message that was just sent. It never blocks.
func (s *sendLog) add(campID, subID int) {
	it := sendLogItem{send: models.CampaignSend{CampaignID: campID, SubscriberID: subID, SentAt: time.Now()}}

	select {
	case s.q <- it:
	default:
		// Log the first drop and then every thousandth, not one line per message.
		total := s.dropped.Add(1)
		if n := s.overflowed.Add(1); n == 1 || n%1000 == 0 {
			s.log.Printf("error recording campaign sends: queue is full. %d send records dropped so far. sending continues", total)
		}
	}
}

// run writes queued records until the queue is closed. It is the only writer.
func (s *sendLog) run() {
	var (
		batch   = make([]models.CampaignSend, 0, sendLogBatchSize)
		waiting []chan struct{}

		// The earliest time the next write may start, unless a flush asks for it.
		notBefore time.Time
	)

	take := func(it sendLogItem) {
		if it.flush != nil {
			waiting = append(waiting, it.flush)
			return
		}
		batch = append(batch, it.send)
	}

	for it := range s.q {
		batch, waiting = batch[:0], waiting[:0]
		take(it)

		// Take whatever else is already waiting. If the previous write started less
		// than minGap ago, keep collecting until that time has passed. A flush request
		// or a full batch ends the collecting at once.
	collect:
		for len(batch) < sendLogBatchSize && len(waiting) == 0 {
			select {
			case it, ok := <-s.q:
				if !ok {
					break collect
				}
				take(it)
				continue
			default:
			}

			wait := time.Until(notBefore)
			if wait <= 0 {
				break collect
			}
			select {
			case it, ok := <-s.q:
				if !ok {
					break collect
				}
				take(it)
			case <-time.After(wait):
				break collect
			}
		}

		notBefore = time.Now().Add(s.minGap)
		s.write(batch)
		for _, ch := range waiting {
			close(ch)
		}
	}
}

// write saves a batch, retrying a few times before giving up on it.
func (s *sendLog) write(batch []models.CampaignSend) {
	if len(batch) == 0 {
		return
	}

	var err error
	for i := 0; ; i++ {
		if err = s.store.RecordCampaignSends(batch); err == nil {
			return
		}
		if i >= len(s.waits) {
			break
		}
		time.Sleep(s.waits[i])
	}

	// Name the campaigns so that the gap can be found later.
	seen := map[int]bool{}
	var ids []int
	for _, b := range batch {
		if !seen[b.CampaignID] {
			seen[b.CampaignID] = true
			ids = append(ids, b.CampaignID)
		}
	}
	sort.Ints(ids)

	n := s.dropped.Add(int64(len(batch)))
	s.log.Printf("error recording campaign sends: %v. dropped %d send records of campaigns %v (%d dropped so far). the messages were sent",
		err, len(batch), ids, n)
}

// flush waits until everything queued so far has been written or dropped.
// It reports false if that did not happen within the timeout.
func (s *sendLog) flush(timeout time.Duration) bool {
	var (
		done = make(chan struct{})
		t    = time.NewTimer(timeout)
	)
	defer t.Stop()

	select {
	case s.q <- sendLogItem{flush: done}:
	case <-t.C:
		return false
	}

	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}
