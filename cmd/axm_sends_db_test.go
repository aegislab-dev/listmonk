package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/goyesql/v2"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"golang.org/x/mod/semver"
)

// These tests run the real campaign manager, the real queries and the real store against
// a disposable Postgres named by AXM_TEST_DB_URL. Each test owns a separate schema.

// sendGapSQL lists the current recipients of a regular campaign who have no send record,
// up to the highest subscriber ID the campaign covered. FORK.md documents the same query
// for finding recipients that a run skipped.
const sendGapSQL = `
WITH target AS (
    SELECT sl.subscriber_id AS id FROM campaign_lists cl
    JOIN lists l ON l.id = cl.list_id AND l.axm_filter IS NULL
    JOIN subscriber_lists sl ON sl.list_id = l.id
    JOIN subscribers s ON s.id = sl.subscriber_id AND s.status != 'blocklisted'
    WHERE cl.campaign_id = $1
        AND CASE WHEN l.optin = 'double' THEN sl.status = 'confirmed' ELSE sl.status != 'unsubscribed' END
    UNION
    SELECT m.subscriber_id FROM campaign_lists cl
    JOIN lists l ON l.id = cl.list_id AND l.axm_filter IS NOT NULL
    CROSS JOIN LATERAL axm_dynamic_list_members(l.id) m
    WHERE cl.campaign_id = $1
)
SELECT t.id FROM target t
WHERE t.id <= (SELECT max_subscriber_id FROM campaigns WHERE id = $1)
    AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends x WHERE x.campaign_id = $1 AND x.subscriber_id = t.id)
ORDER BY t.id`

var initTestNotifs sync.Once

// syncBuffer is a log sink that several goroutines write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type sendsEnv struct {
	t      *testing.T
	db     *sqlx.DB
	q      *models.Queries
	core   *core.Core
	schema string
	store  *store

	listID   int
	campID   int
	campUUID string

	// eligible holds the subscribers the campaign should reach, in ID order.
	eligible []int
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}

func withSearchPath(t *testing.T, dsn, path string) string {
	t.Helper()
	if !strings.HasPrefix(dsn, "postgres") {
		return dsn + " search_path=" + path
	}
	u, err := urlpkg.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	v := u.Query()
	v.Set("search_path", path)
	u.RawQuery = v.Encode()
	return u.String()
}

// newSendsEnv installs schema.sql into a new schema, prepares every query the way the
// app does at boot, and creates one list with n reachable subscribers, one blocklisted
// and one unsubscribed subscriber, and a running campaign on that list.
func newSendsEnv(t *testing.T, n int) *sendsEnv {
	t.Helper()
	dsn := os.Getenv("AXM_TEST_DB_URL")
	if dsn == "" {
		t.Skip("AXM_TEST_DB_URL not set")
	}

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("axm_sends_%d", time.Now().UnixNano())
	// internal/core's DB tests set up the same database at the same time. The lock (one
	// implicit transaction for the whole string) keeps the two from creating the extension at once.
	db.MustExec("SELECT pg_advisory_xact_lock(741919); CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public")
	db.MustExec("CREATE SCHEMA " + pq.QuoteIdentifier(name))
	db.Close()

	// schema.sql starts with DROP statements, so install it with only the new schema on
	// the search path. public is added afterwards for pgcrypto.
	db, err = sqlx.Connect("postgres", withSearchPath(t, dsn, name))
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db.MustExec(string(raw))
	db.Close()

	db, err = sqlx.Connect("postgres", withSearchPath(t, dsn, name+",public"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	// The app reads rows in sqlx's unsafe mode (initDB), which tolerates columns that
	// a destination struct does not have.
	db = db.Unsafe()
	t.Cleanup(func() {
		db.Exec("DROP SCHEMA " + pq.QuoteIdentifier(name) + " CASCADE")
		db.Close()
	})

	qMap := goyesql.Queries{}
	files, err := filepath.Glob(filepath.Join(root, "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatal("no query files", err)
	}
	for _, f := range files {
		parsed, err := goyesql.ParseFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range parsed {
			qMap[k] = v
		}
	}
	q := prepareQueries(qMap, db, koanf.New("."))

	initTestNotifs.Do(func() {
		// No system e-mails: campaign status notifications return without sending.
		notifs.Initialize(notifs.Opt{}, nil, nil, log.New(io.Discard, "", 0))
	})

	e := &sendsEnv{t: t, db: db, q: q, schema: name}
	e.core = core.New(&core.Opt{DB: db, Queries: q, Log: log.New(io.Discard, "", 0), I18n: &i18n.I18n{}}, nil)
	e.store = newManagerStore(q, e.core, nil)

	e.get(&e.listID, `INSERT INTO lists (uuid, name, type, optin) VALUES (gen_random_uuid(), 'Synthetic', 'private', 'single') RETURNING id`)
	for i := 0; i < n; i++ {
		e.eligible = append(e.eligible, e.addSub("enabled", "unconfirmed"))
	}
	e.addSub("blocklisted", "confirmed")
	e.addSub("enabled", "unsubscribed")
	e.campID, e.campUUID = e.addCampaign("running")
	return e
}

// reinstall runs schema.sql again on the test schema, as a plain install does. It uses
// a connection that can only see the test schema, because schema.sql starts with DROPs.
func (e *sendsEnv) reinstall() {
	e.t.Helper()
	db, err := sqlx.Connect("postgres", withSearchPath(e.t, os.Getenv("AXM_TEST_DB_URL"), e.schema))
	if err != nil {
		e.t.Fatal(err)
	}
	defer db.Close()
	raw, err := os.ReadFile(filepath.Join(repoRoot(e.t), "schema.sql"))
	if err != nil {
		e.t.Fatal(err)
	}
	db.MustExec(string(raw))
}

func (e *sendsEnv) get(dest any, query string, args ...any) {
	e.t.Helper()
	if err := e.db.Get(dest, query, args...); err != nil {
		e.t.Fatal(err, query)
	}
}

func (e *sendsEnv) ids(query string, args ...any) []int {
	e.t.Helper()
	out := []int{}
	if err := e.db.Select(&out, query, args...); err != nil {
		e.t.Fatal(err, query)
	}
	return out
}

func (e *sendsEnv) addSub(status, subscription string) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status)
		VALUES (gen_random_uuid(), gen_random_uuid()::TEXT || '@example.invalid', 'Synthetic', $1::subscriber_status) RETURNING id`, status)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, $3::subscription_status)`, id, e.listID, subscription)
	return id
}

