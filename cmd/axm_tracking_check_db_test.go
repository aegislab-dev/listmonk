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
	"github.com/lib/pq"
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
	e.a.cfg = &Config{FromEmail: "App <app@example.invalid>"}

	var bare, tracked int
	e.get(&bare, `INSERT INTO templates(name,type,subject,body) VALUES('bare','campaign','','<html><body>{{ template "content" . }}<a href="{{ UnsubscribeURL }}">leave</a></body></html>') RETURNING id`)
	e.get(&tracked, `INSERT INTO templates(name,type,subject,body) VALUES('tracked','campaign','','<html><body>{{ template "content" . }}{{ TrackView }}</body></html>') RETURNING id`)
	const stored = `<p><a href="https://a.example.test/x">A</a> <a href="https://b.example.test/y@TrackLink">B</a></p>`
	e.db.MustExec(`UPDATE campaigns SET status='draft', content_type='richtext', body=$2, template_id=$3, from_email='Test <t@example.invalid>' WHERE id=$1`, e.campID, stored, bare)
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

	// A body can read the campaign itself. The unsaved subject, name, tags, headers and
	// attributes of the editor decide with it; left out, the stored ones do.
	cond := map[string]any{"content_type": "html", "template_id": bare,
		"body": `{{ if eq .Campaign.Subject "with-pixel" }}{{ TrackView }}{{ end }}` +
			`{{ if .Campaign.Attribs.cta }}<a href="https://a.example.test/{{ .Campaign.Name }}/{{ index .Campaign.Tags 0 }}">A</a>{{ end }}`}
	if rep = check(js(cond)); rep.View != "missing" || rep.Links.Total != 0 {
		t.Fatalf("stored subject and attributes: %+v", rep)
	}
	cond["subject"], cond["name"], cond["tags"], cond["attribs"] = "with-pixel", "nm", []string{"tg"}, map[string]any{"cta": true}
	cond["headers"], cond["altbody"], cond["from_email"] = []map[string]string{{"X-A": "b"}}, "alt", "A <a@example.test>"
	if rep = check(js(cond)); rep.View != "manual" || !slices.Equal(rep.Links.UntrackedURLs, []string{"https://a.example.test/nm/tg"}) {
		t.Fatalf("unsaved subject, name, tags and attributes: %+v", rep)
	}

	// Custom headers: saving keeps a stored header that the request no longer has, in
	// the set it was in; a test send replaces them. The check follows each.
	e.db.MustExec(`UPDATE campaigns SET headers='[{"X-No-Track":"1"}]' WHERE id=$1`, e.campID)
	hdr := map[string]any{"content_type": "html", "template_id": bare, "headers": []map[string]string{{"X-Other": "1"}},
		"body": `{{ $no := false }}{{ range .Campaign.Headers }}{{ if index . "X-No-Track" }}{{ $no = true }}{{ end }}{{ end }}{{ if not $no }}{{ TrackView }}{{ end }}`}
	if rep = check(js(hdr)); rep.View != "missing" {
		t.Fatalf("headers as saving keeps them: %+v", rep)
	}
	hdr["mode"] = "test"
	if rep = check(js(hdr)); rep.View != "manual" {
		t.Fatalf("headers as a test send replaces them: %+v", rep)
	}
	e.db.MustExec(`UPDATE campaigns SET headers='[]' WHERE id=$1`, e.campID)

	// The unsaved template is also the one the campaign names to its templates.
	byTpl := map[string]any{"content_type": "html", "template_id": bare,
		"body": fmt.Sprintf(`{{ if eq .Campaign.TemplateID.Int %d }}{{ TrackView }}{{ end }}<p>x</p>`, bare)}
	if rep = check(js(byTpl)); rep.View != "manual" {
		t.Fatalf("stored template id: %+v", rep)
	}
	e.db.MustExec(`INSERT INTO templates(name,type,subject,body) VALUES('bare2','campaign','','<html><body>{{ template "content" . }}</body></html>')`)
	var bare2 int
	e.get(&bare2, `SELECT id FROM templates WHERE name='bare2'`)
	byTpl["template_id"] = bare2
	if rep = check(js(byTpl)); rep.View != "missing" {
		t.Fatalf("unsaved template id: %+v", rep)
	}

	// The send reads the campaign without its lists and with the default template when it
	// has none; a test send reads it like the preview, with the lists and no template.
	// The page's archive setting counts like any other saved value (LST-44 review).
	lists := map[string]any{"content_type": "html", "template_id": bare, "body": `{{ if .Campaign.Lists }}{{ TrackView }}{{ end }}<p>x</p>`}
	if rep = check(js(lists)); rep.View != "missing" {
		t.Fatalf("lists, real send: %+v", rep)
	}
	lists["mode"] = "test"
	if rep = check(js(lists)); rep.View != "manual" {
		t.Fatalf("lists, test send: %+v", rep)
	}
	var tplBefore int
	e.get(&tplBefore, `SELECT template_id FROM campaigns WHERE id=$1`, e.campID)
	var defTpl int
	e.get(&defTpl, `INSERT INTO templates(name,type,subject,body) VALUES('deftpl','campaign','','<html><body>{{ template "content" . }}{{ TrackView }}</body></html>') RETURNING id`)
	var oldDefault []int
	e.db.Select(&oldDefault, `SELECT id FROM templates WHERE is_default`)
	e.db.MustExec(`UPDATE templates SET is_default = (id = $1)`, defTpl)
	e.db.MustExec(`UPDATE campaigns SET template_id=NULL, content_type='html', body='<p>x</p>' WHERE id=$1`, e.campID)
	if rep = check(`{}`); rep.View != "manual" {
		t.Fatalf("no template, real send uses the default: %+v", rep)
	}
	if rep = check(`{"mode":"test"}`); rep.View != "missing" {
		t.Fatalf("no template, test send has none: %+v", rep)
	}
	e.db.MustExec(`UPDATE templates SET is_default = COALESCE(id = ANY($1::INT[]), false)`, pq.Array(oldDefault))
	e.db.MustExec(`UPDATE campaigns SET template_id=$2, content_type='richtext', body=$3 WHERE id=$1`, e.campID, tplBefore, stored)
	arch := map[string]any{"content_type": "html", "template_id": bare, "body": `{{ if not .Campaign.Archive }}{{ TrackView }}{{ end }}<p>x</p>`}
	if rep = check(js(arch)); rep.View != "manual" {
		t.Fatalf("archive off: %+v", rep)
	}
	arch["archive"] = true
	if rep = check(js(arch)); rep.View != "missing" {
		t.Fatalf("archive switched on on the page: %+v", rep)
	}

	// A template can read the status, which the start changes: the check renders the
	// campaign as the manager sends it (started: "running"; with a send time: the row as
	// it was read, "scheduled"), and a test send as it is now (LST-44 review).
	st := map[string]any{"content_type": "html", "template_id": bare,
		"body": `{{ if eq .Campaign.Status "draft" }}{{ TrackView }}{{ end }}<p>x</p>`}
	if rep = check(js(st)); rep.View != "missing" {
		t.Fatalf("draft-only pixel, started: %+v", rep)
	}
	st["mode"] = "test"
	if rep = check(js(st)); rep.View != "manual" {
		t.Fatalf("draft-only pixel, test send: %+v", rep)
	}
	delete(st, "mode")
	st["body"] = `{{ if eq .Campaign.Status "scheduled" }}{{ TrackView }}{{ end }}<p>x</p>`
	for _, c := range []struct {
		status, sendAt any
		view           string
	}{
		{nil, nil, "missing"},
		{"running", nil, "missing"},
		{"scheduled", nil, "manual"},
		{"running", "2099-01-02T03:04:05Z", "manual"},
	} {
		req := map[string]any{}
		for k, v := range st {
			req[k] = v
		}
		if c.status != nil {
			req["status"] = c.status
		}
		if c.sendAt != nil {
			req["send_at"] = c.sendAt
		}
		if rep = check(js(req)); rep.View != c.view {
			t.Fatalf("status %v, send_at %v: %+v", c.status, c.sendAt, rep)
		}
	}
	// A stored send time counts unless the page takes it off.
	e.db.MustExec(`UPDATE campaigns SET send_at=NOW() + INTERVAL '30 days' WHERE id=$1`, e.campID)
	if rep = check(js(st)); rep.View != "manual" {
		t.Fatalf("stored send time: %+v", rep)
	}
	st["send_at"] = nil
	if rep = check(js(st)); rep.View != "missing" {
		t.Fatalf("send time taken off on the page: %+v", rep)
	}
	e.db.MustExec(`UPDATE campaigns SET send_at=NULL WHERE id=$1`, e.campID)
	if code, _ := e.call(e.a.CheckCampaignTracking, member, http.MethodPost, target, strconv.Itoa(e.campID), `{"status":"finished"}`); code != http.StatusBadRequest {
		t.Errorf("status finished: %d, want 400", code)
	}

	// The sender, which templates can read (LST-44 review). With sender domains, a test
	// send takes it from the account of the person who sends the test, and a start from
	// the person who starts; neither is the stored one, which the author's save set.
	// A draft that two people edit gets a pixel for its author only: the check must
	// warn the other one.
	e.a.cfg = &Config{SenderDomains: []string{"example.invalid"}, FromEmail: "App <app@example.invalid>"}
	var uc int
	e.get(&uc, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES('uc','uc@example.invalid','uc',$1,$2,'enabled','user') RETURNING id`, e.memberRole, e.team1)
	author, editor := member, e.as(uc)
	authorFrom := resolveFrom(author, "", e.a.cfg.FromEmail, e.a.cfg.SenderDomains)
	if editorFrom := resolveFrom(editor, "", e.a.cfg.FromEmail, e.a.cfg.SenderDomains); authorFrom == editorFrom || !strings.Contains(authorFrom, "ua@") {
		t.Fatalf("senders: author %q, editor %q", authorFrom, editorFrom)
	}
	var storedFrom, storedReply string
	e.get(&storedFrom, `SELECT from_email FROM campaigns WHERE id=$1`, e.campID)
	e.get(&storedReply, `SELECT reply_to FROM campaigns WHERE id=$1`, e.campID)
	e.db.MustExec(`UPDATE campaigns SET from_email=$2, reply_to='stored@example.test' WHERE id=$1`, e.campID, authorFrom)
	checkAs := func(u auth.User, body map[string]any) manager.TrackingReport {
		t.Helper()
		code, data := e.call(e.a.CheckCampaignTracking, u, http.MethodPost, target, strconv.Itoa(e.campID), js(body))
		var rep manager.TrackingReport
		if code != http.StatusOK || json.Unmarshal(data, &rep) != nil {
			t.Fatalf("%v: %d %s", body, code, data)
		}
		return rep
	}
	byFrom := map[string]any{"content_type": "html", "template_id": bare,
		"body": fmt.Sprintf(`{{ if eq .Campaign.FromEmail %q }}{{ TrackView }}{{ end }}<p>x</p>`, authorFrom)}
	for _, mode := range []string{"test", ""} {
		byFrom["mode"] = mode
		if rep = checkAs(author, byFrom); rep.View != "manual" {
			t.Fatalf("mode %q, the author: %+v", mode, rep)
		}
		if rep = checkAs(editor, byFrom); rep.View != "missing" {
			t.Fatalf("mode %q, another editor: %+v", mode, rep)
		}
	}
	// A campaign the author scheduled is sent with the stored sender, whoever looks at
	// it: another person's check (stored or with the page) must not read theirs.
	e.db.MustExec(`UPDATE campaigns SET status='scheduled', send_at=NOW() + INTERVAL '30 days' WHERE id=$1`, e.campID)
	delete(byFrom, "mode")
	for _, body := range []map[string]any{{}, {"status": "scheduled"}, byFrom} {
		probe := map[string]any{}
		for k, v := range body {
			probe[k] = v
		}
		probe["content_type"], probe["template_id"], probe["body"] = "html", bare, byFrom["body"]
		if len(body) == 0 {
			// The stored body: put the probe there.
			e.db.MustExec(`UPDATE campaigns SET content_type='html', body=$2 WHERE id=$1`, e.campID, byFrom["body"])
			probe = map[string]any{}
		}
		if rep = checkAs(editor, probe); rep.View != "manual" {
			t.Fatalf("scheduled by the author, %v checked by another person: %+v", body, rep)
		}
	}
	e.db.MustExec(`UPDATE campaigns SET status='draft', send_at=NULL, content_type='richtext', body=$2 WHERE id=$1`, e.campID, stored)

	// The reply-to of a test send is the one in the request, read as the send reads it;
	// none in the request is none, not the stored one.
	byReply := map[string]any{"content_type": "html", "template_id": bare, "mode": "test",
		"body": `{{ if eq .Campaign.ReplyTo "a@example.test" }}{{ TrackView }}{{ end }}{{ if eq .Campaign.ReplyTo "stored@example.test" }}<a href="https://a.example.test/stored">s</a>{{ end }}<p>x</p>`}
	if rep = checkAs(author, byReply); rep.View != "missing" || rep.Links.Total != 0 {
		t.Fatalf("test send without a reply-to: %+v", rep)
	}
	byReply["reply_to"] = " <a@example.test> "
	if rep = checkAs(author, byReply); rep.View != "manual" {
		t.Fatalf("test send with a reply-to: %+v", rep)
	}
	// Saving keeps the stored reply-to unless the request has one.
	delete(byReply, "mode")
	delete(byReply, "reply_to")
	if rep = checkAs(author, byReply); rep.View != "missing" || rep.Links.Total != 1 {
		t.Fatalf("saved reply-to: %+v", rep)
	}
	// A reply-to that is still being typed: saving would refuse it and keep the stored
	// one, so the check reads the stored one; a test send with it fails, so does its check.
	byReply["reply_to"] = "not an address"
	if rep = checkAs(author, byReply); rep.View != "missing" || rep.Links.Total != 1 {
		t.Fatalf("invalid reply-to on the page: %+v", rep)
	}
	byReply["mode"] = "test"
	if code, _ := e.call(e.a.CheckCampaignTracking, author, http.MethodPost, target, strconv.Itoa(e.campID), js(byReply)); code != http.StatusBadRequest {
		t.Errorf("invalid reply-to in a test send: %d, want 400", code)
	}
	e.db.MustExec(`UPDATE campaigns SET from_email=$2, reply_to=$3 WHERE id=$1`, e.campID, storedFrom, storedReply)
	e.a.cfg = &Config{FromEmail: "App <app@example.invalid>"}

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
	on := e.a.manager
	e.a.manager = e.startManagerWith(e.store, &bodyMessenger{}, io.Discard, func(c *manager.Config) { c.DisableTracking = true })
	code, data := e.call(e.a.CheckCampaignTracking, member, http.MethodPost, target, strconv.Itoa(e.campID), `{}`)
	e.a.manager = on
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
