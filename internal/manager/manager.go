package manager

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/mail"
	"net/textproto"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"maps"

	"github.com/Masterminds/sprig/v3"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// attribInlineEmbed is the HTML attrib used to mark <img> tags whose source
// should be embedded as a multipart cid inline attachment.
const attribInlineEmbed = "data-embed"

var (
	reInlineImage = regexp.MustCompile(`(?is)<img\b[^>]*\b` + attribInlineEmbed + `\b[^>]*>`)
	reImgSrc      = regexp.MustCompile(`(?is)(\s)src\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

const (
	// BaseTPL is the name of the base template.
	BaseTPL = "base"

	// ContentTpl is the name of the compiled message.
	ContentTpl = "content"

	dummyUUID = "00000000-0000-0000-0000-000000000000"
)

// Store represents a data backend, such as a database,
// that provides subscriber and campaign records.
type Store interface {
	NextCampaigns(currentIDs []int64, sentCounts []int64) ([]*models.Campaign, error)

	// NextSubscribers returns the next batch of a running campaign's subscribers
	// after the subscriber ID lastSubID, in ascending ID order. AXM (LST-20): the
	// caller keeps the read position. Upstream kept it in the DB and moved it on
	// every read.
	NextSubscribers(campID, lastSubID, limit int) ([]models.Subscriber, error)
	GetCampaign(campID int) (*models.Campaign, error)
	GetAttachment(mediaID int) (models.Attachment, error)
	GetInlineAttachmentByFilename(filename string) (models.Attachment, string, error)
	UpdateCampaignStatus(campID int, status string) error
	UpdateCampaignCounts(campID int, toSend int, sent int, lastSubID int) error
	CreateLink(url string) (string, error)
	BlocklistSubscriber(id int64) error
	DeleteSubscriber(id int64) error

	// RecordCampaignSends saves the records of campaign messages that were sent.
	RecordCampaignSends(sends []models.CampaignSend) error

	// CountCampaignSends returns the number of send records of a campaign.
	CountCampaignSends(campID int) (int, error)

	// GetCampaignSentIDs returns the subscribers above the ID lastSubID that have a
	// send record for a campaign (AXM, LST-20).
	GetCampaignSentIDs(campID, lastSubID int) ([]int, error)

	// HoldCampaignLock reports whether this app instance may send campaigns. It takes
	// the lock if it is free and checks that a lock taken earlier is still held. Only
	// one instance holds it at a time, and it is released when the instance goes away
	// (AXM, LST-20).
	HoldCampaignLock() (bool, error)

	// ReleaseCampaignLock gives the lock up.
	ReleaseCampaignLock()
}

// Messenger is an interface for a generic messaging backend,
// for instance, e-mail, SMS etc.
type Messenger interface {
	Name() string
	Push(models.Message) error
	Flush() error
	Close() error
}

// CampStats contains campaign stats like per minute send rate.
type CampStats struct {
	SendRate int
}

// Manager handles the scheduling, processing, and queuing of campaigns
// and message pushes.
type Manager struct {
	cfg        Config
	store      Store
	i18n       *i18n.I18n
	messengers map[string]Messenger
	fnNotify   func(subject string, data any) error
	log        *log.Logger

	// Campaigns that are currently running.
	pipes    map[int]*pipe
	pipesMut sync.RWMutex

	tpls    map[int]*models.Template
	tplsMut sync.RWMutex

	// Links generated using Track() are cached here so as to not query
	// the database for the link UUID for every message sent. This has to
	// be locked as it may be used externally when previewing campaigns.
	links    map[string]string
	linksMut sync.RWMutex

	nextPipes chan *pipe
	campMsgQ  chan CampaignMessage
	msgQ      chan models.Message

	// Sliding window keeps track of the total number of messages sent in a period
	// and on reaching the specified limit, waits until the window is over before
	// sending further messages.
	slidingCount int
	slidingStart time.Time

	// Records who was sent which campaign. Nil in a Manager that was not built by New().
	sends *sendLog

	// AXM (LST-20). closing is set once Close() is called: no campaign message is
	// handed to a messenger after that. inflight counts the campaign messages that
	// are with a messenger right now. lockHeld and lockWaiting belong to the
	// scanCampaigns goroutine.
	closing     atomic.Bool
	inflight    atomic.Int64
	lockHeld    bool
	lockWaiting bool
	saveEvery   time.Duration

	tplFuncs template.FuncMap
}

// CampaignMessage represents an instance of campaign message to be pushed out,
// specific to a subscriber, via the campaign's messenger.
type CampaignMessage struct {
	Campaign   *models.Campaign
	Subscriber models.Subscriber

	from     string
	to       string
	subject  string
	body     []byte
	altBody  []byte
	unsubURL string
	headers  models.Headers

	pipe *pipe
}

// Config has parameters for configuring the manager.
type Config struct {
	// Number of subscribers to pull from the DB in a single iteration.
	BatchSize             int
	Concurrency           int
	MessageRate           int
	MaxSendErrors         int
	SlidingWindow         bool
	SlidingWindowDuration time.Duration
	SlidingWindowRate     int
	RequeueOnError        bool
	FromEmail             string
	IndividualTracking    bool
	DisableTracking       bool
	LinkTrackURL          string
	UnsubURL              string
	OptinURL              string
	MessageURL            string
	ViewTrackURL          string
	ArchiveURL            string
	RootURL               string
	SenderPolicyEnabled   bool
	UnsubHeader           bool

	// Pre-rendered business footer. Empty bodies disable insertion.
	BusinessFooterHTML   []byte
	BusinessFooterText   []byte
	BusinessFooterMarker string

	// Interval to scan the DB for active campaign checkpoints.
	ScanInterval time.Duration

	// ScanCampaigns indicates whether this instance of manager will scan the DB
	// for active campaigns and process them.
	// This can be used to run multiple instances of listmonk
	// (exposed to the internet, private etc.) where only one does campaign
	// processing while the others handle other kinds of traffic.
	ScanCampaigns bool
}

var pushTimeout = time.Second * 3

// New returns a new instance of Mailer.
func New(cfg Config, store Store, i *i18n.I18n, l *log.Logger) *Manager {
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 1000
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.MessageRate < 1 {
		cfg.MessageRate = 1
	}

	m := &Manager{
		cfg:   cfg,
		store: store,
		i18n:  i,
		fnNotify: func(subject string, data any) error {
			return notifs.NotifySystem(subject, notifs.TplCampaignStatus, data, nil)
		},
		log:          l,
		messengers:   make(map[string]Messenger),
		pipes:        make(map[int]*pipe),
		tpls:         make(map[int]*models.Template),
		links:        make(map[string]string),
		nextPipes:    make(chan *pipe, 1000),
		campMsgQ:     make(chan CampaignMessage, cfg.Concurrency*cfg.MessageRate*2),
		msgQ:         make(chan models.Message, cfg.Concurrency*cfg.MessageRate*2),
		slidingStart: time.Now(),
		sends:        newSendLog(store, l),
		saveEvery:    progressSaveInterval,
	}
	m.tplFuncs = m.makeGnericFuncMap()

	return m
}

// AddMessenger adds a Messenger messaging backend to the manager.
func (m *Manager) AddMessenger(msg Messenger) error {
	id := msg.Name()
	if _, ok := m.messengers[id]; ok {
		return fmt.Errorf("messenger '%s' is already loaded", id)
	}
	m.messengers[id] = msg

	return nil
}

// PushMessage pushes an arbitrary non-campaign Message to be sent out by the workers.
// It times out if the queue is busy.
func (m *Manager) PushMessage(msg models.Message) error {
	t := time.NewTicker(pushTimeout)
	defer t.Stop()

	select {
	case m.msgQ <- msg:
	case <-t.C:
		m.log.Printf("message push timed out: '%s'", msg.Subject)
		return errors.New("message push timed out")
	}

	return nil
}

// PushCampaignMessage pushes a campaign messages into a queue to be sent out by the workers.
// It times out if the queue is busy.
func (m *Manager) PushCampaignMessage(msg CampaignMessage) error {
	t := time.NewTicker(pushTimeout)
	defer t.Stop()

	// Load any media/attachments.
	if err := m.attachMedia(msg.Campaign); err != nil {
		return err
	}

	select {
	case m.campMsgQ <- msg:
	case <-t.C:
		m.log.Printf("message push timed out: '%s'", msg.Subject())
		return errors.New("message push timed out")
	}

	return nil
}

// HasMessenger checks if a given messenger is registered.
func (m *Manager) HasMessenger(id string) bool {
	_, ok := m.messengers[id]

	return ok
}

// HasRunningCampaigns checks if there are any active campaigns.
func (m *Manager) HasRunningCampaigns() bool {
	m.pipesMut.Lock()
	defer m.pipesMut.Unlock()

	return len(m.pipes) > 0
}

// GetCampaignStats returns campaign statistics.
func (m *Manager) GetCampaignStats(id int) CampStats {
	n := 0

	m.pipesMut.Lock()
	if c, ok := m.pipes[id]; ok {
		n = int(c.rate.Rate())
	}
	m.pipesMut.Unlock()

	return CampStats{SendRate: n}
}

// Run is a blocking function (that should be invoked as a goroutine)
// that scans the data source at regular intervals for pending campaigns,
// and queues them for processing. The process queue fetches batches of
// subscribers and pushes messages to them for each queued campaign
// until all subscribers are exhausted, at which point, a campaign is marked
// as "finished".
func (m *Manager) Run() {
	if m.cfg.ScanCampaigns {
		// Periodically scan campaigns and push running campaigns to nextPipes
		// to fetch subscribers from the campaign.
		go m.scanCampaigns(m.cfg.ScanInterval)
	}

	// Start the writer of campaign send records.
	if m.sends != nil {
		go m.sends.run()
	}

	// AXM (LST-20): keep the checkpoints of the running campaigns saved.
	if m.saveEvery > 0 {
		go m.saveProgress(m.saveEvery)
	}

	// Spawn N message workers.
	for i := 0; i < m.cfg.Concurrency; i++ {
		go m.worker()
	}

	// Indefinitely wait on the pipe queue to fetch the next set of subscribers
	// for any active campaigns.
	for p := range m.nextPipes {
		// AXM (LST-20): the app is going down, or the pipe was stopped (too many
		// errors, a lost lock). Read nothing more and let the pipe go. Upstream kept
		// reading a stopped campaign that was still 'running' in the DB to its end,
		// dropping every message.
		if m.closing.Load() || p.stopped.Load() {
			p.Stop(false)
			p.wg.Done()
			continue
		}

		has, err := p.NextSubscribers()
		if err != nil {
			m.log.Printf("error processing campaign batch (%s): %v", p.camp.Name, err)

			// If the batch fails, stop the pipe and release it so that it doesn't hang forever.
			// The cleanup() records the state in DB and scanCampaigns() picks it up at a later point.
			p.Stop(false)
			p.wg.Done()
			continue
		}

		if has {
			// There are more subscribers to fetch. Queue again.
			select {
			case m.nextPipes <- p:
			default:
				// If the queue is full for any reason, stop the pipe and release it.
				// The cleanup() records the state in DB and scanCampaigns() picks it up
				// at a later point.
				p.Stop(false)
				p.wg.Done()
			}
		} else {
			// The pipe is created with a +1 on the waitgroup pseudo counter
			// so that it immediately waits. Subsequently, every message created
			// is incremented in the counter in pipe.newMessage(), and when it's'
			// processed (or ignored when a campaign is paused or cancelled),
			// the count is's reduced in worker().
			//
			// This marks down the original non-message +1, causing the waitgroup
			// to be released and the pipe to end, triggering the pg.Wait()
			// in newPipe() that calls pipe.cleanup().
			p.wg.Done()
		}
	}
}

// CacheTpl caches a template for ad-hoc use. This is currently only used by tx templates.
func (m *Manager) CacheTpl(id int, tpl *models.Template) {
	if body, atts := m.ApplyInlineImages(tpl.Body); len(atts) > 0 {
		tpl.Body = body
		tpl.Attachments = atts
		if err := tpl.Compile(m.GenericTemplateFuncs()); err != nil {
			m.log.Printf("error recompiling tx template %d after inline image: %v", id, err)
			return
		}
	}

	m.tplsMut.Lock()
	m.tpls[id] = tpl
	m.tplsMut.Unlock()
}

// DeleteTpl deletes a cached template.
func (m *Manager) DeleteTpl(id int) {
	m.tplsMut.Lock()
	delete(m.tpls, id)
	m.tplsMut.Unlock()
}

// GetTpl returns a cached template.
func (m *Manager) GetTpl(id int) (*models.Template, error) {
	m.tplsMut.RLock()
	tpl, ok := m.tpls[id]
	m.tplsMut.RUnlock()

	if !ok {
		return nil, fmt.Errorf("template %d not found", id)
	}

	return tpl, nil
}

// TemplateFuncs returns the template functions to be applied into
// compiled campaign templates.
func (m *Manager) TemplateFuncs(c *models.Campaign) template.FuncMap {
	f := template.FuncMap{
		"TrackLink": m.trackMessageLink,
		"TrackView": m.trackMessageView,
		"UnsubscribeURL": func(msg *CampaignMessage) string {
			return msg.unsubURL
		},
		"ManageURL": func(msg *CampaignMessage) string {
			return msg.unsubURL + "?manage=true"
		},
		"OptinURL": func(msg *CampaignMessage) string {
			// Add list IDs.
			// TODO: Show private lists list on optin e-mail
			return fmt.Sprintf(m.cfg.OptinURL, msg.Subscriber.UUID, "")
		},
		"MessageURL": func(msg *CampaignMessage) string {
			return fmt.Sprintf(m.cfg.MessageURL, c.UUID, msg.Subscriber.UUID)
		},
		"ArchiveURL": func() string {
			return m.cfg.ArchiveURL
		},
		"RootURL": func() string {
			return m.cfg.RootURL
		},
	}

	maps.Copy(f, m.tplFuncs)

	return f
}

func (m *Manager) GenericTemplateFuncs() template.FuncMap {
	return m.tplFuncs
}

// StopCampaign marks a running campaign as stopped so that all its queued messages are ignored.
func (m *Manager) StopCampaign(id int) {
	m.pipesMut.RLock()
	if p, ok := m.pipes[id]; ok {
		p.Stop(false)
	}
	m.pipesMut.RUnlock()
}

// Close closes and exits the campaign manager.
//
// AXM (LST-20): it first stops the running campaigns the way a pause does, waits for
// the messages that are with a messenger, and saves the send records, the checkpoints
// and the sent counts, so that the next start continues exactly where this one stopped.
// The campaigns stay 'running' in the DB.
func (m *Manager) Close() {
	m.closing.Store(true)
	pipes := m.stopPipes()

	deadline := time.Now().Add(closeDrainWait)
	for m.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := m.inflight.Load(); n > 0 {
		m.log.Printf("%d campaign messages were still being sent when closing. they may be sent again on the next start", n)
	}

	// AXM (LST-20): nextPipes is not closed any more. The reader loop in Run() puts a
	// pipe back on it after every batch and would panic on a closed channel in the
	// middle of this shutdown. The loop stops reading on the closing mark instead.
	close(m.msgQ)

	// Write the send records that are still queued. The caller closes the DB next.
	if m.sends != nil && !m.sends.flush(1500*time.Millisecond) {
		m.log.Printf("error recording campaign sends: queued send records were not written before closing")
	}

	for _, p := range pipes {
		p.saveProgress()
	}
	if len(pipes) > 0 {
		m.log.Printf("stopped %d running campaigns for closing. they continue on the next start", len(pipes))
	}

	if m.store != nil {
		m.store.ReleaseCampaignLock()
	}
}

// stopPipes marks every campaign that is being processed as stopped, like a pause does,
// and returns them (AXM, LST-20). Queued messages of a stopped campaign are dropped,
// which leaves them above the checkpoint, and its cleanup() does not finish the campaign.
func (m *Manager) stopPipes() []*pipe {
	m.pipesMut.RLock()
	defer m.pipesMut.RUnlock()

	out := make([]*pipe, 0, len(m.pipes))
	for _, p := range m.pipes {
		p.Stop(false)
		out = append(out, p)
	}
	return out
}

// saveProgress is a blocking function that saves the checkpoint and the sent count of
// every campaign that is being processed, at an interval (AXM, LST-20). After the app
// is killed, a campaign continues from the checkpoint that was saved last, and the
// send records above it tell who was already sent.
func (m *Manager) saveProgress(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for range t.C {
		if m.closing.Load() {
			return
		}

		m.pipesMut.RLock()
		pipes := make([]*pipe, 0, len(m.pipes))
		for _, p := range m.pipes {
			pipes = append(pipes, p)
		}
		m.pipesMut.RUnlock()

		for _, p := range pipes {
			p.saveProgress()
		}
	}
}

// holdCampaignLock reports whether this instance may send campaigns now (AXM, LST-20).
// A campaign continues from its saved checkpoint, so two instances that both process
// campaigns, like the old and the new one during a redeploy, would send every message
// twice. The store's lock lets one instance in. When the lock is lost, the campaigns
// that are being processed here are stopped and continue once it is held again.
func (m *Manager) holdCampaignLock() bool {
	held, err := m.store.HoldCampaignLock()
	if held {
		if m.lockWaiting {
			m.log.Printf("this instance sends campaigns now")
		}
		m.lockHeld, m.lockWaiting = true, false
		return true
	}

	if m.lockHeld {
		m.lockHeld = false
		n := len(m.stopPipes())
		m.log.Printf("lost the campaign sending lock (%v). stopped %d running campaigns. they continue when the lock is held again", err, n)
		return false
	}

	if !m.lockWaiting {
		m.lockWaiting = true
		if err != nil {
			m.log.Printf("error taking the campaign sending lock: %v. no campaign is sent until it is held", err)
		} else {
			m.log.Printf("another instance is sending campaigns. this one starts when that one stops")
		}
	}
	return false
}

// scanCampaigns is a blocking function that periodically scans the data source
// for campaigns to process and dispatches them to the manager. It feeds campaigns
// into nextPipes.
func (m *Manager) scanCampaigns(tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()

	// Periodically scan the data source for campaigns to process.
	for range t.C {
		// AXM (LST-20): nothing is started while closing, or while another
		// instance is the one sending campaigns.
		if m.closing.Load() {
			return
		}
		if !m.holdCampaignLock() {
			continue
		}

		ids, counts := m.getCurrentCampaigns()
		campaigns, err := m.store.NextCampaigns(ids, counts)
		if err != nil {
			m.log.Printf("error fetching campaigns: %v", err)
			continue
		}

		for _, c := range campaigns {
			// Create a new pipe that'll handle this campaign's states.
			p, err := m.newPipe(c)
			if err != nil {
				m.log.Printf("error processing campaign (%s): %v", c.Name, err)
				continue
			}
			m.log.Printf("start processing campaign (%s)", c.Name)

			// If subscriber processing is busy, move on. Blocking and waiting
			// can end up in a race condition where the waiting campaign's
			// state in the data source has changed.
			select {
			case m.nextPipes <- p:
			default:
				// If the queue is full for any reason, stop the pipe and release it.
				// The cleanup() records the state in DB and scanCampaigns() picks it up
				// at a later point.
				p.Stop(false)
				p.wg.Done()
			}
		}
	}
}

// worker is a blocking function that perpetually listents to events (message) on different
// queues and processes them.
func (m *Manager) worker() {
	// Counter to keep track of the message / sec rate limit.
	numMsg := 0
	for {
		select {
		// Campaign message.
		case msg, ok := <-m.campMsgQ:
			if !ok {
				return
			}

			// If the campaign has ended or stopped, ignore the message.
			if msg.pipe != nil && msg.pipe.stopped.Load() {
				// Reduce the message counter on the pipe.
				msg.pipe.wg.Done()
				continue
			}

			// Pause on hitting the message rate.
			if numMsg >= m.cfg.MessageRate {
				time.Sleep(time.Second)
				numMsg = 0
			}
			numMsg++

			// AXM (LST-20): from here on the message counts as being with the
			// messenger, and Close() waits for it. The stop mark is looked at
			// once more after counting, so that a message is either dropped
			// here or seen by Close().
			m.inflight.Add(1)
			if msg.pipe != nil && msg.pipe.stopped.Load() {
				m.inflight.Add(-1)
				msg.pipe.wg.Done()
				continue
			}

			// Outgoing message.
			out := models.Message{
				From:        msg.from,
				To:          []string{msg.to},
				Subject:     msg.subject,
				ContentType: msg.Campaign.ContentType,
				Body:        msg.body,
				AltBody:     msg.altBody,
				Subscriber:  msg.Subscriber,
				Campaign:    msg.Campaign,
				Attachments: msg.Campaign.Attachments,
			}

			h := textproto.MIMEHeader{}
			h.Set(models.EmailHeaderCampaignUUID, msg.Campaign.UUID)
			h.Set(models.EmailHeaderSubscriberUUID, msg.Subscriber.UUID)

			// Attach List-Unsubscribe headers?
			if m.cfg.UnsubHeader {
				h.Set("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
				h.Set("List-Unsubscribe", `<`+msg.unsubURL+`>`)
			}

			// Attach any custom headers.
			for _, set := range msg.headers {
				for hdr, val := range set {
					h.Add(hdr, val)
				}
			}

			if m.cfg.SenderPolicyEnabled && msg.Campaign.ReplyTo != "" && (msg.Campaign.Messenger == "email" || strings.HasPrefix(msg.Campaign.Messenger, "email-")) {
				replyTo := msg.Campaign.ReplyTo
				if address, err := mail.ParseAddress(replyTo); err == nil && address.Name != "" {
					replyTo = address.String()
				}
				h.Set("Reply-To", replyTo)
			}
			// Set the headers.
			out.Headers = h

			// Push the message to the messenger.
			err := m.messengers[msg.Campaign.Messenger].Push(out)
			if err != nil {
				m.log.Printf("error sending message in campaign %s: subscriber %d: %v", msg.Campaign.Name, msg.Subscriber.ID, err)
			}

			// Increment the send rate or the error counter if there was an error.
			if msg.pipe != nil {
				// AXM: record who was sent this campaign. This only queues the
				// record, so it can neither wait nor fail. It is queued before
				// the message is marked as done so that the campaign's cleanup
				// never runs ahead of its last record.
				if err == nil && m.sends != nil {
					m.sends.add(msg.Campaign.ID, msg.Subscriber.ID)
				}

				// AXM (LST-20): settle the message for the checkpoint and count
				// it before it is marked as done, so that the campaign's cleanup
				// saves both. Upstream kept the highest subscriber ID that was
				// sent, with a load and a store that two workers could interleave,
				// and counted the message after marking it as done.
				msg.pipe.prog.done(msg.Subscriber.ID, err == nil)
				if err == nil {
					msg.pipe.sent.Add(1)
				}

				// Mark the message as done.
				msg.pipe.wg.Done()

				if err != nil {
					// Call the error callback, which keeps track of the error count
					// and stops the campaign if the error count exceeds the threshold.
					msg.pipe.OnError()
				} else {
					msg.pipe.rate.Incr(1)
				}
			}
			m.inflight.Add(-1)

		// Arbitrary message.
		case msg, ok := <-m.msgQ:
			if !ok {
				return
			}

			// Push the message to the messenger.
			if err := m.messengers[msg.Messenger].Push(msg); err != nil {
				m.log.Printf("error sending message '%s': %v", msg.Subject, err)
			}
		}
	}
}

// getCurrentCampaigns returns the IDs of campaigns currently being processed
// and their sent counts.
func (m *Manager) getCurrentCampaigns() ([]int64, []int64) {
	// Needs to return an empty slice in case there are no campaigns.
	m.pipesMut.RLock()
	defer m.pipesMut.RUnlock()

	var (
		ids    = make([]int64, 0, len(m.pipes))
		counts = make([]int64, 0, len(m.pipes))
	)
	for _, p := range m.pipes {
		ids = append(ids, int64(p.camp.ID))

		// Get the sent counts for campaigns and reset them to 0
		// as in the database, they're stored cumulatively (sent += $newSent).
		// AXM (LST-20): in one step. The progress saver takes the count the same
		// way, and a separate load and store would let both write it.
		counts = append(counts, p.sent.Swap(0))
	}

	return ids, counts
}

// trackLink register a URL and return its UUID to be used in message templates
// for tracking links.
func (m *Manager) trackLink(url, campUUID, subUUID string) string {
	if m.cfg.DisableTracking {
		return url
	}

	m.linksMut.RLock()
	if uu, ok := m.links[url]; ok {
		m.linksMut.RUnlock()
		return fmt.Sprintf(m.cfg.LinkTrackURL, uu, campUUID, subUUID)
	}
	m.linksMut.RUnlock()

	// Register link.
	uu, err := m.store.CreateLink(url)
	if err != nil {
		m.log.Printf("error registering tracking for link '%s': %v", url, err)

		// If the registration fails, fail over to the original URL.
		return url
	}

	m.linksMut.Lock()
	m.links[url] = uu
	m.linksMut.Unlock()

	return fmt.Sprintf(m.cfg.LinkTrackURL, uu, campUUID, subUUID)
}

// sendNotif sends a notification to registered admin e-mails.
func (m *Manager) sendNotif(c *models.Campaign, status, reason string) error {
	var (
		subject = fmt.Sprintf("%s: %s", cases.Title(language.Und).String(status), c.Name)
		data    = map[string]any{
			"ID":     c.ID,
			"Name":   c.Name,
			"Status": status,
			"Sent":   c.Sent,
			"ToSend": c.ToSend,
			"Reason": reason,
		}
	)

	return m.fnNotify(subject, data)
}

// makeGnericFuncMap returns a generic template func map with custom template
// functions and sprig template functions.
func (m *Manager) makeGnericFuncMap() template.FuncMap {
	funcs := template.FuncMap{
		"Date": func(layout string) string {
			if layout == "" {
				layout = time.ANSIC
			}
			return time.Now().Format(layout)
		},
		"L": func() *i18n.I18n {
			return m.i18n
		},
		"Safe": func(safeHTML string) template.HTML {
			return template.HTML(safeHTML)
		},
	}

	// Copy spring functions.
	sprigFuncs := sprig.GenericFuncMap()
	delete(sprigFuncs, "env")
	delete(sprigFuncs, "expandenv")
	delete(sprigFuncs, "getHostByName")

	maps.Copy(funcs, sprigFuncs)

	return funcs
}

// attachMedia loads any media/attachments from the media store and attaches
// the byte blobs to the campaign. Inline attachments are skipped as they're
// loaded earlier by LoadInlineImages().
func (m *Manager) attachMedia(c *models.Campaign) error {
	// Already loaded if any non-inline attachment is present.
	for _, a := range c.Attachments {
		if !a.IsInline {
			return nil
		}
	}

	for _, mid := range []int64(c.MediaIDs) {
		a, err := m.store.GetAttachment(int(mid))
		if err != nil {
			return fmt.Errorf("error fetching attachment %d on campaign %s: %v", mid, c.Name, err)
		}
		c.Attachments = append(c.Attachments, a)
	}

	return nil
}

// LoadInlineImages resolves any <img ... data-embed ...> tags in the campaign
// body and template body one time before CompileTemplate.
func (m *Manager) LoadInlineImages(c *models.Campaign) error {
	if c.ContentType == models.CampaignContentTypePlain {
		return nil
	}

	cidCache := make(map[string]string)
	body, atts := m.applyInlineImages(c.Body, cidCache)
	c.Body = body

	tplBody, tplAtts := m.applyInlineImages(c.TemplateBody, cidCache)
	c.TemplateBody = tplBody
	atts = append(atts, tplAtts...)

	c.Attachments = append(c.Attachments, atts...)
	return nil
}

// ApplyInlineImages scans body for <img ... data-embed ...> tags, resolves
// each unique src filename to a media item, attaches it as an inline part, and
// rewrites the matched img src to cid.
func (m *Manager) ApplyInlineImages(body string) (string, []models.Attachment) {
	return m.applyInlineImages(body, make(map[string]string))
}

func (m *Manager) applyInlineImages(body string, cache map[string]string) (string, []models.Attachment) {
	if !strings.Contains(body, attribInlineEmbed) {
		return body, nil
	}

	var atts []models.Attachment
	out := reInlineImage.ReplaceAllStringFunc(body, func(tag string) string {
		src := extractSrc(tag)
		if src == "" || strings.HasPrefix(strings.ToLower(src), "cid:") {
			return tag
		}

		fname := filenameFromSrc(src)
		if fname == "" {
			return tag
		}

		cid, ok := cache[src]
		if !ok {
			if a, c, err := m.store.GetInlineAttachmentByFilename(fname); err == nil {
				atts = append(atts, a)
				cid = c
			} else {
				m.log.Printf("inline image %q not embedded: %v", src, err)
			}
			cache[src] = cid
		}
		if cid == "" {
			return tag
		}
		return reImgSrc.ReplaceAllString(tag, `${1}src="cid:`+cid+`"`)
	})

	return out, atts
}

// MakeContentID returns a standard `Content-ID` value (without  the angle brackets).
func MakeContentID(key string) string {
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:8]) + "@email"
}

// filenameFromSrc extracts a media filename from an <img src> value (ignoring
// any URLs and just capturing the filename.
func filenameFromSrc(src string) string {
	if src == "" {
		return ""
	}
	p := src
	if u, err := url.Parse(src); err == nil && u.Path != "" {
		p = u.Path
	}
	return path.Base(p)
}

// MakeAttachmentHeader returns a textproto.MIMEHeader for an email
// attachment. Default encoding is base64  and contentType is application/octet-stream.
func MakeAttachmentHeader(filename, encoding, contentType string) textproto.MIMEHeader {
	return makeAttachmentHeader("attachment", filename, encoding, contentType, "")
}

// MakeInlineAttachmentHeader returns a textproto.MIMEHeader for an inline
// image attachment referenced from the HTML body via a cid URL.
func MakeInlineAttachmentHeader(filename, encoding, contentType, contentID string) textproto.MIMEHeader {
	return makeAttachmentHeader("inline", filename, encoding, contentType, contentID)
}

func makeAttachmentHeader(disposition, filename, encoding, contentType, contentID string) textproto.MIMEHeader {
	if encoding == "" {
		encoding = "base64"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", disposition+"; filename="+filename)
	h.Set("Content-Type", fmt.Sprintf("%s; name=\""+filename+"\"", contentType))
	h.Set("Content-Transfer-Encoding", encoding)
	if contentID != "" {
		h.Set("Content-ID", "<"+contentID+">")
	}
	return h
}

func extractSrc(tag string) string {
	m := reImgSrc.FindStringSubmatch(tag)
	if len(m) == 0 {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	return m[3]
}
