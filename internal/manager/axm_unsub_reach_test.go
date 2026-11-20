package manager

import (
	"testing"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// Whether every recipient gets the unsubscribe link, as far as the campaign decides: the parts
// that can differ between mails are taken out and the rest is rendered (LST-48 reviews).
func TestUnsubscribeOnEveryPath(t *testing.T) {
	m, _ := trackingManager()
	// The functions of a real manager (Date and the others), as New sets them.
	m.tplFuncs = m.makeGnericFuncMap()
	sub := models.Subscriber{Email: "demo@listmonk.app", Name: "Demo", UUID: dummyUUID, Attribs: models.JSON{"city": "Bengaluru"}}
	body := `{{ template "content" . }}`
	link := `<a href="{{ UnsubscribeURL }}">x</a>`
	for _, tc := range []struct {
		name      string
		base, src string
		alt       string
		attribs   models.JSON
		everyPath bool
	}{
		{"plain", body, link, "", nil, true},
		{"under a subscriber condition", body, `{{ if not .Subscriber.Attribs.compact }}` + link + `{{ end }}`, "", nil, false},
		{"in its else", body, `{{ if .Subscriber.Attribs.compact }}short{{ else }}` + link + `{{ end }}`, "", nil, false},
		{"under a campaign condition that holds", body, `{{ if eq .Campaign.Status "running" }}` + link + `{{ end }}`, "", nil, true},
		{"under a campaign condition that does not", body, `{{ if eq .Campaign.Status "draft" }}` + link + `{{ end }}`, "", nil, false},
		{"under a time condition", body, `{{ if eq (Date "15") "09" }}` + link + `{{ end }}`, "", nil, false},
		{"after the block", body, `{{- if .Subscriber.Name }}Hi{{ end -}}` + link, "", nil, true},
		{"in a range", body, `{{ range .Campaign.Tags }}` + link + `{{ end }}`, "", nil, false},
		{"in the template, plain", `<p>{{ template "content" . }}</p>` + link, `<p>Hi</p>`, "", nil, true},
		{"body included under a condition", `{{ if not .Subscriber.Attribs.compact }}{{ template "content" . }}{{ else }}<p>Short</p>{{ end }}`, link, "", nil, false},
		{"body in a named template called under a condition",
			`{{ define "full" }}{{ template "content" . }}{{ end }}{{ if not .Subscriber.Attribs.compact }}{{ template "full" . }}{{ else }}<p>Short</p>{{ end }}`, link, "", nil, false},
		{"named template called plainly", `{{ define "full" }}{{ template "content" . }}{{ end }}{{ template "full" . }}`, link, "", nil, true},
		{"named template with another dot", `{{ define "foot" }}` + link + `{{ end }}{{ template "content" . }}{{ template "foot" .Campaign }}`, `<p>Hi</p>`, "", nil, false},
		{"variable printed under a condition", body,
			`{{ $leave := UnsubscribeURL . }}{{ if not .Subscriber.Attribs.compact }}<a href="{{ $leave }}">x</a>{{ end }}`, "", nil, false},
		{"variable printed plainly", body, `{{ $leave := UnsubscribeURL . }}<a href="{{ $leave }}">x</a>`, "", nil, true},
		{"overwritten under a condition", body,
			`{{ $l := UnsubscribeURL . }}{{ if .Subscriber.Attribs.compact }}{{ $l = "" }}{{ end }}<a href="{{ $l }}">x</a>`, "", nil, false},
		{"printf around it, under a condition", body,
			`{{ if not .Subscriber.Attribs.compact }}<a href="{{ printf "%s" (UnsubscribeURL .) }}">x</a>{{ end }}`, "", nil, false},
		{"printf around it, plainly", body, `<a href="{{ printf "%s" (UnsubscribeURL .) }}">x</a>`, "", nil, true},
		{"same variable name in the template and the body",
			`{{ $url := "https://example.org/archive" }}{{ template "content" . }}<a href="{{ $url }}">archive</a>`,
			`{{ $url := UnsubscribeURL . }}{{ if not .Subscriber.Attribs.compact }}<a href="{{ $url }}">x</a>{{ end }}`, "", nil, false},
		// The review's case: a footer with the link, switched off by a campaign setting.
		{"footer switched off by a campaign setting",
			`{{ template "content" . }}{{ if .Campaign.Attribs.shared_footer }}` + link + `{{ end }}`,
			`<a href="https://example.org/archive">archive</a>{{ if not .Subscriber.Attribs.compact }}` + link + `{{ end }}`, "",
			models.JSON{"shared_footer": false}, false},
		{"footer switched on by a campaign setting",
			`{{ template "content" . }}{{ if .Campaign.Attribs.shared_footer }}` + link + `{{ end }}`,
			`<p>Hi</p>`, "", models.JSON{"shared_footer": true}, true},
		{"in the alternative body", body, `<p>Hi</p>`, `Leave: {{ UnsubscribeURL }}`, nil, true},
		{"none", body, `<p>Hi {{ .Subscriber.Name }}</p>`, "", nil, false},
	} {
		c := models.Campaign{ContentType: models.CampaignContentTypeHTML, Messenger: "email", TemplateBody: tc.base, Body: tc.src, Attribs: tc.attribs}
		c.UUID = "11111111-2222-3333-4444-555555555555"
		if tc.alt != "" {
			c.AltBody = null.StringFrom(tc.alt)
		}
		got, err := m.UnsubscribeOnEveryPath(&c, sub, "running")
		if err != nil || got != tc.everyPath {
			t.Errorf("%s: got %v %v", tc.name, got, err)
		}
	}
}
