package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

const sandboxCSP = "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox"

// Every page that renders a campaign or template body can carry script the
// author wrote. Each of the real handlers has to answer with the sandbox
// header: this runs them against a database, not the helper they share.
func TestCampaignBodyPagesAreSandboxed(t *testing.T) {
	e := newTestSendEnv(t)
	admin := e.as(e.superAdmin)
	const script = `<script>document.title = document.cookie</script>`
	e.db.MustExec(`UPDATE campaigns SET body = $2 WHERE id = $1`, e.camp, `<p>Hello {{ .Subscriber.Name }}</p>`+script)
	var tplID int
	e.get(&tplID, `INSERT INTO templates (name, type, subject, body) VALUES ('T', 'campaign', '', $1) RETURNING id`,
		`<div class="tpl">{{ template "content" . }}</div>`+script)

	post := func(form url.Values) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		return req
	}
	get := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/x", nil) }

	for _, tc := range []struct {
		name   string
		h      echo.HandlerFunc
		req    *http.Request
		id     int
		params map[string]string
		body   string // text the page must contain
	}{
		{"view in browser", e.a.ViewCampaignMessage, get(), 0,
			map[string]string{"campUUID": e.campUUID, "subUUID": e.subUUID(e.mine)}, "Hello 김하늘"},
		{"campaign preview GET", e.a.PreviewCampaign, get(), e.camp, nil, "<p>Hello "},
		{"campaign preview POST", e.a.PreviewCampaign,
			post(url.Values{"content_type": {"html"}, "body": {"<p>posted body</p>" + script}}), e.camp, nil, "posted body"},
		{"archive preview", e.a.PreviewCampaignArchive,
			post(url.Values{"archive_meta": {`{"name":"Archive Reader","email":"reader@example.invalid"}`}}), e.camp, nil, "Hello Archive Reader"},
		{"template preview", e.a.PreviewTemplate, get(), tplID, nil, `<div class="tpl">`},
		{"template body preview", e.a.PreviewTemplateBody,
			post(url.Values{"template_type": {"campaign"}, "body": {`<div class="posted">{{ template "content" . }}</div>` + script}}), 0, nil, `<div class="posted">`},
	} {
		rec, he := e.do(tc.h, admin, tc.req, tc.id, tc.params)
		if he != nil {
			t.Errorf("%s: error %d %v", tc.name, he.Code, he.Message)
			continue
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: %d %q, want 200 with %q", tc.name, rec.Code, rec.Body.String(), tc.body)
		}
		if got := rec.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != sandboxCSP {
			t.Errorf("%s: Content-Security-Policy = %q, want %q", tc.name, got, sandboxCSP)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("%s: Content-Type = %q", tc.name, got)
		}
	}

	// A plain text campaign is not rendered as HTML at all.
	rec, he := e.do(e.a.PreviewCampaign, admin, post(url.Values{"content_type": {"plain"}, "body": {"plain " + script}}), e.camp, nil)
	if he != nil || rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("plain preview: %v %d %q", he, rec.Code, rec.Header().Get("Content-Type"))
	}
}
