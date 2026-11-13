package manager

import (
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// A subscriber as the database gives it: attribute numbers are float64.
func numbersSubscriber(t *testing.T, attribs string) models.Subscriber {
	t.Helper()
	sub := models.Subscriber{UUID: "subscriber", Email: "a@example.invalid", Name: "Alice", Attribs: models.JSON{}}
	if err := sub.Attribs.Scan([]byte(attribs)); err != nil {
		t.Fatal(err)
	}
	return sub
}

func numbersManager() *Manager {
	return New(Config{
		RootURL:      "https://lists.example.test",
		LinkTrackURL: "https://lists.example.test/link/%s/%s/%s",
		ViewTrackURL: "https://lists.example.test/campaign/%s/%s/px.png",
		UnsubURL:     "https://lists.example.test/subscription/%s/%s",
		MessageURL:   "https://lists.example.test/campaign/%s/%s",
		OptinURL:     "https://lists.example.test/subscription/optin/%s?%s",
	}, &trackingStore{}, nil, log.New(io.Discard, "", 0))
}

// LST-22: {{ .Subscriber.Attribs.follower }} printed 1.234567e+06 for a million or more.
func TestCampaignMessagePrintsAttributeNumbersAsDigits(t *testing.T) {
	m := numbersManager()
	for _, contentType := range []string{models.CampaignContentTypeHTML, models.CampaignContentTypeRichtext,
		models.CampaignContentTypeVisual, models.CampaignContentTypeMarkdown, models.CampaignContentTypePlain} {
		t.Run(contentType, func(t *testing.T) {
			c := models.Campaign{
				UUID: "campaign", ContentType: contentType, Messenger: "postback",
				Subject: "{{ .Subscriber.Attribs.follower }} followers",
				Body:    `{{ .Subscriber.Attribs.follower }} / {{ .Subscriber.Attribs.small }} / {{ .Subscriber.Attribs.text }} / {{ .Subscriber.Attribs.stats.posts }} / {{ index .Subscriber.Attribs "팔로워" }} / {{ .Subscriber.Attribs.missing | default "none" }}`,
				AltBody: null.StringFrom("{{ .Subscriber.Attribs.follower }} alt"),
				Headers: models.Headers{{"X-Followers": "{{ .Subscriber.Attribs.follower }}"}},
			}
			if err := c.CompileTemplate(m.TemplateFuncs(&c)); err != nil {
				t.Fatal(err)
			}
			sub := numbersSubscriber(t, `{"follower": 1234567, "small": 14900, "text": "1,234,567", "stats": {"posts": 2500000}, "팔로워": 21474836480}`)
			msg, err := m.NewCampaignMessage(&c, sub)
			if err != nil {
				t.Fatal(err)
			}

			body := string(msg.Body())
			if contentType == models.CampaignContentTypeMarkdown {
				body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "<p>"), "</p>")
			}
			if want := "1234567 / 14900 / 1,234,567 / 2500000 / 21474836480 / none"; body != want {
				t.Errorf("body = %q, want %q", body, want)
			}
			if msg.Subject() != "1234567 followers" {
				t.Errorf("subject = %q", msg.Subject())
			}
			if contentType != models.CampaignContentTypePlain && string(msg.AltBody()) != "1234567 alt" {
				t.Errorf("alt body = %q", msg.AltBody())
			}
			if got := msg.headers[0]["X-Followers"]; got != "1234567" {
				t.Errorf("header = %q", got)
			}

			// The caller's subscriber keeps its float64, and the message's subscriber is
			// written as the same JSON (the postback messenger sends it).
			if _, ok := sub.Attribs["follower"].(float64); !ok {
				t.Fatalf("caller's attribs changed: %T", sub.Attribs["follower"])
			}
			want, _ := json.Marshal(sub.Attribs)
			if got, _ := json.Marshal(msg.Subscriber.Attribs); string(got) != string(want) {
				t.Fatalf("attribs JSON differs:\n%s\n%s", want, got)
			}
		})
	}
}

// Templates that already work around the float (sprig math, conversions, float
// comparisons, printf) give what they gave before.
func TestCampaignMessageNumberFunctionsUnchanged(t *testing.T) {
	m := numbersManager()
	for _, tc := range []struct{ expr, want string }{
		{`{{ int .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ int64 .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ .Subscriber.Attribs.follower | int }}`, "1234567"},
		{`{{ printf "%.0f" .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ printf "%d" (int .Subscriber.Attribs.follower) }}`, "1234567"},
		{`{{ add .Subscriber.Attribs.follower 1 }}`, "1234568"},
		{`{{ mul .Subscriber.Attribs.follower 2 }}`, "2469134"},
		{`{{ div .Subscriber.Attribs.follower 10000 }}`, "123"},
		{`{{ mod .Subscriber.Attribs.follower 10 }}`, "7"},
		{`{{ max .Subscriber.Attribs.follower 5 }}`, "1234567"},
		{`{{ divf .Subscriber.Attribs.follower 2.0 }}`, "617283.5"},
		{`{{ if gt .Subscriber.Attribs.follower 1000000.0 }}big{{ else }}small{{ end }}`, "big"},
		{`{{ if gt (int .Subscriber.Attribs.follower) 1000000 }}big{{ else }}small{{ end }}`, "big"},
		{`{{ if ge .Subscriber.Attribs.small 1000000.0 }}big{{ else }}small{{ end }}`, "small"},
		{`{{ if eq .Subscriber.Attribs.follower 1234567.0 }}same{{ end }}`, "same"},
		{`{{ if .Subscriber.Attribs.zero }}set{{ else }}unset{{ end }}`, "unset"},
		{`{{ .Subscriber.Attribs.zero | default "none" }}`, "none"},
		{`{{ .Subscriber.Attribs.follower | default "none" }}`, "1234567"},
		{`{{ kindOf .Subscriber.Attribs.follower }}`, "float64"},
		{`{{ toJson .Subscriber.Attribs.follower }}`, "1234567"},
		// These printed the exponent as well.
		{`{{ toString .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ printf "%v" .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ cat .Subscriber.Attribs.follower "followers" }}`, "1234567 followers"},
		{`{{ atoi (toString .Subscriber.Attribs.follower) }}`, "1234567"},
	} {
		c := models.Campaign{UUID: "campaign", ContentType: models.CampaignContentTypePlain, Messenger: "postback", Body: tc.expr}
		if err := c.CompileTemplate(m.TemplateFuncs(&c)); err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		msg, err := m.NewCampaignMessage(&c, numbersSubscriber(t, `{"follower": 1234567, "small": 14900, "zero": 0}`))
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got := string(msg.Body()); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}