func (e *sendsEnv) addCampaign(status string) (int, string) {
	e.t.Helper()
	var c struct {
		ID   int    `db:"id"`
		UUID string `db:"uuid"`
	}
	e.get(&c, `INSERT INTO campaigns (uuid, name, subject, from_email, body, content_type, messenger, status)
		VALUES (gen_random_uuid(), 'Synthetic ' || gen_random_uuid()::TEXT, 'Subject', 'sender@example.invalid',
			'<p>Hello {{ .Subscriber.Name }}</p>', 'html', 'email', $1::campaign_status) RETURNING id, uuid`, status)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'Synthetic')`, c.ID, e.listID)
	return c.ID, c.UUID
}

func (e *sendsEnv) subUUID(id int) string {
	e.t.Helper()
	var u string
	e.get(&u, `SELECT uuid FROM subscribers WHERE id = $1`, id)
	return u
}

// recorded returns the subscribers that have a send record for the campaign.
func (e *sendsEnv) recorded() []int {
	e.t.Helper()
	return e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1 ORDER BY subscriber_id`, e.campID)
}

func (e *sendsEnv) campaign() (status string, sent, toSend int) {
	e.t.Helper()
	var c struct {
		Status string `db:"status"`
		Sent   int    `db:"sent"`
		ToSend int    `db:"to_send"`
	}
	e.get(&c, `SELECT status, sent, to_send FROM campaigns WHERE id = $1`, e.campID)
	return c.Status, c.Sent, c.ToSend
}

// waitCampaign waits for the campaign to reach a status and for its sent counter to
// reach at least n. The counter is not compared exactly: the engine adds to it from the
// periodic scan and from cleanup, which can land after the status changes, and can add a
// pipe's last count twice when a scan runs during cleanup.
func (e *sendsEnv) waitCampaign(status string, n int) {
	e.t.Helper()
	waitFor(e.t, fmt.Sprintf("campaign to be %s with at least %d sent", status, n), func() bool {
		s, sent, _ := e.campaign()
		return s == status && sent >= n
	})
}

func (e *sendsEnv) setStatus(status string) {
	e.t.Helper()
	if _, err := e.q.UpdateCampaignStatus.Exec(e.campID, status); err != nil {
		e.t.Fatal(err)
	}
}

