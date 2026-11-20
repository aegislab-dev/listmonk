package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// Campaign test messages to addresses that are not subscribers (LST-41).
//
// Upstream sends a test only to subscribers in the database, so people added their company
// address to a list and it then received real campaigns. Here an address may also receive a
// test when it is the signed-in user's own address or belongs to a test domain
// (axm.test_domains). Such an address gets no subscriber row: the message is rendered for a
// stand-in ("sample") subscriber and, like a preview, with the dummy campaign UUID, so its
// views, clicks and unsubscribe link touch nothing real. The dummy subscriber UUID alone
// cannot mark such a message: with individual tracking off every real campaign message
// carries it too.

// How an address came to be allowed.
const (
	testKindSubscriber = "subscriber"
	testKindOwn        = "own"
	testKindInternal   = "internal"
)

// Where the values of the stand-in subscriber come from.
const (
	testSampleDefault    = "default"
	testSampleSubscriber = "subscriber"
	testSampleCustom     = "custom"
)

// Limits. They are constants on purpose (decided with the plan): one request reaches at most
// testMaxRecipients addresses and one user sends at most testRateLimit messages per
// testRateWindow.
const (
	testMaxRecipients    = 10
	testRateLimit        = 30
	testRateWindow       = 10 * time.Minute
	testSampleMaxName    = 200
	testSampleMaxAttribs = 8 << 10
)

var errTestTooMany = errors.New("too many test recipients")

// testSample is the optional "sample" of a test or preview request: either a subscriber
// whose name and attributes are borrowed, or values typed in by hand. Empty means the
// default sample.
type testSample struct {
	SubscriberID int             `json:"subscriber_id"`
	Name         string          `json:"name"`
	Attribs      json.RawMessage `json:"attribs"`
}

// testSampleValues is a testSample after it has been checked and read.
type testSampleValues struct {
	Kind    string
	SubID   int
	Name    string
	Attribs models.JSON
}

// testRecipient is one address of a test request and why it may receive the test.
type testRecipient struct {
	Email string
	Kind  string
	// The subscriber, when Kind is testKindSubscriber.
	Sub models.Subscriber
}

// cleanTestEmails lowercases and trims the addresses of a request and drops empty and
// repeated ones, keeping the order.
func cleanTestEmails(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// profileAddress is the address on the signed-in user's profile, or "". An API user has none.
func profileAddress(user auth.User) string {
	if user.Type == auth.UserTypeAPI || !user.Email.Valid {
		return ""
	}
	email := strings.ToLower(strings.TrimSpace(user.Email.String))
	if !plainAddress(email) {
		return ""
	}
	return email
}

// ownTestAddress is the address the signed-in user may send a test to as their own, or "".
// The profile address alone proves nothing: a password account can type any address into
// its profile, and an identity provider may hand over an address it never checked. It
// counts once its mailbox has confirmed it through the link of requestOwnTestAddress,
// whatever the kind of account and whatever the sender settings.
func (a *App) ownTestAddress(user auth.User) string {
	email := profileAddress(user)
	if email == "" {
		return ""
	}
	ok, err := a.core.TestAddressVerified(user.ID, email)
	if err != nil {
		a.log.Printf("error checking the user's own test address: %v", err)
		return ""
	}
	if !ok {
		return ""
	}
	return email
}

// requestOwnTestAddress mails the user's profile address a link that confirms it as their
// own test address. It tells whether a mail went out: at most one goes out per user and
// minute. The mail is a system mail, like the reply address verification.
func (a *App) requestOwnTestAddress(user auth.User) (bool, error) {
	email := profileAddress(user)
	if email == "" {
		return false, nil
	}
	token, err := generateRandomString(32)
	if err != nil {
		return false, err
	}
	ok, err := a.core.RequestTestAddress(user.ID, email, tokenHash(token))
	if err != nil || !ok {
		return false, err
	}
	// A confirmation mail counts towards the user's limit of test messages, so that changing
	// the profile address over and over cannot be used to mail many addresses.
	slot, wait := a.testLimiter.take(user.ID, 1)
	if slot == 0 {
		a.core.ForgetTestAddressMail(user.ID)
		mins := int(math.Ceil(wait.Minutes()))
		if mins < 1 {
			mins = 1
		}
		return false, echo.NewHTTPError(http.StatusTooManyRequests, a.i18n.Ts("campaigns.testRateLimited", "minutes", strconv.Itoa(mins)))
	}
	link := strings.TrimRight(a.urlCfg.RootURL, "/") + "/test-address/verify/" + token
	body := "<p>" + html.EscapeString(a.i18n.Ts("email.testAddr.request", "name", user.Name, "username", user.Username)) + "</p>" +
		`<p><a href="` + html.EscapeString(link) + `">` + html.EscapeString(a.i18n.T("email.testAddr.button")) + "</a></p>" +
		"<p>" + html.EscapeString(a.i18n.T("email.testAddr.info")) + "</p>"
	msg := models.Message{From: a.cfg.FromEmail, To: []string{email}, Subject: a.i18n.T("email.testAddr.subject"), Body: []byte(body)}
	if err := a.emailMsgr.Push(msg); err != nil {
		a.log.Printf("error sending the test address verification e-mail: %v", err)
		// No mail went out, so the next try need not wait a minute.
		a.core.ForgetTestAddressMail(user.ID)
		a.testLimiter.giveBack(user.ID, slot, 1)
		return false, echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("campaigns.testAddr.sendFailed"))
	}
	return true, nil
}

