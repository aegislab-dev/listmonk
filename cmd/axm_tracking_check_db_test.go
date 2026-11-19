package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/labstack/echo/v4"
)

// The pre-send tracking check (LST-44) through the real handler, a real manager and a
// database: it reads the unsaved editor state, answers per case, and writes nothing.
func TestCampaignTrackingCheck(t *testing.T) {
	e := newTeamsEnv(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "i18n", "ko.json"))
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.a.i18n = lang
	e.a.log = log.New(io.Discard, "", 0)
	e.a.manager = e.startManager(e.store, &bodyMessenger{}, io.Discard)

	var bare, tracked int
	e.get(&bare, `INSERT INTO templates(name,type,subject,body) VALUES('bare','campaign','','<html><body>{{ template "content" . }}<a href="{{ UnsubscribeURL }}">leave</a></body></html>') RETURNING id`)
	e.get(&tracked, `INSERT INTO templates(name,type,subject,body) VALUES('tracked','campaign','','<html><body>{{ template "content" . }}{{ TrackView }}</body></html>') RETURNING id`)
	const stored = `<p><a href="https://a.example.test/x">A</a> <a href="https://b.example.test/y@TrackLink">B</a></p>`
	e.db.MustExec(`UPDATE campaigns SET status='draft', content_type='richtext', body=$2, template_id=$3 WHERE id=$1`, e.campID, stored, bare)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, e.campID, e.aList)

	type counts struct{ Links, Views, Clicks, Sends int }
	count := func() (c counts) {
		e.get(&c.Links, `SELECT COUNT(*) FROM links`)
		e.get(&c.Views, `SELECT COUNT(*) FROM campaign_views`)
		e.get(&c.Clicks, `SELECT COUNT(*) FROM link_clicks`)
		e.get(&c.Sends, `SELECT COUNT(*) FROM axm_campaign_sends`)
		return c
	}
	before := count()
	var rowBefore string
	e.get(&rowBefore, `SELECT md5(campaigns::TEXT) FROM campaigns WHERE id=$1`, e.campID)

	member, other := e.as(e.ua), e.as(e.ub)
	target := fmt.Sprintf("/api/campaigns/%d/tracking-check", e.campID)
	check := func(body string) manager.TrackingReport {
		t.Helper()
		code, data := e.call(e.a.CheckCampaignTracking, member, http.MethodPost, target, strconv.Itoa(e.campID), body)
		var rep manager.TrackingReport
		if code != http.StatusOK || json.Unmarshal(data, &rep) != nil {
			t.Fatalf("%s: %d %s", body, code, data)
		}
		return rep
	}
	js := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	// Nothing in the request: the stored draft. No pixel in its template, one plain link.
	rep := check(`{}`)
	if rep.State != "warn" || rep.View != "missing" || rep.Links.Total != 2 || rep.Links.Manual != 1 ||
		!slices.Equal(rep.Links.UntrackedURLs, []string{"https://a.example.test/x"}) || !rep.Individual || !rep.Applicable {
		t.Fatalf("stored draft: %+v", rep)
	}

	// The unsaved body and template of the editor replace the stored ones.
	rep = check(js(map[string]any{"content_type": "richtext", "template_id": tracked,
		"body": `<a href="https://a.example.test/x@TrackLink">A</a>`}))
	if rep.State != "ok" || rep.View != "manual" || rep.Links.Total != 1 || rep.Links.Manual != 1 || rep.Links.Untracked != 0 {
		t.Fatalf("edited body with a tracked template: %+v", rep)
	}
	rep = check(js(map[string]any{"content_type": "html", "template_id": bare, "body": `<p>Hi</p>{{ TrackView }}`}))
	if rep.State != "ok" || rep.View != "manual" || rep.Links.Total != 0 {
		t.Fatalf("pixel in the edited body: %+v", rep)
	}

	// A visual e-mail is tracked at send time and ignores the template; on another
	// messenger it is not.
	visual := map[string]any{"content_type": "visual", "template_id": bare, "body": `<html><body><a href="https://a.example.test/x">A</a></body></html>`}
	rep = check(js(visual))
	if rep.State != "ok" || rep.View != "auto" || !rep.Auto || rep.Links.Auto != 1 {
		t.Fatalf("visual e-mail: %+v", rep)
	}
	visual["messenger"] = "webhook"
	rep = check(js(visual))
	if rep.State != "warn" || rep.View != "n/a" || rep.Applicable || rep.Auto || rep.Links.Untracked != 1 || rep.Messenger != "webhook" {
		t.Fatalf("visual on another messenger: %+v", rep)
	}

	rep = check(js(map[string]any{"content_type": "plain", "body": "see https://a.example.test/x and https://b.example.test/y@TrackLink"}))
	if rep.State != "warn" || rep.View != "unsupported" || rep.Links.Manual != 1 || rep.Links.Untracked != 1 {
		t.Fatalf("plain text: %+v", rep)
	}

	// The same request as a form post, the way the preview is posted.
	form := url.Values{"content_type": {"richtext"}, "template_id": {strconv.Itoa(tracked)}, "body": {`<a href="https://a.example.test/x">A</a>`}}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, member)
	c.Set("id", e.campID)
	if err := e.a.CheckCampaignTracking(c); err != nil {
		t.Fatal(err)
	}
	var out struct{ Data manager.TrackingReport }
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Data.State != "warn" || out.Data.View != "manual" || out.Data.Links.Untracked != 1 {
		t.Fatalf("form post: %s", rec.Body.String())
	}

	// What cannot be rendered cannot be checked: the same 400 as the preview.
	for _, bad := range []string{
		js(map[string]any{"content_type": "html", "body": `{{ .Subscriber.Name }`}),
		js(map[string]any{"content_type": "html", "body": `{{ .Subscriber.NoSuchField }}`}),
		js(map[string]any{"content_type": "pdf", "body": `x`}),
	} {
		if code, _ := e.call(e.a.CheckCampaignTracking, member, http.MethodPost, target, strconv.Itoa(e.campID), bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}

	// Another team's member cannot check this campaign.
	if code, _ := e.call(e.a.CheckCampaignTracking, other, http.MethodPost, target, strconv.Itoa(e.campID), `{}`); code != http.StatusForbidden {
		t.Errorf("other team: %d, want 403", code)
	}

	// Tracking switched off for the whole instance.
	off := *e.a
	off.manager = e.startManagerWith(e.store, &bodyMessenger{}, io.Discard, func(c *manager.Config) { c.DisableTracking = true })
	code, data := e.call(off.CheckCampaignTracking, member, http.MethodPost, target, strconv.Itoa(e.campID), `{}`)
	if json.Unmarshal(data, &rep) != nil || code != http.StatusOK || rep.State != "off" || !rep.Disabled || rep.View != "off" {
		t.Fatalf("tracking disabled: %d %s", code, data)
	}

	// Nothing was written: no links registered, no views, clicks or sends, same campaign row.
	if after := count(); after != before {
		t.Fatalf("rows changed: before %+v, after %+v", before, after)
	}
	var rowAfter string
	e.get(&rowAfter, `SELECT md5(campaigns::TEXT) FROM campaigns WHERE id=$1`, e.campID)
	if rowAfter != rowBefore {
		t.Fatal("the campaign row changed")
	}
}
