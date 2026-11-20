package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

func senderDomain(addr string) string {
	a, err := mail.ParseAddress(addr)
	if err != nil {
		return ""
	}
	at := strings.LastIndexByte(a.Address, '@')
	if at < 1 {
		return ""
	}
	return strings.ToLower(a.Address[at+1:])
}

func internalSender(addr string, domains []string) bool {
	d := senderDomain(addr)
	for _, v := range domains {
		if d != "" && d == strings.ToLower(v) {
			return true
		}
	}
	return false
}

// placeholderLocals are local parts that stand for "fill in the real address".
var placeholderLocals = map[string]bool{"change-me": true, "changeme": true, "change_me": true}

// addrOf returns the lowercased address part of a From value, or "" if the
// value cannot be parsed.
func addrOf(v string) string {
	a, err := mail.ParseAddress(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return strings.ToLower(a.Address)
}

// placeholderFrom reports whether a From value is a placeholder that was never
// replaced (change-me@..., or an address on a domain reserved for examples).
// It is a pure function so other checks can reuse it.
func placeholderFrom(v string) bool { return placeholderAddress(addrOf(v)) }

// placeholderAddress is placeholderFrom for an address that is already parsed
// (quotes in the local part removed) and lowercased.
func placeholderAddress(addr string) bool {
	at := strings.LastIndexByte(addr, '@')
	if at < 1 {
		return false
	}
	local, domain := addr[:at], addr[at+1:]
	if placeholderLocals[local] {
		return true
	}
	for _, d := range []string{"example.com", "example.org", "example.net"} {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	for _, tld := range []string{"example", "invalid", "test"} {
		if strings.HasSuffix(domain, "."+tld) {
			return true
		}
	}
	return false
}

// placeholderListFrom is the stricter rule for the settings list: it also
// rejects the stock install address (noreply@listmonk.yoursite.com).
func placeholderListFrom(v string) bool { return placeholderListAddress(addrOf(v)) }

func placeholderListAddress(addr string) bool {
	if placeholderAddress(addr) {
		return true
	}
	at := strings.LastIndexByte(addr, '@')
	if at < 1 {
		return false
	}
	d := addr[at+1:]
	return d == "yoursite.com" || strings.HasSuffix(d, ".yoursite.com")
}

var reFromNameSpecial = regexp.MustCompile(`[,"<>@;:\\()\[\]]`)

// matchFromAddress finds the list entry a From value stands for. A value equal
// to an entry (after trimming) matches it; otherwise a value with the same
// address (case-insensitive) matches the first entry with that address, so a
// stray space or a different display name is replaced by the list's spelling.
func matchFromAddress(value string, list []string) (string, bool) {
	if i := matchFromIndex(value, list); i >= 0 {
		return list[i], true
	}
	return "", false
}

// matchFromIndex is the index of the list entry a From value stands for, or -1.
// It is the one place that reads From values against the list; the admin UI
// shows what this returns and never parses an address itself.
func matchFromIndex(value string, list []string) int {
	v := strings.TrimSpace(value)
	if v == "" {
		return -1
	}
	for i, e := range list {
		if e == v {
			return i
		}
	}
	if a := addrOf(v); a != "" {
		for i, e := range list {
			if addrOf(e) == a {
				return i
			}
		}
	}
	return -1
}

// fromState is what a campaign's stored From means: the index of the matching
// list entry (nil when there is no list or no match) and whether it is a
// placeholder. Account senders (no list is used) and an empty From say nothing.
func fromState(from string, list []string, accountSenders bool) (*int, bool) {
	if accountSenders || strings.TrimSpace(from) == "" {
		return nil, false
	}
	if i := matchFromIndex(from, list); i >= 0 {
		return &i, false
	}
	return nil, placeholderFrom(from)
}

// defaultFromIndex is the list entry a new campaign starts with: the app
// default if it is on the list, else the first entry. -1 for an empty list.
func defaultFromIndex(appFrom string, list []string) int {
	if len(list) == 0 {
		return -1
	}
	if i := matchFromIndex(appFrom, list); i >= 0 {
		return i
	}
	return 0
}

// defaultFromFor is the From a new campaign gets when none is sent.
func defaultFromFor(appFrom string, list []string) string {
	return strings.TrimSpace(list[defaultFromIndex(appFrom, list)])
}

// normalizeFromAddresses cleans the team's From list. Entries are "Name <addr>"
// or "addr"; the address part is lowercased, exact duplicates are dropped and
// placeholder addresses are refused.
func normalizeFromAddresses(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("invalid from address")
		}
		v = strings.TrimSpace(v)
		if v == "" || len(v) > 200 {
			return nil, errors.New("invalid from address")
		}
		a, err := mail.ParseAddress(v)
		if err != nil || placeholderListAddress(strings.ToLower(a.Address)) {
			return nil, errors.New("invalid from address")
		}
		// Address.String quotes a local part that needs it ("two words"@x).
		e := strings.ToLower(a.Address)
		bare := strings.TrimSuffix(strings.TrimPrefix((&mail.Address{Address: e}).String(), "<"), ">")
		if name := strings.TrimSpace(a.Name); name != "" {
			if reFromNameSpecial.MatchString(name) {
				name = strconv.Quote(name)
			}
			e = name + " <" + bare + ">"
		} else {
			e = bare
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) > 50 {
		return nil, errors.New("too many from addresses")
	}
	return out, nil
}

func resolveFrom(user auth.User, defaultFrom, appFrom string, domains []string) string {
	if user.Type != auth.UserTypeAPI && user.Email.Valid && internalSender(user.Email.String, domains) {
		a, _ := mail.ParseAddress(user.Email.String)
		// Keep the stored/UI value readable. The SMTP messenger encodes the
		// display name on the wire; quote it here to preserve commas/quotes.
		return strconv.Quote(user.Name) + " " + (&mail.Address{Address: a.Address}).String()
	}
	if defaultFrom != "" {
		return defaultFrom
	}
	return appFrom
}

func defaultReplyTo(user auth.User, cfg *Config) string {
	if user.Type == auth.UserTypeAPI || !internalSender(user.Email.String, cfg.SenderDomains) {
		return cfg.DefaultReplyTo
	}
	return ""
}

func replyToAllowed(addr string, user auth.User, domains []string, verifiedLookup func(string) (bool, error)) (bool, error) {
	if strings.TrimSpace(addr) == "" {
		return true, nil
	}
	a, err := mail.ParseAddress(addr)
	if err != nil {
		return false, err
	}
	if internalSender(addr, domains) {
		return true, nil
	}
	return verifiedLookup(strings.ToLower(a.Address))
}

// hasFromHeader reports a custom From header, which would replace the From
// that was checked against the From list when the mail is sent.
func hasFromHeader(headers []map[string]string) bool {
	for _, set := range headers {
		for k := range set {
			if strings.EqualFold(strings.TrimSpace(k), "from") {
				return true
			}
		}
	}
	return false
}

func hasReservedHeader(headers []map[string]string) bool {
	for _, set := range headers {
		for k := range set {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "reply-to", "from", "sender", "return-path":
				return true
			}
		}
	}
	return false
}

