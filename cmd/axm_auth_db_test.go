package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/labstack/echo/v4"
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

type authEnv struct {
	*sendsEnv
	a   *App
	srv *echo.Echo
	idp *fakeIdP
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

	e.a = &App{core: e.core, auth: au, i18n: &i18n.I18n{}, cfg: &Config{}, urlCfg: &UrlConfig{}, log: log.New(io.Discard, "", 0)}
	e.a.cfg.Security.OIDC.Enabled = true
	e.a.cfg.Security.OIDC.ProviderURL = e.idp.srv.URL

	// The routes these tests use, registered like initHTTPHandlers does.
	e.srv = echo.New()
	e.srv.Renderer = testRenderer{}
	e.srv.GET("/auth/oidc", e.a.OIDCFinish)
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
