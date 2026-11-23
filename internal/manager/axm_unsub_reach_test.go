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
		// The review's case: the footer address chosen by a subscriber attribute.
		{"address chosen by a subscriber attribute", body,
			`<a href="{{ or .Subscriber.Attribs.footer_url (UnsubscribeURL .) }}">x</a>`, "", nil, false},
		{"variable given an address chosen by a subscriber attribute", body,
			`{{ $u := or .Subscriber.Attribs.footer_url (UnsubscribeURL .) }}<a href="{{ $u }}">x</a>`, "", nil, false},
		{"subscriber fields printed beside a plain link", body, `<p>Hi {{ .Subscriber.Name }}</p>` + link, "", nil, true},
		// The review's case: a footer map whose url a with-block sets from a subscriber attribute.
		{"map changed in place under a subscriber condition", body,
			`{{ $f := dict "url" (UnsubscribeURL .) }}{{ with .Subscriber.Attribs.footer_url }}{{ $_ := set $f "url" . }}{{ end }}<a href="{{ $f.url }}">x</a>`,
			"", nil, false},
		{"a map changed in place anywhere", body, `{{ $f := dict "url" "x" }}{{ $_ := set $f "url" "y" }}` + link, "", nil, false},
		// The review's case: the body chooses its footer by the rendered subject.
		{"footer chosen by the rendered subject", body,
			`{{ if eq .Subject "Digest" }}<a href="https://example.org/archive">archive</a>{{ else }}` + link + `{{ end }}`, "", nil, false},
		{"rendered subject read through $", body, `{{ with $.Subject }}` + link + `{{ end }}`, "", nil, false},
		// The review's cases: the message in a variable.
		{"rendered subject read through a variable holding the message", body,
			`{{ $mail := . }}{{ $u := UnsubscribeURL . }}{{ if eq $mail.Subject "Digest" }}{{ $u = "https://example.org/archive" }}{{ end }}<a href="{{ $u }}">x</a>`, "", nil, false},
		{"footer address chosen by the rendered subject through a variable", body,
			`{{ $mail := . }}<a href="{{ if eq $mail.Subject "Digest" }}https://example.org/archive{{ else }}{{ UnsubscribeURL . }}{{ end }}">x</a>`, "", nil, false},
		{"printed value read through a variable holding the message", body,
			`{{ $mail := . }}<a href="{{ or $mail.Subscriber.Attribs.footer_url (UnsubscribeURL .) }}">x</a>`, "", nil, false},
		{"in the alternative body", body, `<p>Hi</p>`, `Leave: {{ UnsubscribeURL }}`, nil, true},
		{"none", body, `<p>Hi {{ .Subscriber.Name }}</p>`, "", nil, false},
	} {
		// A subject made per recipient, so that a body reading .Subject reads something that varies.
		c := models.Campaign{ContentType: models.CampaignContentTypeHTML, Messenger: "email", TemplateBody: tc.base, Body: tc.src, Attribs: tc.attribs,
			Subject: `{{ if .Subscriber.Attribs.digest }}Digest{{ else }}Full{{ end }}`}
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

// A subject the same for everyone: a body that chooses by it follows the branch it takes
// (LST-48 review: a fixed subject read by the body or headers was taken as varying).
func TestUnsubscribeFixedSubject(t *testing.T) {
	m, _ := trackingManager()
	m.tplFuncs = m.makeGnericFuncMap()
	sub := models.Subscriber{Email: "demo@listmonk.app", Name: "Demo", UUID: dummyUUID}
	src := `{{ if eq .Subject "Digest" }}<a href="https://example.org/archive">archive</a>{{ else }}<a href="{{ UnsubscribeURL }}">x</a>{{ end }}`
	for _, tc := range []struct {
		subject string
		want    bool
	}{
		{"Full", true},
		{"Digest", false},
		{`{{ "Full" }}`, true},
		{`{{ if .Subscriber.Attribs.digest }}Digest{{ else }}Full{{ end }}`, false},
		{`{{ if eq (Date "15") "09" }}Digest{{ else }}Full{{ end }}`, false},
		// LST-48 review: a campaign attribute read with with is the same for everyone; the
		// shorthand {{ MessageURL }} is the message's link, different for each.
		{`{{ with .Campaign.Attribs.edition }}{{ . }}{{ end }}`, true},
		{`{{ MessageURL }}`, false},
		{`{{ MessageURL . }}`, false},
		{`{{ $m := . }}{{ $m.Campaign.Attribs.edition }}`, true},
	} {
		c := models.Campaign{ContentType: models.CampaignContentTypeHTML, Messenger: "email", TemplateBody: `{{ template "content" . }}`, Body: src, Subject: tc.subject,
			Attribs: models.JSON{"edition": "Full"}}
		c.UUID = "11111111-2222-3333-4444-555555555555"
		if got, err := m.UnsubscribeOnEveryPath(&c, sub, "running"); err != nil || got != tc.want {
			t.Errorf("subject %q: got %v %v", tc.subject, got, err)
		}
	}
}

func TestReadsVarying(t *testing.T) {
	m, _ := trackingManager()
	m.tplFuncs = m.makeGnericFuncMap()
	c := &models.Campaign{Subject: `{{ if .Subscriber.Attribs.regional }}Regional{{ else }}Weekly{{ end }}`}
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"desk@crevasse.ai", false},
		{`{{ "desk@crevasse.ai" }}`, false},
		{`{{ if .Campaign.Attribs.x }}a{{ end }}`, false},
		{`{{ .Subscriber.Attribs.owner }}`, true},
		{`{{ if eq .Subject "Regional" }}regional@crevasse.ai{{ end }}`, true},
		{`{{ $m := . }}{{ if eq $m.Subject "R" }}r{{ end }}`, true},
		{`{{ with .Subscriber }}{{ .Email }}{{ end }}`, true},
		{`{{ if }}`, true},
	} {
		if got := m.ReadsVarying(c, tc.text); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.text, got, tc.want)
		}
	}
	// The same headers with a subject the same for everyone (LST-48 review): .Subject is fixed.
	for _, subject := range []string{"Regional edition", `{{ "Regional" }} edition`, `{{ .Campaign.Name }}`, `{{ Date "2006" }}`,
		`{{ with .Campaign.Attribs.edition }}{{ . }}{{ end }}`, `{{ range .Campaign.Tags }}{{ . }}{{ end }}`, `{{ $m := . }}{{ $m.Campaign.Name }}`} {
		c := &models.Campaign{Subject: subject, Attribs: models.JSON{"edition": "Regional edition"}}
		for _, text := range []string{`{{ if eq .Subject "Regional edition" }}regional@crevasse.ai{{ end }}`, `{{ $m := . }}{{ if eq $m.Subject "R" }}r{{ end }}`} {
			if m.ReadsVarying(c, text) {
				t.Errorf("subject %q, %q: varying", subject, text)
			}
		}
	}
	// A subject that passes the message on, or reads the subscriber through a variable.
	for _, subject := range []string{`{{ UnsubscribeURL . }}`, `{{ $s := .Subscriber }}{{ $s.Name }}`, `{{ $m := . }}{{ $m.Subscriber.Name }}`,
		`News {{ MessageURL }}`, `{{ UnsubscribeURL }}`, `{{ with . }}{{ MessageURL . }}{{ end }}`, `{{ $m := . }}{{ MessageURL $m }}`} {
		c := &models.Campaign{Subject: subject}
		if !m.ReadsVarying(c, `{{ if eq .Subject "R" }}r{{ end }}`) {
			t.Errorf("subject %q: not varying", subject)
		}
	}
}