func (a *App) campaignReplyVerified(camp *models.Campaign) error {
	// Every campaign response passes here: tell the UI what the From means.
	camp.FromMatch, camp.FromPlaceholder = fromState(camp.FromEmail, a.cfg.FromAddresses, len(a.cfg.SenderDomains) > 0)
	camp.FromEntry = nil
	if camp.FromMatch != nil {
		e := strings.TrimSpace(a.cfg.FromAddresses[*camp.FromMatch])
		camp.FromEntry = &e
	}
	if len(a.cfg.SenderDomains) == 0 {
		camp.ReplyToVerified = true
		return nil
	}
	ok, err := replyToAllowed(camp.ReplyTo, auth.User{}, a.cfg.SenderDomains, a.core.SenderAddressVerified)
	if err != nil {
		return err
	}
	camp.ReplyToVerified = ok
	return nil
}

func (a *App) requireVerifiedReply(camp *models.Campaign) error {
	if err := a.campaignReplyVerified(camp); err != nil {
		return err
	}
	if !camp.ReplyToVerified {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("senders.unverified", "email", camp.ReplyTo))
	}
	if len(a.cfg.SenderDomains) > 0 && hasReservedHeader(camp.Headers) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("senders.reservedHeader"))
	}
	return nil
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (a *App) ListSenderAddresses(c echo.Context) error {
	out, err := a.core.ListSenderAddresses()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) RequestSenderAddress(c echo.Context) error {
	if len(a.cfg.SenderDomains) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("senders.disabled"))
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := c.Bind(&req); err != nil {
		return err
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	if err != nil || strings.ContainsAny(req.Email, "\r\n") {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("senders.invalidEmail"))
	}
	email := strings.ToLower(addr.Address)
	if internalSender(email, a.cfg.SenderDomains) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("senders.internalAddress"))
	}
	if ok, err := a.core.SenderAddressVerified(email); err != nil {
		return err
	} else if ok {
		return c.JSON(http.StatusOK, okResp{true})
	}
	token, err := generateRandomString(32)
	if err != nil {
		return err
	}
	user := auth.GetUser(c)
	out, err := a.core.RequestSenderAddress(email, tokenHash(token), user.ID)
	if err != nil {
		return err
	}
	var msg bytes.Buffer
	data := struct{ VerifyURL, RequesterName, RequesterEmail, Email string }{
		strings.TrimRight(a.urlCfg.RootURL, "/") + "/sender/verify/" + token, user.Name, user.Email.String, email,
	}
	if err := notifs.Tpls.ExecuteTemplate(&msg, notifs.TplSenderVerify, data); err != nil {
		return err
	}
	subject, body := notifs.GetTplSubject(a.i18n.T("email.senderVerify.subject"), msg.Bytes())
	if err := a.emailMsgr.Push(models.Message{From: a.cfg.FromEmail, To: []string{email}, Subject: subject, Body: body}); err != nil {
		a.log.Printf("error sending reply address verification email: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("senders.sendFailed"))
	}
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) DeleteSenderAddress(c echo.Context) error {
	u := auth.GetUser(c)
	if err := a.core.DeleteSenderAddress(getID(c), u.ID, u.HasPerm(auth.PermSettingsManage)); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{true})
}

