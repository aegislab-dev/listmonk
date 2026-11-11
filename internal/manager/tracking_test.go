package manager

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

type trackingStore struct {
	Store
	urls []string
	fail bool
}

func (s *trackingStore) CreateLink(url string) (string, error) {
	s.urls = append(s.urls, url)
	if s.fail {
		return "", errors.New("test registration failure")
	}
	return fmt.Sprintf("link-%d", len(s.urls)), nil
}

func trackingManager() (*Manager, *trackingStore) {
	s := &trackingStore{}
	return &Manager{
		cfg: Config{
			RootURL: "https://lists.example.test", IndividualTracking: true,
			LinkTrackURL: "https://lists.example.test/link/%s/%s/%s",
			ViewTrackURL: "https://lists.example.test/campaign/%s/%s/px.png",
			UnsubURL:     "https://lists.example.test/subscription/%s/%s",
			MessageURL:   "https://lists.example.test/campaign/%s/%s",
			OptinURL:     "https://lists.example.test/subscription/optin/%s?%s",
		},
		store: s, links: map[string]string{}, log: log.New(io.Discard, "", 0),
	}, s
}

func renderTrackingCampaign(t *testing.T, m *Manager, c *models.Campaign, subUUID string) CampaignMessage {
	t.Helper()
	if err := c.CompileTemplate(m.TemplateFuncs(c)); err != nil {
		t.Fatal(err)
	}
	msg, err := m.NewCampaignMessage(c, models.Subscriber{UUID: subUUID, Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestVisualCampaignTracking(t *testing.T) {
	const body = `<html><body><a href="https://example.test/?a=1&amp;b={{ .Subscriber.Name }}">CTA</a><a href="https://example.test/?a=1&amp;b=Alice"><img src="https://example.test/image.png"></a><a href="{{ UnsubscribeURL }}">Unsubscribe</a><a href="{{ ManageURL }}">Manage</a><a href="{{ OptinURL }}">Opt-in</a><a href="{{ MessageURL }}">Browser</a></body></html>`
	for _, individual := range []bool{true, false} {
		t.Run(fmt.Sprint(individual), func(t *testing.T) {
			m, store := trackingManager()
			m.cfg.IndividualTracking = individual
			m.cfg.BusinessFooterHTML = []byte(`<a href="https://example.test/company">Company</a>`)
			c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email", Body: body,
				BodySource: null.StringFrom(`{"source":"unchanged"}`), AltBody: null.StringFrom("Alternative")}
			msg := renderTrackingCampaign(t, m, &c, "subscriber")
			subID := "subscriber"
			if !individual {
				subID = dummyUUID
			}
			out := string(msg.Body())
			if len(store.urls) != 1 || store.urls[0] != "https://example.test/?a=1&b=Alice" {
				t.Fatalf("Wrong registered URLs: %v", store.urls)
			}
			if strings.Count(out, "/link/link-1/campaign/"+subID) != 2 || strings.Count(out, "/px.png") != 1 ||
				!strings.Contains(out, "/campaign/campaign/"+subID+`/px.png"`) {
				t.Fatalf("Missing tracking: %s", out)
			}
			for _, want := range []string{`src="https://example.test/image.png"`, `href="https://lists.example.test/subscription/campaign/subscriber"`,
				`href="https://lists.example.test/subscription/campaign/subscriber?manage=true"`, `href="https://lists.example.test/subscription/optin/subscriber?"`,
				`href="https://lists.example.test/campaign/campaign/subscriber"`, `href="https://example.test/company"`} {
				if !strings.Contains(out, want) {
					t.Errorf("Changed service/image/footer URL %q: %s", want, out)
				}
			}
			if c.Body != body || c.BodySource.String != `{"source":"unchanged"}` || string(msg.AltBody()) != "Alternative" || !strings.HasSuffix(out, "Company</a></body></html>") {
				t.Fatalf("Changed stored content, alternate body or layout: %s", out)
			}
		})
	}
}

func TestVisualTrackingPerRecipient(t *testing.T) {
	m, store := trackingManager()
	c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email",
		Body: `<body><a href="https://example.test/{{ .Subscriber.UUID }}">CTA</a></body>`}
	for _, sub := range []string{"alice", "bob"} {
		msg := renderTrackingCampaign(t, m, &c, sub)
		if !strings.Contains(string(msg.Body()), "/campaign/"+sub+`/px.png"`) ||
			!strings.Contains(string(msg.Body()), "/campaign/"+sub+`"`) {
			t.Fatalf("Wrong recipient in tracking URLs: %s", msg.Body())
		}
	}
	if strings.Join(store.urls, "|") != "https://example.test/alice|https://example.test/bob" {
		t.Fatalf("Personalized destinations not registered: %v", store.urls)
	}
}

func TestVisualExplicitTracking(t *testing.T) {
	for _, body := range []string{
		`<body><a href="https://example.test/@TrackLink">CTA</a>{{ TrackView }}</body>`,
		`<body><a href='{{ TrackLink "https://example.test/" }}'>CTA</a>{{ TrackView . }}</body>`,
	} {
		m, store := trackingManager()
		c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email-custom", Body: body}
		msg := renderTrackingCampaign(t, m, &c, "subscriber")
		if len(store.urls) != 1 || store.urls[0] != "https://example.test/" || strings.Count(string(msg.Body()), "/px.png") != 1 {
			t.Fatalf("Explicit tracking was nested or duplicated: %s / %v", msg.Body(), store.urls)
		}
		before := string(msg.Body())
		m.trackVisualMessage(&msg)
		if string(msg.Body()) != before || len(store.urls) != 1 {
			t.Fatal("Transform is not idempotent")
		}
	}
}

func TestVisualTrackingScope(t *testing.T) {
	for _, tc := range []struct {
		name, format, messenger, uuid string
		disabled                      bool
	}{
		{"privacy", "visual", "email", "campaign", true},
		{"preview", "visual", "email", dummyUUID, false},
		{"no campaign", "visual", "email", "", false},
		{"other messenger", "visual", "sms", "campaign", false},
		{"not email", "visual", "emailish", "campaign", false},
		{"HTML", "html", "email", "campaign", false},
		{"richtext", "richtext", "email", "campaign", false},
		{"markdown", "markdown", "email", "campaign", false},
		{"plain", "plain", "email", "campaign", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store := trackingManager()
			m.cfg.DisableTracking = tc.disabled
			c := models.Campaign{UUID: tc.uuid, ContentType: tc.format, Messenger: tc.messenger, Body: `<a href="https://example.test/">CTA</a>`}
			msg := renderTrackingCampaign(t, m, &c, "subscriber")
			if len(store.urls) != 0 || strings.Contains(string(msg.Body()), "/px.png") || !strings.Contains(string(msg.Body()), `href="https://example.test/"`) {
				t.Fatalf("Tracking added out of scope: %s / %v", msg.Body(), store.urls)
			}
		})
	}
	// The same functions power explicit tags and automatic tracking.
	m, store := trackingManager()
	m.cfg.DisableTracking = true
	c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email", Body: `<a href="https://example.test/@TrackLink">CTA</a>{{ TrackView }}`}
	msg := renderTrackingCampaign(t, m, &c, "subscriber")
	if string(msg.Body()) != `<a href="https://example.test/">CTA</a>` || len(store.urls) != 0 {
		t.Fatalf("Privacy disabled explicit tracking: %s", msg.Body())
	}
}

