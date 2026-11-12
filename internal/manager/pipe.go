package manager

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/paulbellamy/ratecounter"
)

type pipe struct {
	camp       *models.Campaign
	rate       *ratecounter.RateCounter
	wg         *sync.WaitGroup
	sent       atomic.Int64
	errors     atomic.Uint64
	stopped    atomic.Bool
	withErrors atomic.Bool

	// AXM (LST-20). readID is the subscriber ID that this run has read up to. Only the
	// reader loop in Run() touches it. prog keeps the checkpoint, which trails readID
	// by the messages that are queued or being sent. alreadySent holds the subscribers
	// above the checkpoint that an earlier run already sent the campaign to.
	readID      int
	prog        *progress
	alreadySent map[int]struct{}

	// saved is the checkpoint that was last written to the store.
	saveMut sync.Mutex
	saved   int

	m *Manager
}

// newPipe adds a campaign to the process queue.
func (m *Manager) newPipe(c *models.Campaign) (*pipe, error) {
	// Validate messenger.
	if _, ok := m.messengers[c.Messenger]; !ok {
		m.store.UpdateCampaignStatus(c.ID, models.CampaignStatusCancelled)
		return nil, fmt.Errorf("unknown messenger %s on campaign %s", c.Messenger, c.Name)
	}

	// Resolve any inline images before compiling the template.
	if err := m.LoadInlineImages(c); err != nil {
		return nil, err
	}

	// Load the template.
	if err := c.CompileTemplate(m.TemplateFuncs(c)); err != nil {
		return nil, err
	}

	// Load any media/attachments.
	if err := m.attachMedia(c); err != nil {
		return nil, err
	}

	// AXM (LST-20): the campaign continues after its checkpoint. Find out who above
	// the checkpoint already got it.
	alreadySent, err := m.sentAfterCheckpoint(c)
	if err != nil {
		return nil, err
	}

	// Add the campaign to the active map.
	p := &pipe{
		camp: c,
		rate: ratecounter.NewRateCounter(time.Minute),
		wg:   &sync.WaitGroup{},
		m:    m,

		readID:      c.LastSubscriberID,
		prog:        newProgress(c.LastSubscriberID),
		alreadySent: alreadySent,
		saved:       c.LastSubscriberID,
	}

	// Increment the waitgroup so that Wait() blocks immediately. This is necessary
	// as a campaign pipe is created first and subscribers/messages under it are
	// fetched asynchronolusly later. The messages each add to the wg and that
	// count is used to determine the exhaustion/completion of all messages.
	p.wg.Add(1)

	go func() {
		// Wait for all the messages in the campaign to be processed
		// (successfully or skipped after errors or cancellation).
		p.wg.Wait()

		p.cleanup()
	}()

	m.pipesMut.Lock()
	m.pipes[c.ID] = p

	// AXM (LST-20): Close() has stopped the pipes that existed when it was called.
	// One that is created after that starts out stopped.
	if m.closing.Load() {
		p.Stop(false)
	}
	m.pipesMut.Unlock()
	return p, nil
}

// NextSubscribers processes the next batch of subscribers in a given campaign.
// It returns a bool indicating whether any subscribers were processed
// in the current batch or not. A false indicates that all subscribers
// have been processed, or that a campaign has been paused or cancelled.
func (p *pipe) NextSubscribers() (bool, error) {
	// Fetch the next batch of subscribers from a 'running' campaign.
	// AXM (LST-20): after the position this run has read up to. The fetch no longer
	// moves the campaign's checkpoint in the DB.
	subs, err := p.m.store.NextSubscribers(p.camp.ID, p.readID, p.m.cfg.BatchSize)
	if err != nil {
		return false, fmt.Errorf("error fetching campaign subscribers (%s): %v", p.camp.Name, err)
	}

	// There are no subscribers from the query. Either all subscribers on the campaign
	// have been processed, or the campaign has changed from 'running' to 'paused' or 'cancelled'.
	if len(subs) == 0 {
		return false, nil
	}

	// Is there a sliding window limit configured?
	hasSliding := p.m.cfg.SlidingWindow &&
		p.m.cfg.SlidingWindowRate > 0 &&
		p.m.cfg.SlidingWindowDuration.Seconds() > 1

	// AXM (LST-20): the batch is in ascending subscriber ID order.
	p.readID = subs[len(subs)-1].ID

	// Push messages.
	for _, s := range subs {
		// AXM (LST-20): an earlier run of the campaign already sent this subscriber.
		if _, ok := p.alreadySent[s.ID]; ok {
			continue
		}

		msg, err := p.newMessage(s)
		if err != nil {
			p.m.log.Printf("error rendering message (%s) (%s): %v", p.camp.Name, s.Email, err)
			continue
		}

		// Push the message to the queue while blocking and waiting until
		// the queue is drained.
		p.m.campMsgQ <- msg

		// Check if the sliding window is active.
		if hasSliding {
			diff := time.Since(p.m.slidingStart)

			// Window has expired. Reset the clock.
			if diff >= p.m.cfg.SlidingWindowDuration {
				p.m.slidingStart = time.Now()
				p.m.slidingCount = 0
			}

			// Have the messages exceeded the limit?
			p.m.slidingCount++
			if p.m.slidingCount >= p.m.cfg.SlidingWindowRate {
				wait := p.m.cfg.SlidingWindowDuration - diff

				p.m.log.Printf("messages exceeded (%d) for the window (%v since %s). Sleeping for %s.",
					p.m.slidingCount,
					p.m.cfg.SlidingWindowDuration,
					p.m.slidingStart.Format(time.RFC822Z),
					wait.Round(time.Second)*1)

				p.m.slidingCount = 0
				time.Sleep(wait)
			}
		}
	}

	return true, nil
}