// SenderVerify GET never consumes a token. Only an explicit POST confirms ownership.
func (a *App) SenderVerify(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	token := c.Param("token")
	invalid := func() error {
		return c.Render(http.StatusBadRequest, tplMessage, makeMsgTpl(a.i18n.T("senders.verifyTitle"), "", a.i18n.T("senders.invalidLink")))
	}
	if len(token) != 32 {
		return invalid()
	}
	out, err := a.core.GetSenderToken(tokenHash(token))
	if err != nil {
		return invalid()
	}
	if c.Request().Method == http.MethodPost {
		if c.FormValue("confirm") != "yes" {
			return invalid()
		}
		if err := a.core.VerifySenderToken(tokenHash(token)); err != nil {
			return invalid()
		}
		return c.Render(http.StatusOK, tplMessage, makeMsgTpl(a.i18n.T("senders.verifiedTitle"), "", a.i18n.T("senders.verifiedMessage")))
	}
	return c.Render(http.StatusOK, "sender-verify", struct {
		publicTpl
		Email, Action string
	}{publicTpl{Title: a.i18n.T("senders.verifyTitle")}, out.Email, c.Request().URL.Path})
}

// normalizeSenderSettings is shared by full and keyed settings updates.
func normalizeSenderSettings(set *models.Settings) error {
	domains := []string{}
	seen := map[string]bool{}
	for _, v := range set.AxmSenderDomains {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || len(v) > 253 || strings.ContainsAny(v, " <>@\t\r\n:/") || !strings.Contains(v, ".") {
			return errors.New("invalid domain")
		}
		for _, label := range strings.Split(v, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("invalid domain")
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
					return errors.New("invalid domain")
				}
			}
		}
		if !seen[v] {
			domains = append(domains, v)
			seen[v] = true
		}
	}
	set.AxmSenderDomains = domains
	froms, err := normalizeFromAddresses(set.AxmFromAddresses)
	if err != nil {
		return err
	}
	set.AxmFromAddresses = froms
	for _, v := range []*string{&set.AxmDefaultFrom, &set.AxmDefaultReplyTo} {
		*v = strings.TrimSpace(*v)
		if *v != "" {
			if _, err := mail.ParseAddress(*v); err != nil || strings.ContainsAny(*v, "\r\n") {
				return errors.New("invalid address")
			}
		}
	}
	if set.AxmDefaultFrom != "" && !internalSender(set.AxmDefaultFrom, domains) {
		return errors.New("default From must belong to a sender domain")
	}
	return nil
}

// senderPolicyGuard serializes campaign activation and address deletion across app
// instances. Hold an advisory transaction lock until verification and persistence
// finish so deleting an unused address cannot race a campaign becoming scheduled.
func (a *App) senderPolicyGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if len(a.cfg.SenderDomains) == 0 {
			return next(c)
		}
		// Queue local waiters before reserving a DB connection. Otherwise a
		// burst of requests waiting on the advisory lock can exhaust the pool
		// while the lock holder needs another connection for campaign queries.
		a.senderPolicyMu.Lock()
		defer a.senderPolicyMu.Unlock()
		tx, err := a.db.BeginTxx(c.Request().Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(c.Request().Context(), "SELECT pg_advisory_xact_lock(824719302)"); err != nil {
			return err
		}
		return next(c)
	}
}
