package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