func TestVisualTrackingRegistrationFailure(t *testing.T) {
	m, store := trackingManager()
	store.fail = true
	c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email", Body: `<a href="https://example.test/?a=1&amp;b=2">CTA</a>`}
	msg := renderTrackingCampaign(t, m, &c, "subscriber")
	if !strings.HasPrefix(string(msg.Body()), c.Body) || !strings.Contains(string(msg.Body()), "/px.png") {
		t.Fatalf("Link registration failure broke content: %s", msg.Body())
	}
}

func TestVisualTrackingDecodesAttributesOnce(t *testing.T) {
	m, store := trackingManager()
	c := models.Campaign{UUID: "campaign", ContentType: "visual", Messenger: "email",
		Body: `<a href="https://example.test/?a=1&amp;amp;b=2">CTA</a>`}
	renderTrackingCampaign(t, m, &c, "subscriber")
	if len(store.urls) != 1 || store.urls[0] != "https://example.test/?a=1&amp;b=2" {
		t.Fatalf("Literal entity in destination decoded twice: %v", store.urls)
	}
}

func TestVisualLinkEligibility(t *testing.T) {
	m, _ := trackingManager()
	m.cfg.RootURL += "/newsletter"
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://example.test/path?a=1&b=2#section", true},
		{"https://example.test/link/path", true},
		{"http://example.test/", true},
		{"https://lists.example.test/newsletter/product", true},
		{"https://lists.example.test/newsletter/link/one/two/three", false},
		{"https://lists.example.test/newsletter/subscription/one/two?manage=true", false},
		{"https://lists.example.test/newsletter/subscription/optin/one", false},
		{"https://lists.example.test/newsletter/campaign/one/two", false},
		{"mailto:team@example.test", false}, {"tel:+123", false}, {"#section", false},
		{"/relative", false}, {"//example.test/path", false}, {"javascript:alert(1)", false},
		{"https://example.test/%ZZ", false}, {"", false},
	} {
		if got := m.canTrackVisualLink(tc.url); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestRewriteTrackingHTMLPreservesMarkup(t *testing.T) {
	const body = `<!DOCTYPE html><BODY><A title='some href="decoy"' HREF = 'https://example.test/?a=1&amp;b=2' class="cta">한국어</A><area href=https://example.test/map><IMG SRC="https://example.test/image"><!-- href="https://example.test/comment" --><!--[if mso]><v:roundrect href="https://example.test/outlook" style="width:100px">Outlook</v:roundrect><![endif]--><script>var a = '<a href="https://example.test/script">';</script></BODY>`
	var links []string
	got := rewriteTrackingHTML([]byte(body), func(tag, attr, value string) string {
		if (tag == "a" || tag == "area" || tag == "v:roundrect") && attr == "href" {
			links = append(links, value)
			return "https://lists.example.test/link/tracked"
		}
		return value
	})
	want := strings.ReplaceAll(body, "https://example.test/?a=1&amp;b=2", "https://lists.example.test/link/tracked")
	want = strings.ReplaceAll(want, "https://example.test/map", "https://lists.example.test/link/tracked")
	want = strings.ReplaceAll(want, "https://example.test/outlook", "https://lists.example.test/link/tracked")
	if string(got) != want || strings.Join(links, "|") != "https://example.test/?a=1&b=2|https://example.test/map|https://example.test/outlook" {
		t.Fatalf("Markup changed outside tracking attributes: %s / %v", got, links)
	}
}
