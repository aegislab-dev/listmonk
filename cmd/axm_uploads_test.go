package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// newUploadTestServer serves a temp upload directory the way initHTTPServer
// does for the filesystem media provider.
func newUploadTestServer(t *testing.T, files map[string]string, m ...echo.MiddlewareFunc) *echo.Echo {
	t.Helper()

	// A file next to (outside) the upload directory that must never be served.
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "config.toml"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "uploads")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv := echo.New()
	serveUploadDir(srv, "/uploads", root, m...)
	return srv
}

// uploadGet requests a raw (already encoded) request target.
func uploadGet(srv *echo.Echo, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	// Parse the target the way net/http does, so that a path such as
	// "/uploads/../config.toml" reaches the handler as it was sent.
	u, err := url.ParseRequestURI(target)
	if err != nil {
		panic(err)
	}
	req.URL = u
	req.RequestURI = target
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// Uploaded file names keep every character except whitespace (makeFilename),
// and the links to them are already in sent e-mails. They have to keep
// resolving however the client encodes them.
func TestUploadDirServesEncodedFileNames(t *testing.T) {
	korean := makeFilename("한글 이미지.png")
	if korean != "한글-이미지.png" {
		t.Fatalf("makeFilename = %q", korean)
	}
	srv := newUploadTestServer(t, map[string]string{
		korean:        "korean",
		"a(1)+b.png":  "parens",
		"plain.png":   "plain",
		"café,@x.png": "accent",
	})

	for target, want := range map[string]string{
		"/uploads/plain.png": "plain",
		// UTF-8 name, percent-encoded upper and lower case, and sent raw.
		"/uploads/%ED%95%9C%EA%B8%80-%EC%9D%B4%EB%AF%B8%EC%A7%80.png": "korean",
		"/uploads/%ed%95%9c%ea%b8%80-%ec%9d%b4%eb%af%b8%ec%a7%80.png": "korean",
		"/uploads/" + korean: "korean",
		// Characters that Go does not escape by default but clients may.
		"/uploads/a%281%29%2Bb.png":     "parens",
		"/uploads/a(1)+b.png":           "parens",
		"/uploads/caf%C3%A9%2C%40x.png": "accent",
		"/uploads/caf%c3%a9,@x.png":     "accent",
	} {
		rec := uploadGet(srv, target)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("GET %s = %d %q, want 200 %q", target, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestUploadDirRejectsTraversal(t *testing.T) {
	srv := newUploadTestServer(t, map[string]string{"plain.png": "plain"})

	for _, target := range []string{
		"/uploads/../config.toml",
		"/uploads/%2e%2e/config.toml",
		"/uploads/%2E%2E%2Fconfig.toml",
		"/uploads/..%2fconfig.toml",
		"/uploads/%252e%252e/config.toml",
		"/uploads/./plain.png",
		"/uploads//plain.png",
		"/uploads/missing.png",
	} {
		rec := uploadGet(srv, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, rec.Code, rec.Body.String())
		}
	}
}

const (
	testSVG = `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script></svg>`
	testPNG = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	testPDF = "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"
)

// Every uploaded file is sandboxed. Images and PDFs render inline, anything
// else (an HTML page with script, most of all) is downloaded instead of shown.
func TestUploadSecurityHeaders(t *testing.T) {
	srv := newUploadTestServer(t, map[string]string{
		"a.png":      testPNG,
		"a.svg":      testSVG,
		"a.pdf":      testPDF,
		"a.html":     "<script>alert(document.cookie)</script>",
		"a.txt":      "hello",
		"a.csv":      "a,b\n1,2\n",
		"noext":      "<html><script>alert(1)</script></html>",
		"한글-이미지.png": testPNG,
	}, uploadSecurityHeaders)

	for _, tc := range []struct {
		target, ctype, disposition string
	}{
		{"/uploads/a.png", "image/png", "inline"},
		{"/uploads/a.svg", "image/svg+xml", "inline"},
		{"/uploads/a.pdf", "application/pdf", "inline"},
		{"/uploads/%ED%95%9C%EA%B8%80-%EC%9D%B4%EB%AF%B8%EC%A7%80.png", "image/png", "inline"},
		{"/uploads/a.html", "text/html; charset=utf-8", "attachment"},
		{"/uploads/a.txt", "text/plain; charset=utf-8", "attachment"},
		{"/uploads/a.csv", "text/csv; charset=utf-8", "attachment"},
		{"/uploads/noext", "text/html; charset=utf-8", "attachment"},
	} {
		rec := uploadGet(srv, tc.target)
		h := rec.Header()
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.target, rec.Code)
			continue
		}
		if got := h.Get("Content-Type"); got != tc.ctype {
			t.Errorf("GET %s Content-Type = %q, want %q", tc.target, got, tc.ctype)
		}
		if got := h.Get("Content-Disposition"); got != tc.disposition {
			t.Errorf("GET %s Content-Disposition = %q, want %q", tc.target, got, tc.disposition)
		}
		if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "sandbox" {
			t.Errorf("GET %s Content-Security-Policy = %q, want [sandbox]", tc.target, got)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", tc.target, got)
		}
	}

	// Errors are sandboxed too and are not rendered.
	rec := uploadGet(srv, "/uploads/missing.html")
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Security-Policy") != "sandbox" ||
		rec.Header().Get("Content-Disposition") != "attachment" {
		t.Errorf("GET missing = %d %v", rec.Code, rec.Header())
	}
}

// A 304 carries no Content-Type. Its headers replace the cached ones, so it
// must not turn a cached image into a download.
func TestUploadSecurityHeadersNotModified(t *testing.T) {
	srv := newUploadTestServer(t, map[string]string{"a.png": testPNG}, uploadSecurityHeaders)

	first := uploadGet(srv, "/uploads/a.png")
	lastMod := first.Header().Get("Last-Modified")
	if first.Code != http.StatusOK || lastMod == "" {
		t.Fatalf("GET = %d, Last-Modified %q", first.Code, lastMod)
	}

	rec := uploadGet(srv, "/uploads/a.png", "If-Modified-Since", lastMod)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", rec.Code)
	}
	if got, ok := rec.Header()["Content-Disposition"]; ok {
		t.Errorf("304 Content-Disposition = %q, want none", got)
	}
	if rec.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("304 lost the sandbox header: %v", rec.Header())
	}
}

