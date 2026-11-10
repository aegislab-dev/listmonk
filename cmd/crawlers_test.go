package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/stuffbin"
	"github.com/labstack/echo/v4"
)

func TestBlockCrawlers(t *testing.T) {
	var logs bytes.Buffer
	block := blockCrawlers(log.New(&logs, "", 0))
	e := echo.New()
	e.GET("/", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) }, block)
	for n, agent := range strings.Fields(crawlerUserAgents) {
		// Real UAs wrap the token in a browser-style string; case is untrusted.
		req := httptest.NewRequest(http.MethodGet, "/?subscriber=private", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; "+strings.ToUpper(agent)+"/1.0)")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || rec.Body.String() != "Forbidden\n" {
			t.Fatalf("%s: got %d %q", agent, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Vary") != "User-Agent" {
			t.Fatalf("%s: missing Vary: User-Agent", agent)
		}
		if got, want := logs.String(), fmt.Sprintf("blocked crawler requests: count=%d\n", n+1); got != want {
			t.Fatalf("log must contain only a count: got %q, want %q", got, want)
		}
		logs.Reset()
	}
	for _, agent := range []string{"", "Mozilla/5.0", "Googlebot/2.1", "bingbot/2.0", "Applebot/0.1", "GoogleImageProxy", "Microsoft Office/16.0", "curl/8.0"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("User-Agent", agent)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent || logs.Len() != 0 {
			t.Fatalf("ordinary UA %q: got %d, log %q", agent, rec.Code, logs.String())
		}
	}
}

// Exercise the real route registrations. Invalid UUIDs deliberately stop at the
// existing validation layer without a DB; the built-image smoke test also uses
// real subscribers to verify opt-in, unsubscribe, redirect and pixel behavior.
func TestCrawlerRouteBoundaries(t *testing.T) {
	a := &App{cfg: &Config{EnablePublicArchive: true}, log: log.New(io.Discard, "", 0), i18n: &i18n.I18n{}, urlCfg: &UrlConfig{LoginURL: "/admin/login"}}
	a.cfg.Privacy.DisableTracking = true
	e := echo.New()
	e.Renderer = crawlerTestRenderer{}
	initHTTPHandlers(e, a)

	for _, agent := range strings.Fields(crawlerUserAgents) {
		for _, path := range []string{"/", "/subscription/form", "/archive", "/archive.xml", "/archive/latest", "/archive/example", "/api/public/archive", "/campaign/invalid/invalid"} {
			rec := crawlerTestRequest(e, http.MethodGet, path, agent)
			if rec.Code != http.StatusForbidden || rec.Body.String() != "Forbidden\n" {
				t.Errorf("%s %s: got %d %q", agent, path, rec.Code, rec.Body.String())
			}
		}
		for _, path := range []string{
			"/subscription/invalid/invalid", "/subscription/optin/invalid",
			"/subscription/export/invalid", "/subscription/wipe/invalid",
			"/link/invalid/invalid/invalid", "/campaign/invalid/invalid/px.png",
		} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				// Export/wipe are POST-only, links/pixels are GET-only.
				if method == http.MethodGet && (strings.Contains(path, "/export/") || strings.Contains(path, "/wipe/")) ||
					method == http.MethodPost && (strings.HasPrefix(path, "/link/") || strings.HasSuffix(path, "px.png")) {
					continue
				}
				rec := crawlerTestRequest(e, method, path, agent)
				if rec.Code != http.StatusBadRequest || rec.Body.String() != "existing handler reached" || rec.Header().Get("Vary") != "" {
					t.Errorf("%s %s %s: UA blocked a mail route: %d %q", agent, method, path, rec.Code, rec.Body.String())
				}
			}
		}
		// A valid tracking-pixel URL must return the real PNG even for blocked UAs.
		pixel := "/campaign/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/px.png"
		rec := crawlerTestRequest(e, http.MethodGet, pixel, agent)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pixelPNG) {
			t.Errorf("%s: tracking pixel failed: %d", agent, rec.Code)
		}
		for _, path := range []string{"/admin/", "/api/config"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("User-Agent", agent)
			// Malformed auth stops before session/DB access, using existing auth.
			req.Header.Set("Authorization", "invalid")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			want := http.StatusForbidden
			if path == "/admin/" {
				want = http.StatusTemporaryRedirect
			}
			if rec.Code != want || rec.Body.String() == "Forbidden\n" || rec.Header().Get("Vary") != "" {
				t.Errorf("%s %s: auth behavior changed: %d %q", agent, path, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestCrawlerRobotsTxt(t *testing.T) {
	dir, err := filepath.Abs("../static/public")
	if err != nil {
		t.Fatal(err)
	}
	fs, err := stuffbin.NewLocalFS("/", dir+":/public")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{fs: fs}
	e := echo.New()
	e.GET("/robots.txt", a.RobotsTxt)
	for range 2 {
		rec := crawlerTestRequest(e, http.MethodGet, "/robots.txt", "GPTBot")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/plain; charset=UTF-8" {
			t.Fatalf("robots.txt: %d %s", rec.Code, rec.Header())
		}
		body := rec.Body.String()
		for _, directive := range []string{"User-agent: *", "Allow: /subscription/form", "Disallow: /subscription/", "Disallow: /campaign/", "Disallow: /link/", "Disallow: /archive", "Disallow: /archive.xml"} {
			if !strings.Contains(body, directive+"\n") {
				t.Errorf("robots.txt missing %q", directive)
			}
		}
		group := strings.Split(body, "\n# AI crawler policy")[1]
		for _, agent := range strings.Fields(crawlerUserAgents) {
			if strings.Count(group, "User-agent: "+agent+"\n") != 1 {
				t.Errorf("robots.txt missing or duplicated %s", agent)
			}
		}
		if !strings.HasSuffix(group, "Disallow: /\n") || strings.Contains(group, "Allow:") {
			t.Fatalf("AI group must disallow all paths: %s", group)
		}
	}
}

func crawlerTestRequest(e *echo.Echo, method, path, agent string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("User-Agent", agent)
	if method == http.MethodPost {
		req.Body = io.NopCloser(strings.NewReader("List-Unsubscribe=One-Click"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

type crawlerTestRenderer struct{}

func (crawlerTestRenderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	_, err := io.WriteString(w, "existing handler reached")
	return err
}
