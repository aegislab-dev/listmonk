package manager

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
)

// A template like static/email-templates/default.tpl: it carries the view pixel.
const trackedTpl = `<html><body>{{ template "content" . }}<a href="{{ UnsubscribeURL }}">Unsubscribe</a> <a href="{{ MessageURL }}">Browser</a>{{ TrackView }}</body></html>`
const untrackedTpl = `<html><body>{{ template "content" . }}<a href="{{ UnsubscribeURL }}">Unsubscribe</a></body></html>`

func checkTracking(t *testing.T, m *Manager, c models.Campaign) TrackingReport {
	t.Helper()
	rep, err := m.CheckTracking(&c, models.Subscriber{UUID: dummyUUID, Name: "Alice", Email: "alice@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestCheckTracking(t *testing.T) {
	const two = `<p><a href="https://a.test/x@TrackLink">A</a> <a href="{{ TrackLink "https://b.test/y?p=1&amp;q=2" }}">B</a></p>`
	tests := []struct {
		name string
		c    models.Campaign
		set  func(*Config)
		want TrackingReport
	}{
		{"richtext, template with TrackView, both links tracked",
			models.Campaign{ContentType: "richtext", Messenger: "email", TemplateBody: trackedTpl, Body: two}, nil,
			TrackingReport{State: "ok", View: "manual", Links: TrackingLinks{Total: 2, Manual: 2}}},
		{"richtext, template without TrackView, plain links",
			models.Campaign{ContentType: "richtext", Messenger: "email", TemplateBody: untrackedTpl,
				Body: `<a href="https://a.test/x?p=1&amp;q=2">A</a><a href="https://a.test/x?p=1&amp;q=2">again</a><a href="https://b.test/y">B</a>`}, nil,
			TrackingReport{State: "warn", View: "missing", Links: TrackingLinks{Total: 2, Untracked: 2,
				UntrackedURLs: []string{"https://a.test/x?p=1&q=2", "https://b.test/y"}}}},
		{"the pixel counts only when it is rendered",
			models.Campaign{ContentType: "html", Messenger: "email", Body: `{{ if false }}{{ TrackView }}{{ end }}<p>Hello</p>`}, nil,
			TrackingReport{State: "warn", View: "missing"}},
		{"a pixel in the body, no template, no links",
			models.Campaign{ContentType: "html", Messenger: "email", Body: `<p>Hello</p>{{ TrackView }}`}, nil,
			TrackingReport{State: "ok", View: "manual"}},
		{"markdown shorthand",
			models.Campaign{ContentType: "markdown", Messenger: "email", TemplateBody: trackedTpl,
				Body: "[a](https://x.test@TrackLink) and [b](https://y.test/plain)"}, nil,
			TrackingReport{State: "warn", View: "manual", Links: TrackingLinks{Total: 2, Manual: 1, Untracked: 1,
				UntrackedURLs: []string{"https://y.test/plain"}}}},
		{"visual e-mail is tracked at send time",
			models.Campaign{ContentType: "visual", Messenger: "email", TemplateBody: untrackedTpl,
				Body: `<html><body><a href="https://a.test/x">A</a><a href="https://b.test/y@TrackLink">B</a><map><area href="https://c.test/z"></map></body></html>`}, nil,
			TrackingReport{State: "ok", Auto: true, View: "auto", Links: TrackingLinks{Total: 3, Manual: 1, Auto: 2}}},
		{"visual on a named SMTP server, own pixel",
			models.Campaign{ContentType: "visual", Messenger: "email-ses", Body: `<a href="https://a.test/x">A</a>{{ TrackView }}`}, nil,
			TrackingReport{State: "ok", Auto: true, View: "manual", Links: TrackingLinks{Total: 1, Auto: 1}}},
		{"visual on another messenger is not tracked automatically",
			models.Campaign{ContentType: "visual", Messenger: "webhook", Body: `<a href="https://a.test/x">A</a>`}, nil,
			TrackingReport{State: "warn", View: "n/a", Links: TrackingLinks{Total: 1, Untracked: 1, UntrackedURLs: []string{"https://a.test/x"}}}},
		{"plain text cannot record views",
			models.Campaign{ContentType: "plain", Messenger: "email",
				Body: "Offer: https://x.test/o?a=1&b=2@TrackLink\nNotice: https://y.test/n.\nLeave: {{ UnsubscribeURL }}\n"}, nil,
			TrackingReport{State: "warn", View: "unsupported", Links: TrackingLinks{Total: 2, Manual: 1, Untracked: 1,
				UntrackedURLs: []string{"https://y.test/n"}}}},
		{"plain text with every link tracked is not a warning",
			models.Campaign{ContentType: "plain", Messenger: "email", Body: "Offer: https://x.test/o@TrackLink\n"}, nil,
			TrackingReport{State: "ok", View: "unsupported", Links: TrackingLinks{Total: 1, Manual: 1}}},
		{"links that are never tracked are not counted",
			models.Campaign{ContentType: "richtext", Messenger: "email", TemplateBody: trackedTpl,
				Body: `<a href="{{ UnsubscribeURL }}">u</a><a href="{{ ManageURL }}">m</a><a href="{{ MessageURL }}">v</a><a href="{{ OptinURL }}">o</a>` +
					`<a href="mailto:a@example.test">mail</a><a href="tel:+8210">tel</a><a href="#top">top</a><a href="/relative">rel</a>` +
					`<img src="https://img.test/a.png"><a title='x href="https://quoted.test/"' href="#x">q</a>`}, nil,
			TrackingReport{State: "ok", View: "manual"}},
		{"another site with a service-like path is a link",
			models.Campaign{ContentType: "html", Messenger: "email", Body: `{{ TrackView }}<a href="https://other.test/link/abc">x</a>`}, nil,
			TrackingReport{State: "warn", View: "manual", Links: TrackingLinks{Total: 1, Untracked: 1, UntrackedURLs: []string{"https://other.test/link/abc"}}}},
		{"tracking disabled globally",
			models.Campaign{ContentType: "visual", Messenger: "email", Body: `<a href="https://a.test/x">A</a>{{ TrackView }}`},
			func(c *Config) { c.DisableTracking = true },
			TrackingReport{State: "off", Disabled: true, View: "off"}},
		{"individual tracking off does not change the result",
			models.Campaign{ContentType: "richtext", Messenger: "email", TemplateBody: trackedTpl, Body: two},
			func(c *Config) { c.IndividualTracking = false },
			TrackingReport{State: "ok", View: "manual", Links: TrackingLinks{Total: 2, Manual: 2}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, store := trackingManager()
			if tc.set != nil {
				tc.set(&m.cfg)
			}
			c := tc.c
			c.UUID = "real-campaign"
			got := checkTracking(t, m, c)

			want := tc.want
			want.Individual = m.cfg.IndividualTracking
			want.Applicable = isEmailMessenger(c.Messenger)
			want.ContentType, want.Messenger = c.ContentType, c.Messenger
			if want.Links.UntrackedURLs == nil {
				want.Links.UntrackedURLs = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got  %+v\nwant %+v", got, want)
			}
			// The check registers nothing and leaves the campaign uncompiled.
			if len(store.urls) != 0 || len(m.links) != 0 {
				t.Fatalf("registered links: %v %v", store.urls, m.links)
			}
			if c.Tpl != nil || c.SubjectTpl != nil || c.UUID != "real-campaign" {
				t.Fatal("changed the campaign that was passed in")
			}
		})
	}
}

// The check must agree with what a real send produces: a link it calls tracked goes
// through /link/, an untracked one stays direct, and the pixel is where it says.
func TestCheckTrackingMatchesSend(t *testing.T) {
	body := `<a href="https://a.test/x@TrackLink">A</a><a href="https://b.test/y">B</a>`
	for _, tc := range []struct {
		contentType, messenger, tpl string
	}{
		{"richtext", "email", trackedTpl}, {"richtext", "email", untrackedTpl}, {"html", "email-ses", ""},
		{"visual", "email", ""}, {"visual", "webhook", ""}, {"markdown", "email", trackedTpl},
	} {
		t.Run(tc.contentType+"/"+tc.messenger, func(t *testing.T) {
			m, _ := trackingManager()
			c := models.Campaign{UUID: "campaign", ContentType: tc.contentType, Messenger: tc.messenger, TemplateBody: tc.tpl, Body: body}
			rep := checkTracking(t, m, c)
			msg := renderTrackingCampaign(t, m, &c, "subscriber")
			out := string(msg.Body())

			sentPixel := strings.Contains(out, "/campaign/campaign/subscriber/px.png")
			if said := rep.View == TrackingViewManual || rep.View == TrackingViewAuto; said != sentPixel {
				t.Errorf("view %q, but pixel sent = %v: %s", rep.View, sentPixel, out)
			}
			if tracked := strings.Count(out, "/link/link-"); tracked != rep.Links.Manual+rep.Links.Auto {
				t.Errorf("report %+v, but %d tracked links were sent: %s", rep.Links, tracked, out)
			}
			for _, u := range rep.Links.UntrackedURLs {
				if !strings.Contains(out, `href="`+u+`"`) {
					t.Errorf("%s is reported untracked but was not sent direct: %s", u, out)
				}
			}
		})
	}
}

func TestCheckTrackingCapsUntrackedList(t *testing.T) {
	m, _ := trackingManager()
	var b strings.Builder
	for i := range 30 {
		b.WriteString(`<a href="https://a.test/` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `">x</a>`)
	}
	rep := checkTracking(t, m, models.Campaign{ContentType: "html", Messenger: "email", Body: b.String()})
	if rep.Links.Untracked != 30 || len(rep.Links.UntrackedURLs) != maxUntrackedURLs {
		t.Fatalf("got %d untracked, %d listed", rep.Links.Untracked, len(rep.Links.UntrackedURLs))
	}
}

func TestCheckTrackingErrors(t *testing.T) {
	m, _ := trackingManager()
	sub := models.Subscriber{UUID: dummyUUID}

	_, err := m.CheckTracking(&models.Campaign{ContentType: "html", Messenger: "email", Body: `{{ .Subscriber.Name }`}, sub)
	var checkErr *TrackingCheckError
	if !errors.As(err, &checkErr) || !checkErr.Compile {
		t.Fatalf("want a compile error, got %v", err)
	}

	_, err = m.CheckTracking(&models.Campaign{ContentType: "html", Messenger: "email", Body: `{{ .Subscriber.NoSuchField }}`}, sub)
	if !errors.As(err, &checkErr) || checkErr.Compile {
		t.Fatalf("want a render error, got %v", err)
	}
}