// TestAddressVerify is the page the link of requestOwnTestAddress opens. As with reply
// addresses, opening it uses nothing up: only the form's POST confirms the address.
func (a *App) TestAddressVerify(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	title := a.i18n.T("campaigns.testAddr.verifyTitle")
	invalid := func() error {
		return c.Render(http.StatusBadRequest, tplMessage, makeMsgTpl(title, "", a.i18n.T("senders.invalidLink")))
	}
	token := c.Param("token")
	if len(token) != 32 {
		return invalid()
	}
	email, err := a.core.GetTestAddressToken(tokenHash(token))
	if err != nil {
		return invalid()
	}
	if c.Request().Method == http.MethodPost {
		if c.FormValue("confirm") != "yes" || a.core.VerifyTestAddressToken(tokenHash(token)) != nil {
			return invalid()
		}
		return c.Render(http.StatusOK, tplMessage, makeMsgTpl(a.i18n.T("campaigns.testAddr.verifiedTitle"), "", a.i18n.T("campaigns.testAddr.verifiedMessage")))
	}
	return c.Render(http.StatusOK, "test-address-verify", struct {
		publicTpl
		Email, Action string
	}{publicTpl{Title: title}, email, c.Request().URL.Path})
}

// plainAddress says whether s is one bare e-mail address (no display name, no brackets).
func plainAddress(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && strings.EqualFold(a.Address, s)
}

// classifyTestRecipients decides, address by address, whether a test may go there:
//
//  1. a subscriber the user may see receives it as that subscriber,
//  2. the user's own address (own, see ownTestAddress; "" when there is none) receives it
//     as a sample,
//  3. an address in a test domain (exact match, no subdomains) receives it as a sample,
//  4. anything else is rejected.
//
// emails must have gone through cleanTestEmails. subs holds the subscribers the user may see,
// by address. A subscriber the user may not see is not in subs, so it is handled like any
// other address and the answer never tells whether it is a subscriber.
//
// profile is the address on the user's profile. In a test domain it needs no confirmation,
// and it is still the user's own: the default sample is named after the user.
func classifyTestRecipients(emails []string, subs map[string]models.Subscriber, own, profile string, domains []string) ([]testRecipient, []string, error) {
	if len(emails) > testMaxRecipients {
		return nil, nil, errTestTooMany
	}
	var (
		out      = make([]testRecipient, 0, len(emails))
		rejected = []string{}
	)
	for _, e := range emails {
		if s, ok := subs[e]; ok {
			out = append(out, testRecipient{Email: e, Kind: testKindSubscriber, Sub: s})
			continue
		}
		switch {
		case !plainAddress(e):
			rejected = append(rejected, e)
		case own != "" && e == own:
			out = append(out, testRecipient{Email: e, Kind: testKindOwn})
		case internalSender(e, domains):
			kind := testKindInternal
			if profile != "" && e == profile {
				kind = testKindOwn
			}
			out = append(out, testRecipient{Email: e, Kind: kind})
		default:
			rejected = append(rejected, e)
		}
	}
	return out, rejected, nil
}