// startManager runs a campaign manager like the app does, with small batches.
func (e *sendsEnv) startManager(st manager.Store, msgr manager.Messenger, logs io.Writer) *manager.Manager {
	e.t.Helper()
	m := manager.New(manager.Config{
		BatchSize:          10,
		Concurrency:        2,
		MessageRate:        1000,
		ScanInterval:       20 * time.Millisecond,
		ScanCampaigns:      true,
		IndividualTracking: true,
		FromEmail:          "sender@example.invalid",
		RootURL:            "https://lists.example.invalid",
		LinkTrackURL:       "https://lists.example.invalid/link/%s/%s/%s",
		ViewTrackURL:       "https://lists.example.invalid/campaign/%s/%s/px.png",
		UnsubURL:           "https://lists.example.invalid/subscription/%s/%s",
		MessageURL:         "https://lists.example.invalid/campaign/%s/%s",
		OptinURL:           "https://lists.example.invalid/subscription/optin/%s?%s",
	}, st, nil, log.New(logs, "", 0))
	if err := m.AddMessenger(msgr); err != nil {
		e.t.Fatal(err)
	}
	go m.Run()
	return m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gateMessenger stands in for the SMTP messenger. It notes who each message went to.
type gateMessenger struct {
	mu   sync.Mutex
	sent []int
	fail map[int]bool

	// allow, when set, lets one message through per value received (all when closed).
	allow chan struct{}

	// hangAfter, when above zero, makes every push after that many never return,
	// as if the process had died in the middle of sending.
	hangAfter int
	pushes    atomic.Int64
}

func (g *gateMessenger) Name() string { return "email" }
func (g *gateMessenger) Flush() error { return nil }
func (g *gateMessenger) Close() error { return nil }

func (g *gateMessenger) Push(m models.Message) error {
	if n := g.pushes.Add(1); g.hangAfter > 0 && int(n) > g.hangAfter {
		select {}
	}
	if g.allow != nil {
		<-g.allow
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail[m.Subscriber.ID] {
		return errors.New("test push failure")
	}
	g.sent = append(g.sent, m.Subscriber.ID)
	return nil
}

func (g *gateMessenger) sentIDs() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]int{}, g.sent...)
	sort.Ints(out)
	return out
}

func wantSame(t *testing.T, what string, got, want []int) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %v\n want %v", what, got, want)
	}
}

func minus(a, b []int) []int {
	in := map[int]bool{}
	for _, v := range b {
		in[v] = true
	}
	out := []int{}
	for _, v := range a {
		if !in[v] {
			out = append(out, v)
		}
	}
	return out
}

// Every message the messenger accepted gets exactly one record, and nothing else does.
func TestSendRecordsMatchDelivery(t *testing.T) {
	e := newSendsEnv(t, 57)
	failed := []int{e.eligible[3], e.eligible[20], e.eligible[56]}
	msgr := &gateMessenger{fail: map[int]bool{failed[0]: true, failed[1]: true, failed[2]: true}}
	logs := &syncBuffer{}
	e.startManager(e.store, msgr, logs)

	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})

	want := minus(e.eligible, failed)
	wantSame(t, "delivered", msgr.sentIDs(), want)
	wantSame(t, "recorded", e.recorded(), want)

	e.waitCampaign("finished", len(want))
	if _, _, toSend := e.campaign(); toSend != len(e.eligible) {
		t.Fatalf("to_send=%d", toSend)
	}
	if got := logs.String(); !strings.Contains(got, fmt.Sprintf("send records: %d recorded, ", len(want))) ||
		!strings.Contains(got, fmt.Sprintf(" sent, %d to send", len(e.eligible))) {
		t.Fatalf("unexpected send record summary:\n%s", got)
	}

	// sent_at is the time of sending on the database clock, and no reaction is set yet.
	var off int
	e.get(&off, `SELECT COUNT(*) FROM axm_campaign_sends WHERE sent_at > NOW() OR sent_at < NOW() - INTERVAL '1 minute'
		OR num_nonnulls(opened_at, last_opened_at, open_count, open_kind, open_kinds,
			clicked_at, last_clicked_at, click_count, click_kind, click_kinds) > 0`)
	if off != 0 {
		t.Fatalf("%d records have an unexpected time or reaction", off)
	}

	// The failed recipients are exactly what the gap query reports.
	wantSame(t, "gap", e.ids(sendGapSQL, e.campID), failed)
}

