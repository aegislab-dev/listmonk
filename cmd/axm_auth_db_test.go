package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/internal/tmptokens"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/pquerna/otp/totp"
)

// These tests run the real login handlers, the real session store and the real
// user queries against a disposable Postgres (AXM_TEST_DB_URL). The OIDC
// provider is a local HTTP server that signs its own ID tokens.

// fakeIdP is a minimal OpenID Connect provider.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	// What the next login answers with.
	sub, nonce string
	idEmail    string // e-mail claim in the ID token ("" leaves it to UserInfo)
	infoSub    string // subject the UserInfo endpoint reports
	infoEmail  string
}

const testOIDCClientID = "listmonk-test"

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{key: key}

	b64 := base64.RawURLEncoding.EncodeToString
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/keys",
			"userinfo_endpoint":                     p.srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
			"n": b64(key.N.Bytes()),
			"e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		claims := map[string]any{
			"iss": p.srv.URL, "aud": testOIDCClientID, "sub": p.sub, "nonce": p.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		if p.idEmail != "" {
			claims["email"] = p.idEmail
			claims["email_verified"] = true
		}
		head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test"})
		body, _ := json.Marshal(claims)
		signed := b64(head) + "." + b64(body)
		sum := sha256.Sum256([]byte(signed))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 3600,
			"id_token": signed + "." + b64(sig),
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"sub": p.infoSub, "email": p.infoEmail, "email_verified": true})
	})

	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// testRenderer stands in for the public templates: it writes the template name
// and the message the page would show.
type testRenderer struct{}

func (testRenderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	msg := ""
	switch d := data.(type) {
	case loginTpl:
		msg = d.Error
	case twofaTpl:
		msg = d.Error
	case resetPasswordTpl:
		msg = d.Error
	case msgTpl:
		msg = d.Message
	}
	_, err := fmt.Fprintf(w, "%s|%s", name, msg)
	return err
}

// testMailbox is a messenger that keeps what it is asked to send.
type testMailbox struct{ msgs []models.Message }

func (m *testMailbox) Name() string                  { return "email" }
func (m *testMailbox) Push(msg models.Message) error { m.msgs = append(m.msgs, msg); return nil }
func (m *testMailbox) Flush() error                  { return nil }
func (m *testMailbox) Close() error                  { return nil }

type authEnv struct {
	*sendsEnv
	a    *App
	srv  *echo.Echo
	idp  *fakeIdP
	mail *testMailbox
}

func newAuthEnv(t *testing.T) *authEnv {
	e := &authEnv{sendsEnv: newSendsEnv(t, 0), idp: newFakeIdP(t)}
	e.db.MustExec(`INSERT INTO roles(id,type,name,permissions) VALUES(1,'user','Super Admin','{}')`)
	e.db.MustExec(`SELECT setval(pg_get_serial_sequence('roles','id'), 1)`)

	// The same session callbacks as initAuth.
	cb := &auth.Callbacks{
		GetCookie: func(name string, r any) (*http.Cookie, error) {
			return r.(echo.Context).Cookie(name)
		},
		SetCookie: func(cookie *http.Cookie, w any) error {
			cookie.SameSite = http.SameSiteLaxMode
			w.(echo.Context).SetCookie(cookie)
			return nil
		},
		GetUser: func(id int) (auth.User, error) {
			return e.core.GetUser(id, "", "")
		},
	}
	au, err := auth.New(auth.Config{OIDC: auth.OIDCConfig{
		Enabled:      true,
		ProviderURL:  e.idp.srv.URL,
		ClientID:     testOIDCClientID,
		ClientSecret: "secret",
		RedirectURL:  "http://listmonk.invalid/auth/oidc",
	}}, e.db.DB, cb, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}

	e.mail = &testMailbox{}
	e.a = &App{core: e.core, auth: au, i18n: &i18n.I18n{}, cfg: &Config{}, urlCfg: &UrlConfig{RootURL: "http://listmonk.invalid"},
		emailMsgr: e.mail, log: log.New(io.Discard, "", 0)}
	e.a.cfg.Security.OIDC.Enabled = true
	e.a.cfg.Security.OIDC.ProviderURL = e.idp.srv.URL

	// The routes these tests use, registered like initHTTPHandlers does.
	e.srv = echo.New()
	e.srv.Renderer = testRenderer{}
	e.srv.GET("/auth/oidc", e.a.OIDCFinish)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		e.srv.Add(m, "/admin/login", e.a.LoginPage)
		e.srv.Add(m, "/admin/login/twofa", e.a.TwofaPage)
		e.srv.Add(m, "/admin/forgot", e.a.ForgotPage)
		e.srv.Add(m, "/admin/reset", e.a.ResetPage)
	}
	e.srv.GET("/api/profile", func(c echo.Context) error {
		u, ok := c.Get(auth.UserHTTPCtxKey).(auth.User)
		if !ok {
			return c.Get(auth.UserHTTPCtxKey).(*echo.HTTPError)
		}
		return c.String(http.StatusOK, u.Username)
	}, au.Middleware)
	return e
}