// readTestSample checks the values of a sample that was typed in by hand. A sample that
// names a subscriber is read by resolveTestSample, which needs the database. A sample with
// neither a name nor attributes is the default one; one with attributes, even the empty
// object, is typed in, so that empty values can be tested too.
func readTestSample(s *testSample) (testSampleValues, error) {
	if s == nil {
		return testSampleValues{Kind: testSampleDefault}, nil
	}
	var (
		name = strings.TrimSpace(s.Name)
		raw  = strings.TrimSpace(string(s.Attribs))
	)
	if raw == "null" {
		raw = ""
	}
	if name == "" && raw == "" {
		return testSampleValues{Kind: testSampleDefault}, nil
	}
	if utf8.RuneCountInString(name) > testSampleMaxName || len(raw) > testSampleMaxAttribs {
		return testSampleValues{}, errors.New("sample too long")
	}
	attribs := models.JSON{}
	if raw != "" {
		// Only a JSON object is a set of attributes.
		if err := json.Unmarshal([]byte(raw), &attribs); err != nil || attribs == nil {
			return testSampleValues{}, errors.New("sample attribs must be a JSON object")
		}
	}
	return testSampleValues{Kind: testSampleCustom, Name: name, Attribs: attribs}, nil
}

// resolveTestSample checks the sample of a request. Borrowing a subscriber's values shows
// them to the user, so it needs the same rights as opening that subscriber.
func (a *App) resolveTestSample(s *testSample, user auth.User) (testSampleValues, error) {
	if s == nil || s.SubscriberID == 0 {
		v, err := readTestSample(s)
		if err != nil {
			return v, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.testSampleInvalid"))
		}
		return v, nil
	}
	if s.SubscriberID < 0 {
		return testSampleValues{}, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.testSampleInvalid"))
	}
	if !user.HasPerm(auth.PermSubscribersGetAll) && !user.HasPerm(auth.PermSubscribersGet) {
		return testSampleValues{}, echo.NewHTTPError(http.StatusForbidden,
			a.i18n.Ts("globals.messages.permissionDenied", "name", auth.PermSubscribersGet))
	}
	if err := a.hasSubPerm(user, []int{s.SubscriberID}); err != nil {
		return testSampleValues{}, err
	}
	sub, err := a.core.GetSubscriber(s.SubscriberID, "", "")
	if err != nil {
		return testSampleValues{}, err
	}
	return testSampleValues{Kind: testSampleSubscriber, SubID: sub.ID, Name: sub.Name, Attribs: sub.Attribs}, nil
}

// buildTestSample returns the stand-in subscriber a message to email is rendered for. It
// has no ID and the dummy UUID, so nothing it does is recorded against a real subscriber.
// own says that email is the user's own address.
func buildTestSample(v testSampleValues, email string, own bool, user auth.User) models.Subscriber {
	sub := models.Subscriber{
		UUID:    dummyUUID,
		Email:   email,
		Name:    v.Name,
		Attribs: v.Attribs,
		Status:  models.SubscriberStatusEnabled,
	}
	if v.Kind == testSampleDefault {
		sub.Attribs = dummySubscriber.Attribs
		// The default sample has no name of its own: the user's name for their own address,
		// the part before the "@" for any other. A chosen sample keeps its name as it is,
		// empty too, and the preview shows the same.
		if own && strings.TrimSpace(user.Name) != "" {
			sub.Name = user.Name
		} else if at := strings.LastIndexByte(email, '@'); at > 0 {
			sub.Name = email[:at]
		} else {
			sub.Name = dummySubscriber.Name
		}
	}
	if sub.Attribs == nil {
		sub.Attribs = models.JSON{}
	}
	return sub
}