// OnError keeps track of the number of errors that occur while sending messages
// and pauses the campaign if the error threshold is met.
func (p *pipe) OnError() {
	if p.m.cfg.MaxSendErrors < 1 {
		return
	}

	// If the error threshold is met, pause the campaign.
	count := p.errors.Add(1)
	if int(count) < p.m.cfg.MaxSendErrors {
		return
	}

	p.Stop(true)
	p.m.log.Printf("error count exceeded %d. pausing campaign %s", p.m.cfg.MaxSendErrors, p.camp.Name)
}

// Stop "marks" a campaign as stopped. It doesn't actually stop the processing
// of messages. That happens when every queued message in the campaign is processed,
// marking .wg, the waitgroup counter as done. That triggers cleanup().
func (p *pipe) Stop(withErrors bool) {
	// Already stopped.
	if p.stopped.Load() {
		return
	}

	if withErrors {
		p.withErrors.Store(true)
	}

	p.stopped.Store(true)
}

// newMessage returns a campaign message while internally incrementing the
// number of messages in the pipe wait group so that the status of every
// message can be atomically tracked.
func (p *pipe) newMessage(s models.Subscriber) (CampaignMessage, error) {
	msg, err := p.m.NewCampaignMessage(p.camp, s)
	if err != nil {
		return msg, err
	}

	msg.pipe = p
	p.wg.Add(1)

	// AXM (LST-20): the checkpoint waits for this message from here on.
	p.prog.queued(s.ID)

	return msg, nil
}