// Pausing and resuming neither loses nor repeats a record.
func TestSendRecordsPauseResume(t *testing.T) {
	e := newSendsEnv(t, 57)
	msgr := &gateMessenger{allow: make(chan struct{})}
	logs := &syncBuffer{}
	m := e.startManager(e.store, msgr, logs)

	// Let 15 messages out, then pause the way the campaign API does.
	for i := 0; i < 15; i++ {
		select {
		case msgr.allow <- struct{}{}:
		case <-time.After(20 * time.Second):
			t.Fatalf("the campaign did not start sending:\n%s", logs.String())
		}
	}
	waitFor(t, "15 deliveries", func() bool { return len(msgr.sentIDs()) == 15 })
	waitFor(t, "both workers to be in the middle of a message", func() bool { return msgr.pushes.Load() == 17 })
	e.setStatus(models.CampaignStatusPaused)
	m.StopCampaign(e.campID)

	// The two messages in flight still go out. Let them finish one after the other:
	// the engine's checkpoint update is not atomic across workers, and two messages
	// finishing in the same instant could leave it one subscriber low.
	msgr.allow <- struct{}{}
	waitFor(t, "the 16th delivery", func() bool { return len(msgr.sentIDs()) == 16 })
	msgr.allow <- struct{}{}
	waitFor(t, "the 17th delivery", func() bool { return len(msgr.sentIDs()) == 17 })
	close(msgr.allow)
	waitFor(t, "the paused campaign to wind down", func() bool { return !m.HasRunningCampaigns() })

	paused := msgr.sentIDs()
	if len(paused) != 17 {
		t.Fatalf("%d messages went out before the pause took effect, want the 15 let through and the 2 in flight", len(paused))
	}
	waitFor(t, "records of the messages sent before the pause", func() bool { return len(e.recorded()) == len(paused) })
	wantSame(t, "recorded at pause", e.recorded(), paused)
	e.waitCampaign("paused", len(paused))

	// Nothing more is recorded while paused.
	time.Sleep(200 * time.Millisecond)
	wantSame(t, "recorded while paused", e.recorded(), paused)

	e.setStatus(models.CampaignStatusRunning)
	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})

	// Everyone got the campaign once and has one record.
	wantSame(t, "delivered", msgr.sentIDs(), e.eligible)
	wantSame(t, "recorded", e.recorded(), e.eligible)
	e.waitCampaign("finished", len(e.eligible))
	wantSame(t, "gap", e.ids(sendGapSQL, e.campID), nil)
}

// deadStore is a store whose process "dies": after kill(), nothing reaches the database.
type deadStore struct {
	*store
	dead atomic.Bool
}

var errDead = errors.New("test: process is dead")

func (d *deadStore) NextCampaigns(ids []int64, counts []int64) ([]*models.Campaign, error) {
	if d.dead.Load() {
		return nil, errDead
	}
	return d.store.NextCampaigns(ids, counts)
}

func (d *deadStore) NextSubscribers(campID, limit int) ([]models.Subscriber, error) {
	if d.dead.Load() {
		return nil, errDead
	}
	return d.store.NextSubscribers(campID, limit)
}

func (d *deadStore) GetCampaign(campID int) (*models.Campaign, error) {
	if d.dead.Load() {
		return nil, errDead
	}
	return d.store.GetCampaign(campID)
}

func (d *deadStore) UpdateCampaignStatus(campID int, status string) error {
	if d.dead.Load() {
		return errDead
	}
	return d.store.UpdateCampaignStatus(campID, status)
}

func (d *deadStore) UpdateCampaignCounts(campID int, toSend int, sent int, lastSubID int) error {
	if d.dead.Load() {
		return errDead
	}
	return d.store.UpdateCampaignCounts(campID, toSend, sent, lastSubID)
}

func (d *deadStore) RecordCampaignSends(sends []models.CampaignSend) error {
	if d.dead.Load() {
		return errDead
	}
	return d.store.RecordCampaignSends(sends)
}

func (d *deadStore) CountCampaignSends(campID int) (int, error) {
	if d.dead.Load() {
		return 0, errDead
	}
	return d.store.CountCampaignSends(campID)
}

// The app is killed in the middle of a campaign and started again. The engine reads
// subscribers ahead of sending and moves its checkpoint as it reads, so the restarted
// app skips the recipients that were read but not sent and still ends the campaign as
// finished. This task does not change that. The records must hold exactly the messages
// that went out, so that the skipped recipients can be listed.
func TestSendRecordsExposeRecipientsLostOnRestart(t *testing.T) {
	e := newSendsEnv(t, 57)

	// The first process delivers 13 messages and then dies.
	first := &gateMessenger{hangAfter: 13}
	dying := &deadStore{store: e.store}
	e.startManager(dying, first, io.Discard)
	waitFor(t, "13 deliveries", func() bool { return len(first.sentIDs()) == 13 })
	waitFor(t, "their records", func() bool { return len(e.recorded()) == 13 })
	dying.dead.Store(true)

	before := first.sentIDs()
	wantSame(t, "recorded before the restart", e.recorded(), before)

	// The second process picks the running campaign up.
	second := &gateMessenger{}
	logs := &syncBuffer{}
	e.startManager(e.store, second, logs)
	waitFor(t, "the finished campaign's send record summary", func() bool {
		return strings.Contains(logs.String(), "send records:")
	})

	delivered := append(append([]int{}, before...), second.sentIDs()...)
	sort.Ints(delivered)
	for i := 1; i < len(delivered); i++ {
		if delivered[i] == delivered[i-1] {
			t.Fatalf("subscriber %d got the campaign twice", delivered[i])
		}
	}
	wantSame(t, "recorded", e.recorded(), delivered)

	status, _, toSend := e.campaign()
	if status != "finished" || toSend != len(e.eligible) {
		t.Fatalf("campaign status=%s to_send=%d", status, toSend)
	}

	// The upstream defect: recipients are missing although the campaign is finished.
	lost := minus(e.eligible, delivered)
	if len(lost) == 0 {
		t.Fatal("expected the restart to skip recipients; the engine's checkpoint behaviour has changed, so revisit this test")
	}
	wantSame(t, "gap", e.ids(sendGapSQL, e.campID), lost)
	if line := fmt.Sprintf("send records: %d recorded,", len(delivered)); !strings.Contains(logs.String(), line) || !strings.Contains(logs.String(), fmt.Sprintf("%d to send", len(e.eligible))) {
		t.Fatalf("log lacks %q:\n%s", line, logs.String())
	}
}