// testSendLimiter counts the test messages of each user in a sliding window. It lives in
// memory: production runs one instance, and a restart only forgets the last few minutes.
// The zero value is ready to use.
type testSendLimiter struct {
	mu   sync.Mutex
	now  func() time.Time
	seq  uint64
	sent map[int][]testSendSlot
}

// testSendSlot is one reserved message: when, and by which request.
type testSendSlot struct {
	at  time.Time
	req uint64
}

// giveBack returns n messages that the request req (as take named it) reserved and did not
// send after all. Only that request's own messages are touched: another request of the same
// user may have reserved in between.
func (l *testSendLimiter) giveBack(userID int, req uint64, n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.sent[userID]
	for i := len(list) - 1; i >= 0 && n > 0; i-- {
		if list[i].req == req {
			list = append(list[:i], list[i+1:]...)
			n--
		}
	}
	l.sent[userID] = list
}

// take reserves n messages for a user and returns the number of the reservation, for
// giveBack. When they do not fit, nothing is reserved (the number is 0) and wait says how
// long until they would.
func (l *testSendLimiter) take(userID, n int) (uint64, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if l.sent == nil {
		l.sent = map[int][]testSendSlot{}
	}
	// Forget what has left the window (times are in order).
	list := l.sent[userID]
	for len(list) > 0 && !list[0].at.After(now.Add(-testRateWindow)) {
		list = list[1:]
	}
	if over := len(list) + n - testRateLimit; over > 0 {
		l.sent[userID] = list
		if over > len(list) {
			// More than the limit in one request can never fit.
			return 0, testRateWindow
		}
		return 0, list[over-1].at.Add(testRateWindow).Sub(now)
	}
	l.seq++
	for i := 0; i < n; i++ {
		list = append(list, testSendSlot{at: now, req: l.seq})
	}
	l.sent[userID] = list
	return l.seq, 0
}

// testRecipients reads the addresses and the sample of a test request and returns the
// subscriber each message is rendered for. It answers 400 with the rejected addresses when
// any address may not receive a test (nothing is sent then), and 429 when the user is over
// the limit.
// The last value is the number of the limiter reservation, for giving back what is not sent.
func (a *App) testRecipients(c echo.Context, req *campReq, user auth.User) ([]testRecipient, testSampleValues, uint64, error) {
	var none testSampleValues
	emails := cleanTestEmails(req.SubscriberEmails)
	if len(emails) == 0 {
		return nil, none, 0, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.noSubsToTest"))
	}
	tooMany := echo.NewHTTPError(http.StatusBadRequest,
		a.i18n.Ts("campaigns.testTooMany", "max", strconv.Itoa(testMaxRecipients)))
	if len(emails) > testMaxRecipients {
		return nil, none, 0, tooMany
	}

	// The subscribers among the addresses that the user may see. The core answers 400 when
	// none of the addresses is a subscriber, which is not an error here.
	found, err := a.core.GetSubscribersByEmail(emails)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusBadRequest {
			return nil, none, 0, err
		}
		found = nil
	}
	subs := map[string]models.Subscriber{}
	for _, s := range found {
		if err := a.hasSubPerm(user, []int{s.ID}); err == nil {
			subs[strings.ToLower(s.Email)] = s
		}
	}

	own := a.ownTestAddress(user)
	profile := profileAddress(user)
	recips, rejected, err := classifyTestRecipients(emails, subs, own, profile, a.cfg.TestDomains)
	if err == errTestTooMany {
		return nil, none, 0, tooMany
	}
	if len(rejected) > 0 {
		// The screen marks these addresses. Whether one of them is a subscriber of a list
		// the user may not see is not told.
		data := map[string]any{"rejected": rejected}
		msg := a.i18n.T("campaigns.testRejected")
		// The user's own address, not confirmed yet: its mailbox gets the link that
		// confirms it, and the user is told to try again after that.
		if profile != "" && own == "" {
			for _, e := range rejected {
				if e != profile {
					continue
				}
				// A mail goes out now, or one went to this very address within the last
				// minute: either way the user is told to open the link in it.
				if _, err := a.requestOwnTestAddress(user); err != nil {
					return nil, none, 0, err
				}
				data["own_unverified"] = true
				msg = a.i18n.T("campaigns.testOwnUnverified")
			}
		}
		return nil, none, 0, echo.NewHTTPError(http.StatusBadRequest, map[string]any{"message": msg, "data": data})
	}

	sample, err := a.resolveTestSample(req.Sample, user)
	if err != nil {
		return nil, none, 0, err
	}

	slot, wait := a.testLimiter.take(user.ID, len(recips))
	if slot == 0 {
		mins := int(math.Ceil(wait.Minutes()))
		if mins < 1 {
			mins = 1
		}
		a.log.Printf("test send refused: user %d is over %d messages in %s", user.ID, testRateLimit, testRateWindow)
		c.Response().Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		return nil, none, 0, echo.NewHTTPError(http.StatusTooManyRequests,
			a.i18n.Ts("campaigns.testRateLimited", "minutes", strconv.Itoa(mins)))
	}

	// A chosen sample goes to every recipient, so that all of them see the same message.
	// With the default sample a subscriber keeps their own values.
	for i, r := range recips {
		if r.Kind != testKindSubscriber || sample.Kind != testSampleDefault {
			recips[i].Sub = buildTestSample(sample, r.Email, r.Kind == testKindOwn, user)
		}
	}
	return recips, sample, slot, nil
}