// sentAfterCheckpoint returns the subscribers above a campaign's checkpoint who already
// have a send record for it (AXM, LST-20). When a campaign is picked up again after a
// restart, a pause or a stop, it reads from the checkpoint, and messages that went out
// ahead of the checkpoint must not go out twice. An error means it is not known who
// was sent, and the campaign must not start.
func (m *Manager) sentAfterCheckpoint(c *models.Campaign) (map[int]struct{}, error) {
	if m.sends == nil {
		return nil, nil
	}

	// The records of an earlier run in this process may still be queued.
	if !m.sends.flush(sentIDsFlushWait) {
		return nil, fmt.Errorf("send records are still being written. campaign %s starts once they are", c.Name)
	}

	ids, err := m.store.GetCampaignSentIDs(c.ID, c.LastSubscriberID)
	if err != nil {
		return nil, fmt.Errorf("error reading send records of campaign %s: %v", c.Name, err)
	}

	// An app that was killed did not save the count of its last moment, and the
	// campaign would end with fewer "sent" than recipients although everyone got it.
	// The records are per person, so raise the count to their number. It is never
	// lowered: a campaign can have fewer records than messages (records that could
	// not be written, a campaign that began before records existed).
	if n, err := m.store.CountCampaignSends(c.ID); err == nil && n > c.Sent {
		if err := m.store.UpdateCampaignCounts(c.ID, 0, n-c.Sent, 0); err != nil {
			m.log.Printf("error updating campaign counts (%s): %v", c.Name, err)
		} else {
			m.log.Printf("campaign (%s) sent count raised from %d to its %d send records", c.Name, c.Sent, n)
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}

	out := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	m.log.Printf("campaign (%s) continues after subscriber %d. %d subscribers above it were already sent and are left out",
		c.Name, c.LastSubscriberID, len(out))
	return out, nil
}

// saveProgress writes the campaign's checkpoint and the number of messages sent since
// the last save to the store (AXM, LST-20). The periodic saver, cleanup() and
// Manager.Close() all go through it, so no count is written twice and the checkpoint
// in the store never moves back.
func (p *pipe) saveProgress() {
	p.saveMut.Lock()
	defer p.saveMut.Unlock()

	var (
		checkpoint = p.prog.get()
		sent       = p.sent.Swap(0)
	)
	if sent == 0 && checkpoint == p.saved {
		return
	}

	// The store leaves last_subscriber_id alone when it is given 0.
	lastID := 0
	if checkpoint != p.saved {
		lastID = checkpoint
	}
	if err := p.m.store.UpdateCampaignCounts(p.camp.ID, 0, int(sent), lastID); err != nil {
		p.sent.Add(sent)
		p.m.log.Printf("error updating campaign counts (%s): %v", p.camp.Name, err)
		return
	}
	p.saved = checkpoint
}

// logSendRecords logs, for a campaign that just finished, how many send records it has
// next to its sent and to-send counts. Fewer records than messages sent means records
// were lost. Fewer messages sent than there were to send means recipients were skipped,
// and the records tell which ones.
func (p *pipe) logSendRecords(c *models.Campaign) {
	if p.m.sends == nil {
		return
	}

	// Let the records of the last messages reach the DB first.
	if !p.m.sends.flush(5 * time.Second) {
		p.m.log.Printf("campaign (%s) send records are still being written", c.Name)
	}

	n, err := p.m.store.CountCampaignSends(c.ID)
	if err != nil {
		p.m.log.Printf("error counting send records of campaign (%s): %v", c.Name, err)
		return
	}

	p.m.log.Printf("campaign (%s) send records: %d recorded, %d sent, %d to send", c.Name, n, c.Sent, c.ToSend)
}

// cleanup finishes the campaign and updates the campaign status in the DB
// and also triggers a notification to the admin. This only triggers once
// a pipe's wg counter is fully exhausted, draining all messages in its queue.
func (p *pipe) cleanup() {
	defer func() {
		p.m.pipesMut.Lock()
		delete(p.m.pipes, p.camp.ID)
		p.m.pipesMut.Unlock()
	}()

	// Update campaign's 'sent count.
	// AXM (LST-20): and its checkpoint, which only covers subscribers the campaign is
	// done with. Upstream wrote the highest subscriber ID that was sent, and nothing
	// when no message had gone out, which left the read-ahead position in the DB.
	p.saveProgress()

	// The campaign was auto-paused due to errors.
	if p.withErrors.Load() {
		if err := p.m.store.UpdateCampaignStatus(p.camp.ID, models.CampaignStatusPaused); err != nil {
			p.m.log.Printf("error updating campaign (%s) status to %s: %v", p.camp.Name, models.CampaignStatusPaused, err)
		} else {
			p.m.log.Printf("set campaign (%s) to %s", p.camp.Name, models.CampaignStatusPaused)
		}

		_ = p.m.sendNotif(p.camp, models.CampaignStatusPaused, "Too many errors")
		return
	}

	// The campaign was manually stopped (pause, cancel).
	if p.stopped.Load() {
		p.m.log.Printf("stop processing campaign (%s)", p.camp.Name)
		return
	}

	// Campaign wasn't manually stopped and subscribers were naturally exhausted.
	// Fetch the up-to-date campaign status from the DB.
	c, err := p.m.store.GetCampaign(p.camp.ID)
	if err != nil {
		p.m.log.Printf("error fetching campaign (%s) for ending: %v", p.camp.Name, err)
		return
	}

	// If a running campaign has exhausted subscribers, it's finished.
	if c.Status == models.CampaignStatusRunning || c.Status == models.CampaignStatusScheduled {
		c.Status = models.CampaignStatusFinished
		if err := p.m.store.UpdateCampaignStatus(p.camp.ID, models.CampaignStatusFinished); err != nil {
			p.m.log.Printf("error finishing campaign (%s): %v", p.camp.Name, err)
		} else {
			p.m.log.Printf("campaign (%s) finished", p.camp.Name)
		}

		// Off this goroutine: cleanup must not take longer because of it.
		go p.logSendRecords(c)
	} else {
		p.m.log.Printf("finish processing campaign (%s)", p.camp.Name)
	}

	// Notify admin.
	_ = p.m.sendNotif(c, c.Status, "")
}