// When records cannot be written, the campaign still goes out in full and finishes.
func TestSendRecordFailureDoesNotStopSending(t *testing.T) {
	e := newSendsEnv(t, 57)
	e.db.MustExec(`DROP TABLE axm_campaign_sends`)

	msgr := &gateMessenger{}
	logs := &syncBuffer{}
	start := time.Now()
	e.startManager(e.store, msgr, logs)

	waitFor(t, "all deliveries", func() bool { return len(msgr.sentIDs()) == len(e.eligible) })
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("sending took %v with failing records", d)
	}
	e.waitCampaign("finished", len(e.eligible))
	wantSame(t, "delivered", msgr.sentIDs(), e.eligible)
	waitFor(t, "the failure to be logged", func() bool {
		return strings.Contains(logs.String(), "error counting send records")
	})
}

func TestSendRecordSQL(t *testing.T) {
	e := newSendsEnv(t, 5)
	a, b, c := e.eligible[0], e.eligible[1], e.eligible[2]

	// A queued or retried record still gets the time it was sent at. Unknown campaigns
	// and subscribers are skipped instead of failing the whole batch.
	now := time.Now()
	batch := []models.CampaignSend{
		{CampaignID: e.campID, SubscriberID: a, SentAt: now.Add(-90 * time.Second)},
		{CampaignID: e.campID, SubscriberID: b, SentAt: now},
		{CampaignID: e.campID, SubscriberID: 987654321, SentAt: now},
		{CampaignID: 987654321, SubscriberID: c, SentAt: now},
		{CampaignID: e.campID, SubscriberID: b, SentAt: now},
	}
	if err := e.store.RecordCampaignSends(batch); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "recorded", e.recorded(), []int{a, b})
	var ages []float64
	if err := e.db.Select(&ages, `SELECT EXTRACT(EPOCH FROM NOW() - sent_at) FROM axm_campaign_sends WHERE campaign_id = $1 ORDER BY subscriber_id`, e.campID); err != nil {
		t.Fatal(err)
	}
	if ages[0] < 89 || ages[0] > 100 || ages[1] < -1 || ages[1] > 10 {
		t.Fatalf("ages %v, want about 90s and 0s", ages)
	}

	// Recording the same message again keeps the first record.
	if err := e.store.RecordCampaignSends([]models.CampaignSend{{CampaignID: e.campID, SubscriberID: a, SentAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	var age float64
	e.get(&age, `SELECT EXTRACT(EPOCH FROM NOW() - sent_at) FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, a)
	if n, _ := e.store.CountCampaignSends(e.campID); n != 2 || age < 89 {
		t.Fatalf("count=%d age=%v after a repeated record", n, age)
	}

	// Marks: first and last time, count, first class and every class seen.
	type marks struct {
		Opened      *time.Time `db:"opened_at"`
		LastOpened  *time.Time `db:"last_opened_at"`
		OpenCount   *int       `db:"open_count"`
		OpenKind    *int       `db:"open_kind"`
		OpenKinds   *int       `db:"open_kinds"`
		Clicked     *time.Time `db:"clicked_at"`
		LastClicked *time.Time `db:"last_clicked_at"`
		ClickCount  *int       `db:"click_count"`
		ClickKind   *int       `db:"click_kind"`
		ClickKinds  *int       `db:"click_kinds"`
	}
	read := func(sub int) marks {
		var m marks
		e.get(&m, `SELECT opened_at, last_opened_at, open_count, open_kind, open_kinds,
			clicked_at, last_clicked_at, click_count, click_kind, click_kinds
			FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, sub)
		return m
	}
	ua, ub := e.subUUID(a), e.subUUID(b)
	if err := e.core.MarkCampaignSendOpened(e.campUUID, ua, models.AgentKindAppleProxy); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := e.core.MarkCampaignSendOpened(e.campUUID, ua, models.AgentKindBrowser); err != nil {
		t.Fatal(err)
	}
	if err := e.core.MarkCampaignSendOpened(e.campUUID, ua, models.AgentKindBrowser); err != nil {
		t.Fatal(err)
	}
	m := read(a)
	if m.Opened == nil || m.LastOpened == nil || !m.LastOpened.After(*m.Opened) || *m.OpenCount != 3 ||
		*m.OpenKind != models.AgentKindAppleProxy || *m.OpenKinds != 1<<models.AgentKindAppleProxy|1<<models.AgentKindBrowser {
		t.Fatalf("open marks %+v", m)
	}
	if m.Clicked != nil || m.LastClicked != nil || m.ClickCount != nil || m.ClickKind != nil || m.ClickKinds != nil {
		t.Fatalf("an open set click marks: %+v", m)
	}
	if err := e.core.MarkCampaignSendClicked(e.campUUID, ua, models.AgentKindNone); err != nil {
		t.Fatal(err)
	}
	if err := e.core.MarkCampaignSendClicked(e.campUUID, ua, models.AgentKindOther); err != nil {
		t.Fatal(err)
	}
	m = read(a)
	if m.Clicked == nil || *m.ClickCount != 2 || *m.ClickKind != models.AgentKindNone ||
		*m.ClickKinds != 1<<models.AgentKindNone|1<<models.AgentKindOther || *m.OpenCount != 3 {
		t.Fatalf("click marks %+v", m)
	}
	if o := read(b); o.Opened != nil || o.OpenCount != nil || o.Clicked != nil || o.ClickCount != nil {
		t.Fatalf("another subscriber's record was marked: %+v", o)
	}

	// A message without a send record gets no record from a view or a click, and
	// unknown UUIDs are not an error.
	for _, sub := range []string{e.subUUID(c), "00000000-0000-0000-0000-00000000dead"} {
		if err := e.core.MarkCampaignSendOpened(e.campUUID, sub, models.AgentKindBrowser); err != nil {
			t.Fatal(err)
		}
		if err := e.core.MarkCampaignSendClicked(e.campUUID, sub, models.AgentKindBrowser); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.core.MarkCampaignSendOpened("00000000-0000-0000-0000-00000000dead", ub, models.AgentKindBrowser); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "recorded after marks", e.recorded(), []int{a, b})
	if o := read(b); o.Opened != nil {
		t.Fatalf("a view of an unknown campaign marked a record: %+v", o)
	}

	// Deleting a subscriber or a campaign takes the records along.
	if _, err := e.q.DeleteSubscribers.Exec(pq.Int64Array{int64(a)}, pq.StringArray{}); err != nil {
		t.Fatal(err)
	}
	wantSame(t, "recorded after deleting a subscriber", e.recorded(), []int{b})
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	var left int
	e.get(&left, `SELECT COUNT(*) FROM axm_campaign_sends`)
	if left != 0 {
		t.Fatalf("%d records left after deleting the campaign", left)
	}
}

// Records are kept for 12 months. The cleanup deletes older ones in batches.
func TestSendRecordRetention(t *testing.T) {
	e := newSendsEnv(t, 0)

	// 30 campaigns to 1,500 subscribers each: 45,000 old records, more than two batches.
	e.db.MustExec(`DELETE FROM subscribers`)
	e.db.MustExec(`INSERT INTO subscribers (uuid, email, name)
		SELECT gen_random_uuid(), 'r' || g || '@example.invalid', 'Synthetic' FROM generate_series(1, 1500) g`)
	e.db.MustExec(`INSERT INTO campaigns (uuid, name, subject, from_email, body, messenger, status)
		SELECT gen_random_uuid(), 'Old ' || g, 'Subject', 'sender@example.invalid', 'Body', 'email', 'finished' FROM generate_series(1, 31) g`)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at, opened_at, open_count, open_kind, open_kinds)
		SELECT c.id, s.id, NOW() - INTERVAL '12 months' - ((c.n + 2) || ' days')::INTERVAL, NOW() - INTERVAL '12 months', 1, 1, 2
		FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS n FROM campaigns WHERE name LIKE 'Old %') c
		CROSS JOIN subscribers s WHERE c.n <= 30`)
	// Records inside the period stay, including ones that are three days short of 12 months.
	// (Three days, not one: Go and Postgres count "12 months before 29 February" a day apart.)
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at)
		SELECT (SELECT MAX(id) FROM campaigns), s.id,
			CASE WHEN s.id % 2 = 0 THEN NOW() - INTERVAL '12 months' + INTERVAL '3 days' ELSE NOW() - INTERVAL '3 days' END
		FROM subscribers s`)

	n, err := e.core.PurgeOldCampaignSends()
	if err != nil {
		t.Fatal(err)
	}
	if n != 45000 {
		t.Fatalf("deleted %d records, want 45000", n)
	}
	var left, old int
	e.get(&left, `SELECT COUNT(*) FROM axm_campaign_sends`)
	e.get(&old, `SELECT COUNT(*) FROM axm_campaign_sends WHERE sent_at < NOW() - INTERVAL '12 months'`)
	if left != 1500 || old != 0 {
		t.Fatalf("%d records left (%d of them past the period), want 1500 and 0", left, old)
	}

	// Running it again deletes nothing.
	if n, err := e.core.PurgeOldCampaignSends(); err != nil || n != 0 {
		t.Fatalf("second run deleted %d, err %v", n, err)
	}
}