// auditTestSend writes the campaign.test row of a test request: how many messages went to
// which kind of address. Addresses are never written, only the domains of the addresses
// that are not subscribers.
func (a *App) auditTestSend(c echo.Context, campID int, campName string, sent []testRecipient, sample testSampleValues) {
	var (
		kinds   = map[string]int{}
		domains = []string{}
		seen    = map[string]bool{}
	)
	for _, r := range sent {
		kinds[r.Kind]++
		if d := senderDomain(r.Email); r.Kind != testKindSubscriber && d != "" && !seen[d] {
			seen[d] = true
			domains = append(domains, d)
		}
	}
	sort.Strings(domains)
	detail := map[string]any{
		"subscribers": kinds[testKindSubscriber],
		"own":         kinds[testKindOwn],
		"internal":    kinds[testKindInternal],
		"domains":     domains,
		"sample":      sample.Kind,
	}
	if sample.Kind == testSampleSubscriber {
		detail["sample_subscriber_id"] = sample.SubID
	}
	// The login name of a user can be their e-mail address (accounts made by the identity
	// provider), and that address can be the very recipient of this test. So, unlike other
	// rows, this one hides an address in the user columns; user_id still tells who it was.
	user := auth.GetUser(c)
	e := auditEntry(&user, auditCampaignTest, auditTarget{Type: auditTargetCampaign, ID: campID, Name: scrubAuditText(campName)}, len(sent))
	e.Username, e.UserName = scrubAuditText(e.Username), scrubAuditText(e.UserName)
	if err := a.core.AddAudit(e, scrubAuditDetail(detail)); err != nil {
		a.log.Printf("error writing the audit log (%s): %v", auditCampaignTest, err)
	}
}

// sampleCampaign returns the campaign a message to a sample subscriber is rendered with: a
// copy with the dummy UUID, as a preview has, so that nothing in the message is counted
// against the real campaign.
func sampleCampaign(camp models.Campaign) *models.Campaign {
	camp.UUID = dummyUUID
	return &camp
}

// Limits of the kept sample messages (see testViewStore).
const (
	testViewTTL      = 24 * time.Hour
	testViewMax      = 200
	testViewMaxBytes = 2 << 20
)