func (e *authEnv) addUser(username, status string) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO users(username,email,name,user_role_id,status,type,password_login)
		VALUES($1,$1||'@example.invalid',$1,1,$2,'user',false) RETURNING id`, username, status)
	return id
}

// do sends a request with the given cookies and returns the response.
func (e *authEnv) do(method, target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && c.Value != "" {
			return c
		}
	}
	return nil
}

// oidcLogin walks the callback half of the OIDC login for the given e-mail.
func (e *authEnv) oidcLogin(email string) *httptest.ResponseRecorder {
	e.idp.sub, e.idp.nonce, e.idp.idEmail = "sub-"+email, "nonce-1", email
	state, _ := json.Marshal(oidcState{Nonce: "nonce-1", Next: "/admin/lists"})
	q := url.Values{"code": {"code"}, "state": {base64.URLEncoding.EncodeToString(state)}}
	return e.do(http.MethodGet, "/auth/oidc?"+q.Encode(), nil, &http.Cookie{Name: "nonce", Value: "nonce-1"})
}

func (e *authEnv) sessions(userID int) int {
	var n int
	e.get(&n, `SELECT COUNT(*) FROM sessions WHERE data->>'user_id' = $1::TEXT`, userID)
	return n
}

// A user that an admin has disabled must not get in through the OIDC provider.
func TestOIDCLoginRejectsDisabledUser(t *testing.T) {
	e := newAuthEnv(t)
	on, off := e.addUser("on", "enabled"), e.addUser("off", "disabled")

	rec := e.oidcLogin("on@example.invalid")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/lists" || sessionCookie(rec) == nil {
		t.Fatalf("enabled user: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if e.sessions(on) != 1 {
		t.Fatalf("enabled user has %d sessions, want 1", e.sessions(on))
	}

	rec = e.oidcLogin("off@example.invalid")
	if rec.Code != http.StatusOK || rec.Body.String() != "admin-login|users.invalidLogin" {
		t.Fatalf("disabled user: %d %q", rec.Code, rec.Body.String())
	}
	if sessionCookie(rec) != nil || e.sessions(off) != 0 {
		t.Fatalf("disabled user got a session (cookie %v, rows %d)", sessionCookie(rec), e.sessions(off))
	}
	var loggedIn bool
	e.get(&loggedIn, `SELECT loggedin_at IS NOT NULL FROM users WHERE id = $1`, off)
	if loggedIn {
		t.Fatal("disabled user's login was recorded")
	}
}

// Disabling a user ends the sessions they already hold.
func TestDisabledUserLosesSession(t *testing.T) {
	e := newAuthEnv(t)
	id := e.addUser("on", "enabled")

	cookie := sessionCookie(e.oidcLogin("on@example.invalid"))
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	if rec := e.do(http.MethodGet, "/api/profile", nil, cookie); rec.Code != http.StatusOK || rec.Body.String() != "on" {
		t.Fatalf("enabled: %d %q", rec.Code, rec.Body.String())
	}

	e.db.MustExec(`UPDATE users SET status = 'disabled' WHERE id = $1`, id)
	if rec := e.do(http.MethodGet, "/api/profile", nil, cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("disabled: %d %q, want 403", rec.Code, rec.Body.String())
	}

	// Enabled again, the same session works: nothing was deleted.
	e.db.MustExec(`UPDATE users SET status = 'enabled' WHERE id = $1`, id)
	if rec := e.do(http.MethodGet, "/api/profile", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("re-enabled: %d %q", rec.Code, rec.Body.String())
	}
}

// When the e-mail comes from the UserInfo endpoint, the answer has to be about
// the same subject as the ID token.
func TestOIDCUserInfoSubjectMustMatch(t *testing.T) {
	e := newAuthEnv(t)
	id := e.addUser("on", "enabled")

	login := func(infoSub string) *httptest.ResponseRecorder {
		e.idp.sub, e.idp.nonce, e.idp.idEmail = "sub-1", "nonce-1", ""
		e.idp.infoSub, e.idp.infoEmail = infoSub, "on@example.invalid"
		state, _ := json.Marshal(oidcState{Nonce: "nonce-1", Next: "/admin"})
		q := url.Values{"code": {"code"}, "state": {base64.URLEncoding.EncodeToString(state)}}
		return e.do(http.MethodGet, "/auth/oidc?"+q.Encode(), nil, &http.Cookie{Name: "nonce", Value: "nonce-1"})
	}

	rec := login("someone-else")
	if rec.Code != http.StatusOK || rec.Body.String() != "admin-login|OIDC UserInfo subject did not match" {
		t.Fatalf("mismatch: %d %q", rec.Code, rec.Body.String())
	}
	if sessionCookie(rec) != nil || e.sessions(id) != 0 {
		t.Fatal("mismatched UserInfo subject got a session")
	}

	rec = login("sub-1")
	if rec.Code != http.StatusFound || sessionCookie(rec) == nil {
		t.Fatalf("match: %d %q", rec.Code, rec.Body.String())
	}
}

// addPasswordUser adds a user that logs in with a password, optionally with TOTP on.
func (e *authEnv) addPasswordUser(username, password string, withTOTP bool) (id int, totpKey string) {
	e.t.Helper()
	twofa := "none"
	if withTOTP {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "listmonk", AccountName: username})
		if err != nil {
			e.t.Fatal(err)
		}
		twofa, totpKey = "totp", key.Secret()
	}
	e.get(&id, `INSERT INTO users(username,email,name,user_role_id,status,type,password_login,password,twofa_type,twofa_key)
		VALUES($1,$1||'@example.invalid',$1,1,'enabled','user',true,CRYPT($2, GEN_SALT('bf')),$3::twofa_type,NULLIF($4,'')) RETURNING id`,
		username, password, twofa, totpKey)

	// The 2FA attempt budget is kept in memory by user ID, and every test's
	// schema starts its IDs at 1: do not inherit another test's count.
	attempts := fmt.Sprintf("twofa-attempts:%d", id)
	tmptokens.Delete(attempts)
	e.t.Cleanup(func() { tmptokens.Delete(attempts) })
	return id, totpKey
}

func totpNow(t *testing.T, key string) string {
	t.Helper()
	code, err := totp.GenerateCode(key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// forgot asks for a reset link and returns the link that was mailed.
func (e *authEnv) forgot(email string) *url.URL {
	e.t.Helper()
	n := len(e.mail.msgs)
	rec := e.do(http.MethodPost, "/admin/forgot", url.Values{"email": {email}})
	if rec.Code != http.StatusOK || len(e.mail.msgs) != n+1 {
		e.t.Fatalf("forgot: %d %q, %d mails", rec.Code, rec.Body.String(), len(e.mail.msgs)-n)
	}
	// The mail is HTML: the link's "&" arrives escaped.
	u, err := url.Parse(html.UnescapeString(strings.TrimSpace(string(e.mail.msgs[n].Body))))
	if err != nil || u.Query().Get("token") == "" {
		e.t.Fatalf("no reset link in mail %q: %v", e.mail.msgs[n].Body, err)
	}
	return u
}

func (e *authEnv) reset(link *url.URL, token, password string) *httptest.ResponseRecorder {
	q := link.Query()
	q.Set("token", token)
	return e.do(http.MethodPost, "/admin/reset?"+q.Encode(), url.Values{"password": {password}, "password2": {password}})
}

// useResetMailTemplate makes the reset mail's body the bare link.
func useResetMailTemplate(t *testing.T) {
	t.Helper()
	// newSendsEnv initializes notifs once per process, which resets the templates.
	// Do that first so that it cannot undo the template set here.
	initTestNotifs.Do(func() {
		notifs.Initialize(notifs.Opt{}, nil, nil, log.New(io.Discard, "", 0))
	})
	old := notifs.Tpls
	notifs.Tpls = template.Must(template.New(notifs.TplForgotPassword).Parse("{{ .ResetURL }}"))
	t.Cleanup(func() { notifs.Tpls = old })
}

// Logging in with a password still works as before: straight in without 2FA,
// through the 2FA page with it.
func TestPasswordLogin(t *testing.T) {
	e := newAuthEnv(t)
	plain, _ := e.addPasswordUser("plain", "password-1", false)
	withTOTP, key := e.addPasswordUser("totp", "password-2", true)

	rec := e.do(http.MethodPost, "/admin/login", url.Values{"username": {"plain"}, "password": {"password-1"}, "next": {"/admin/lists"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/lists" || sessionCookie(rec) == nil || e.sessions(plain) != 1 {
		t.Fatalf("login: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	rec = e.do(http.MethodPost, "/admin/login", url.Values{"username": {"plain"}, "password": {"wrong-password"}})
	if rec.Code != http.StatusOK || rec.Body.String() != "admin-login|users.invalidLogin" || sessionCookie(rec) != nil {
		t.Fatalf("wrong password: %d %q", rec.Code, rec.Body.String())
	}

	// With TOTP the password alone gives no session.
	rec = e.do(http.MethodPost, "/admin/login", url.Values{"username": {"totp"}, "password": {"password-2"}, "next": {"/admin/lists"}})
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Path != "/admin/login/twofa" || loc.Query().Get("next") != "/admin/lists" ||
		sessionCookie(rec) != nil || e.sessions(withTOTP) != 0 {
		t.Fatalf("totp login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	form := url.Values{"token": {loc.Query().Get("token")}, "next": {"/admin/lists"}, "totp_code": {totpNow(t, key)}}
	rec = e.do(http.MethodPost, "/admin/login/twofa", form)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/lists" || sessionCookie(rec) == nil || e.sessions(withTOTP) != 1 {
		t.Fatalf("totp verify: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	// The login token is used up: the same form does not give a second session.
	rec = e.do(http.MethodPost, "/admin/login/twofa", form)
	if sessionCookie(rec) != nil || e.sessions(withTOTP) != 1 {
		t.Fatalf("replayed 2FA form got a session: %d %q", rec.Code, rec.Body.String())
	}
}

// A password reset is not a way around 2FA.
func TestPasswordResetAsksForTOTP(t *testing.T) {
	useResetMailTemplate(t)
	e := newAuthEnv(t)
	id, key := e.addPasswordUser("totp", "password-1", true)

	link := e.forgot("totp@example.invalid")
	rec := e.reset(link, link.Query().Get("token"), "password-new")
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Path != "/admin/login/twofa" || loc.Query().Get("token") == "" {
		t.Fatalf("reset: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if sessionCookie(rec) != nil || e.sessions(id) != 0 {
		t.Fatal("reset gave a session without the TOTP code")
	}

	// The password did change, and the 2FA step finishes the login.
	if _, err := e.core.LoginUser("totp", "password-new"); err != nil {
		t.Fatalf("new password does not work: %v", err)
	}
	rec = e.do(http.MethodPost, "/admin/login/twofa", url.Values{"token": {loc.Query().Get("token")}, "next": {loc.Query().Get("next")}, "totp_code": {"000000"}})
	if sessionCookie(rec) != nil {
		t.Fatal("wrong TOTP code gave a session")
	}
	rec = e.do(http.MethodPost, "/admin/login/twofa", url.Values{"token": {loc.Query().Get("token")}, "next": {loc.Query().Get("next")}, "totp_code": {totpNow(t, key)}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin" || sessionCookie(rec) == nil {
		t.Fatalf("totp verify: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
}

// Without 2FA, a reset logs the user in directly, as before.
func TestPasswordResetWithoutTOTP(t *testing.T) {
	useResetMailTemplate(t)
	e := newAuthEnv(t)
	id, _ := e.addPasswordUser("plain", "password-1", false)

	link := e.forgot("plain@example.invalid")
	if rec := e.do(http.MethodGet, "/admin/reset?"+link.RawQuery, nil); rec.Code != http.StatusOK || rec.Body.String() != "admin-reset-password|" {
		t.Fatalf("reset page: %d %q", rec.Code, rec.Body.String())
	}
	rec := e.reset(link, link.Query().Get("token"), "password-new")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin" || sessionCookie(rec) == nil || e.sessions(id) != 1 {
		t.Fatalf("reset: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	// The link works once.
	rec = e.reset(link, link.Query().Get("token"), "password-again")
	if rec.Code != http.StatusBadRequest || sessionCookie(rec) != nil {
		t.Fatalf("second use of the link: %d %q", rec.Code, rec.Body.String())
	}
	if _, err := e.core.LoginUser("plain", "password-new"); err != nil {
		t.Fatalf("password after the second try: %v", err)
	}
}

// Anyone who knows an e-mail address can post to the reset page. Wrong tokens
// must not use up the link that was mailed, and asking again must not replace it.
func TestPasswordResetLinkSurvivesWrongTokens(t *testing.T) {
	useResetMailTemplate(t)
	e := newAuthEnv(t)
	e.addPasswordUser("plain", "password-1", false)

	link := e.forgot("plain@example.invalid")
	wrong := strings.Repeat("x", tmpAuthTokenLen)
	for i := 0; i < 20; i++ {
		if rec := e.reset(link, wrong, "attacker-password"); rec.Code != http.StatusBadRequest || sessionCookie(rec) != nil {
			t.Fatalf("wrong token POST %d: %d %q", i, rec.Code, rec.Body.String())
		}
		q := link.Query()
		q.Set("token", wrong)
		if rec := e.do(http.MethodGet, "/admin/reset?"+q.Encode(), nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("wrong token GET %d: %d %q", i, rec.Code, rec.Body.String())
		}
	}
	if _, err := e.core.LoginUser("plain", "attacker-password"); err == nil {
		t.Fatal("a wrong token changed the password")
	}

	// A second request mails the same link instead of replacing it.
	if again := e.forgot("plain@example.invalid"); again.Query().Get("token") != link.Query().Get("token") {
		t.Fatal("a repeated request replaced the pending reset link")
	}

	if rec := e.reset(link, link.Query().Get("token"), "password-new"); rec.Code != http.StatusFound || sessionCookie(rec) == nil {
		t.Fatalf("the mailed link stopped working: %d %q", rec.Code, rec.Body.String())
	}
	// Once used, the next request gets a new link.
	if next := e.forgot("plain@example.invalid"); next.Query().Get("token") == link.Query().Get("token") {
		t.Fatal("a used link was mailed again")
	}
}

// TOTP guesses are counted per user, not per login: logging in again with the
// password does not buy more guesses.
func TestTOTPAttemptBudgetPerUser(t *testing.T) {
	e := newAuthEnv(t)
	id, key := e.addPasswordUser("totp", "password-1", true)

	login := func() string {
		rec := e.do(http.MethodPost, "/admin/login", url.Values{"username": {"totp"}, "password": {"password-1"}})
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if loc.Query().Get("token") == "" {
			t.Fatalf("login: %d %q", rec.Code, rec.Header().Get("Location"))
		}
		return loc.Query().Get("token")
	}
	guess := func(token, code string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/admin/login/twofa", url.Values{"token": {token}, "totp_code": {code}})
	}

	// 15 wrong guesses, each on a new login.
	for i := 0; i < 15; i++ {
		if rec := guess(login(), "000000"); rec.Code != http.StatusOK || rec.Body.String() != "admin-twofa|globals.messages.invalidValue" {
			t.Fatalf("guess %d: %d %q", i+1, rec.Code, rec.Body.String())
		}
	}
	// The 16th is refused, even with the right code and a new login.
	if rec := guess(login(), totpNow(t, key)); rec.Code != http.StatusTooManyRequests || sessionCookie(rec) != nil {
		t.Fatalf("16th try: %d %q, want 429", rec.Code, rec.Body.String())
	}
	if e.sessions(id) != 0 {
		t.Fatal("got a session after the budget was used up")
	}
}