// The middleware also covers media proxied from S3, where the handler sets
// the Content-Type itself.
func TestUploadSecurityHeadersStream(t *testing.T) {
	srv := echo.New()
	srv.GET("/media/:filepath", func(c echo.Context) error {
		key := c.Param("filepath")
		body := map[string]string{"a.svg": testSVG, "a.html": "<script>alert(1)</script>", "a.png": testPNG}[key]
		return c.Stream(http.StatusOK, detectMediaContentType(key, []byte(body)), strings.NewReader(body))
	}, uploadSecurityHeaders)

	for target, want := range map[string][2]string{
		"/media/a.svg":  {"image/svg+xml", "inline"},
		"/media/a.png":  {"image/png", "inline"},
		"/media/a.html": {"text/html; charset=utf-8", "attachment"},
	} {
		h := uploadGet(srv, target).Header()
		if h.Get("Content-Type") != want[0] || h.Get("Content-Disposition") != want[1] ||
			h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s headers = %v, want %v", target, h, want)
		}
	}
}

func TestDetectMediaContentType(t *testing.T) {
	for _, tc := range []struct {
		key, body, want string
	}{
		// Sniffed as text: the extension decides.
		{"logo.svg", testSVG, "image/svg+xml"},
		{"logo.svg", `<?xml version="1.0"?>` + testSVG, "image/svg+xml"},
		{"data.csv", "a,b\n1,2\n", "text/csv; charset=utf-8"},
		// No usable extension: the sniffed type stays.
		{"notes", "hello", "text/plain; charset=utf-8"},
		{"blob.zzzz", "\x00\x01\x02", "application/octet-stream"},
		// A specific sniffed type wins over a misleading extension.
		{"fake.svg", testPNG, "image/png"},
		{"page.png", "<html><script>alert(1)</script></html>", "text/html; charset=utf-8"},
	} {
		if got := detectMediaContentType(tc.key, []byte(tc.body)); got != tc.want {
			t.Errorf("detectMediaContentType(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// Campaign and template previews may carry arbitrary script. Opened directly
// (outside the admin's sandboxed iframe) the response itself has to sandbox it.
func TestRenderSandboxedHTML(t *testing.T) {
	srv := echo.New()
	srv.GET("/preview", func(c echo.Context) error {
		return renderSandboxedHTML(c, "<script>alert(1)</script>")
	})

	rec := uploadGet(srv, "/preview")
	if rec.Code != http.StatusOK || rec.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=UTF-8" {
		t.Errorf("Content-Type = %q", got)
	}
	want := "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox"
	if got := rec.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != want {
		t.Errorf("Content-Security-Policy = %q, want %q", got, want)
	}
}