// testViewStore keeps the sample test messages that were sent lately, each under a random
// token, so that the "view in browser" link of such a message can show exactly what was
// mailed. A sample subscriber is in no table and the body of a test may not be saved yet, so
// the page cannot be rendered again later. The messages live in memory for testViewTTL, at
// most testViewMax of them: a restart forgets them, and the link then says so. The zero
// value is ready to use.
type testViewStore struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]testView
	order []string
}

type testView struct {
	body  []byte
	plain bool
	at    time.Time
}

func (s *testViewStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// put keeps a message under a token, dropping what is too old and, beyond the limit, the oldest.
func (s *testViewStore) put(token string, body []byte, plain bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]testView{}
	}
	now := s.clock()
	for len(s.order) > 0 && (len(s.order) >= testViewMax || now.Sub(s.items[s.order[0]].at) >= testViewTTL) {
		delete(s.items, s.order[0])
		s.order = s.order[1:]
	}
	s.items[token] = testView{body: body, plain: plain, at: now}
	s.order = append(s.order, token)
}

// get returns the message of a token that is still kept.
func (s *testViewStore) get(token string) (testView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[token]
	if !ok || s.clock().Sub(v.at) >= testViewTTL {
		return testView{}, false
	}
	return v, true
}

// sampleBrowserLink makes the "view in browser" link of a sample message work: the link
// (which carries the dummy UUIDs and would lead nowhere) is pointed at /test-message/<token>
// and the finished message is kept under that token. The tracking pixel has the same
// address plus "/px.png" and is left alone. A message without such a link, or one that is
// very large, is left as it is.
func (a *App) sampleBrowserLink(msg *manager.CampaignMessage, plain bool) {
	from := []byte(fmt.Sprintf(a.urlCfg.MessageURL, dummyUUID, dummyUUID))
	if !bytes.Contains(msg.Body(), from) || len(msg.Body()) > testViewMaxBytes {
		return
	}
	token, err := generateRandomString(32)
	if err != nil {
		a.log.Printf("error making a test message token: %v", err)
		return
	}
	var (
		to = []byte(strings.TrimRight(a.urlCfg.RootURL, "/") + "/test-message/" + token)
		px = []byte("/px.png")
	)
	msg.RewriteBody(func(b []byte) []byte {
		out := make([]byte, 0, len(b))
		for {
			i := bytes.Index(b, from)
			if i < 0 {
				return append(out, b...)
			}
			out = append(out, b[:i]...)
			rest := b[i+len(from):]
			if bytes.HasPrefix(rest, px) {
				out = append(out, from...)
			} else {
				out = append(out, to...)
			}
			b = rest
		}
	})
	a.testViews.put(token, msg.Body(), plain)
}

// TestMessageView shows a sample test message in the browser, exactly as it was mailed.
func (a *App) TestMessageView(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	v, ok := a.testViews.get(c.Param("token"))
	if !ok {
		return c.Render(http.StatusNotFound, tplMessage,
			makeMsgTpl(a.i18n.T("public.notFoundTitle"), "", a.i18n.T("campaigns.testView.gone")))
	}
	if v.plain {
		return c.String(http.StatusOK, string(v.body))
	}
	return c.HTML(http.StatusOK, string(v.body))
}

// previewSample reads the "sample" form value of a campaign preview and returns the
// subscriber to render it for. Without a sample, or with the default one, the preview keeps
// its usual demo subscriber.
func (a *App) previewSample(c echo.Context) (models.Subscriber, error) {
	raw := strings.TrimSpace(c.FormValue("sample"))
	if raw == "" {
		return dummySubscriber, nil
	}
	var s testSample
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return dummySubscriber, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.testSampleInvalid"))
	}
	v, err := a.resolveTestSample(&s, auth.GetUser(c))
	if err != nil {
		return dummySubscriber, err
	}
	if v.Kind == testSampleDefault {
		return dummySubscriber, nil
	}
	return buildTestSample(v, dummySubscriber.Email, false, auth.User{}), nil
}
