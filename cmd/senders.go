package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
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

func resolveFrom(user auth.User, defaultFrom, appFrom string, domains []string) string {
	if user.Type != auth.UserTypeAPI && user.Email.Valid && internalSender(user.Email.String, domains) {
		a, _ := mail.ParseAddress(user.Email.String)
		return (&mail.Address{Name: user.Name, Address: a.Address}).String()
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