// The view pixel and the tracked link mark the send record, and never fail because of it.
func TestTrackingRequestsMarkSendRecord(t *testing.T) {
	e := newSendsEnv(t, 3)
	a, b := e.eligible[0], e.eligible[1]
	ua, ub := e.subUUID(a), e.subUUID(b)
	if err := e.store.RecordCampaignSends([]models.CampaignSend{{CampaignID: e.campID, SubscriberID: a, SentAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	var linkUUID string
	e.get(&linkUUID, `INSERT INTO links (uuid, url) VALUES (gen_random_uuid(), 'https://example.invalid/offer') RETURNING uuid`)

	logs := &syncBuffer{}
	app := &App{cfg: &Config{}, core: e.core, log: log.New(logs, "", 0), i18n: &i18n.I18n{}}
	app.cfg.Privacy.IndividualTracking = true
	srv := echo.New()
	srv.GET("/link/:linkUUID/:campUUID/:subUUID", app.LinkRedirect)
	srv.GET("/campaign/:campUUID/:subUUID/px.png", app.RegisterCampaignView)

	request := func(path, agent string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if agent != "" {
			req.Header.Set("User-Agent", agent)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	pixel := func(sub, agent string) {
		t.Helper()
		rec := request("/campaign/"+e.campUUID+"/"+sub+"/px.png", agent)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() == 0 {
			t.Fatalf("pixel: %d %s", rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	click := func(sub, agent string) {
		t.Helper()
		rec := request("/link/"+linkUUID+"/"+e.campUUID+"/"+sub, agent)
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "https://example.invalid/offer" {
			t.Fatalf("click: %d %q", rec.Code, rec.Header().Get("Location"))
		}
	}
	type row struct {
		OpenCount  int `db:"open_count"`
		OpenKind   int `db:"open_kind"`
		OpenKinds  int `db:"open_kinds"`
		ClickCount int `db:"click_count"`
		ClickKind  int `db:"click_kind"`
		Views      int `db:"views"`
		Clicks     int `db:"clicks"`
	}
	state := func() row {
		t.Helper()
		var r row
		e.get(&r, `SELECT COALESCE(open_count, 0) AS open_count, COALESCE(open_kind, -1) AS open_kind,
			COALESCE(open_kinds, 0) AS open_kinds, COALESCE(click_count, 0) AS click_count, COALESCE(click_kind, -1) AS click_kind,
			(SELECT COUNT(*) FROM campaign_views) AS views, (SELECT COUNT(*) FROM link_clicks) AS clicks
			FROM axm_campaign_sends WHERE campaign_id = $1 AND subscriber_id = $2`, e.campID, a)
		return r
	}

	const (
		gmail   = "Mozilla/5.0 (Windows NT 5.1; rv:11.0) Gecko Firefox/11.0 (via ggpht.com GoogleImageProxy)"
		browser = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
	)
	pixel(ua, gmail)
	pixel(ua, browser)
	click(ua, "curl/8.5.0")
	want := row{OpenCount: 2, OpenKind: models.AgentKindGmailProxy, OpenKinds: 1<<models.AgentKindGmailProxy | 1<<models.AgentKindBrowser,
		ClickCount: 1, ClickKind: models.AgentKindAutomated, Views: 2, Clicks: 1}
	if got := state(); got != want {
		t.Fatalf("after two views and a click:\n got  %+v\n want %+v", got, want)
	}

	// A recipient without a send record: the upstream tables record the hit as before,
	// and no send record appears.
	pixel(ub, browser)
	click(ub, browser)
	want.Views, want.Clicks = 3, 2
	if got := state(); got != want {
		t.Fatalf("after hits without a send record:\n got  %+v\n want %+v", got, want)
	}
	wantSame(t, "recorded", e.recorded(), []int{a})

	// Template previews use a dummy UUID: nothing is recorded or marked.
	pixel(dummyUUID, browser)
	if got := state(); got != want {
		t.Fatalf("after a preview hit: %+v", got)
	}

	// Without individual tracking, hits are counted anonymously and records are not marked.
	app.cfg.Privacy.IndividualTracking = false
	pixel(ua, browser)
	click(ua, browser)
	want.Views, want.Clicks = 4, 3
	if got := state(); got != want {
		t.Fatalf("with individual tracking off:\n got  %+v\n want %+v", got, want)
	}

	// With tracking disabled, nothing is recorded at all.
	app.cfg.Privacy.DisableTracking = true
	pixel(ua, browser)
	click(ua, browser)
	if got := state(); got != want {
		t.Fatalf("with tracking disabled: %+v", got)
	}

	// If the send records cannot be written, the pixel and the redirect still work
	// and the upstream tables still get the hit.
	app.cfg.Privacy.DisableTracking, app.cfg.Privacy.IndividualTracking = false, true
	e.db.MustExec(`DROP TABLE axm_campaign_sends`)
	pixel(ua, browser)
	click(ua, browser)
	var views, clicks int
	e.get(&views, `SELECT COUNT(*) FROM campaign_views`)
	e.get(&clicks, `SELECT COUNT(*) FROM link_clicks`)
	if views != 5 || clicks != 4 {
		t.Fatalf("views=%d clicks=%d after the send records table went away", views, clicks)
	}
	if got := logs.String(); !strings.Contains(got, "error marking view on send record") || !strings.Contains(got, "error marking link click on send record") {
		t.Fatalf("mark failures were not logged: %q", got)
	}
}

// schema.sql (new installs) and the migration (upgrades) must create the same table.
func TestSendRecordMigration(t *testing.T) {
	e := newSendsEnv(t, 0)
	root := repoRoot(t)

	schema, err := os.ReadFile(filepath.Join(root, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	mig, err := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.6.go"))
	if err != nil {
		t.Fatal(err)
	}
	const start, end = "-- AXM campaign send records. Keep this block identical to V6_2_1_axm_6.", "-- End of AXM campaign send records."
	i, j := strings.Index(string(schema), start), strings.Index(string(schema), end)
	if i < 0 || j < i {
		t.Fatal("schema.sql lacks the send records block")
	}
	block := string(schema)[i : j+len(end)]
	if !strings.Contains(string(mig), block) {
		t.Fatal("schema.sql and the migration differ")
	}

	shape := func() []string {
		var out []string
		if err := e.db.Select(&out, `
			SELECT 'column ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || COALESCE(column_default, '')
				FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'axm_campaign_sends'
			UNION ALL SELECT 'index ' || indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'axm_campaign_sends'
			UNION ALL SELECT 'constraint ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
				WHERE conrelid = 'axm_campaign_sends'::regclass AND contype IN ('p', 'f')
			ORDER BY 1`); err != nil {
			t.Fatal(err)
		}
		return out
	}
	installed := shape()
	if len(installed) != 13+3+3 {
		t.Fatalf("unexpected table shape (%d parts):\n%s", len(installed), strings.Join(installed, "\n"))
	}

	// An upgrade from a database without the table, run twice, ends in the same shape
	// and keeps existing records.
	e.db.MustExec(`DROP TABLE axm_campaign_sends`)
	if err := migrations.V6_2_1_axm_6(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	sub := e.addSub("enabled", "unconfirmed")
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1, $2)`, e.campID, sub)
	if err := migrations.V6_2_1_axm_6(e.db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if upgraded := shape(); !reflect.DeepEqual(installed, upgraded) {
		t.Fatalf("install and upgrade differ:\n%s\n---\n%s", strings.Join(installed, "\n"), strings.Join(upgraded, "\n"))
	}
	wantSame(t, "records kept by a repeated migration", e.recorded(), []int{sub})

	// A plain install on a database that already has the schema wipes the send records
	// like every other table, and puts the table back with its foreign keys.
	e.reinstall()
	var left int
	e.get(&left, `SELECT COUNT(*) FROM axm_campaign_sends`)
	if reinstalled := shape(); left != 0 || !reflect.DeepEqual(installed, reinstalled) {
		t.Fatalf("after a second install: %d records left, shape:\n%s", left, strings.Join(reinstalled, "\n"))
	}

	// The table has no sequence of its own, so a backup role needs one grant only.
	var seqs int
	e.get(&seqs, `SELECT COUNT(*) FROM pg_depend d JOIN pg_class c ON c.oid = d.objid AND c.relkind = 'S'
		WHERE d.refobjid = 'axm_campaign_sends'::regclass`)
	if seqs != 0 {
		t.Fatalf("the table owns %d sequences", seqs)
	}
}

// The migration is registered last and the list stays in semver order.
func TestSendRecordMigrationRegistered(t *testing.T) {
	last := migList[len(migList)-1]
	if last.version != "v6.2.1-axm.6" {
		t.Fatalf("last migration is %s", last.version)
	}
	for i := 1; i < len(migList); i++ {
		if semver.Compare(migList[i-1].version, migList[i].version) >= 0 {
			t.Fatalf("%s is not before %s", migList[i-1].version, migList[i].version)
		}
	}
}
