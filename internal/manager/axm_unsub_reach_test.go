package manager

import "testing"

// Where the unsubscribe link is reached: on the template's parse trees, through the calls of
// named templates and the body (LST-48 reviews).
func TestUnsubscribeReach(t *testing.T) {
	m, _ := trackingManager()
	funcs := m.TemplateFuncs(nil)
	if _, ok := funcs["Date"]; !ok {
		// The real manager has it (makeGnericFuncMap); this test one does not.
		funcs["Date"] = func(string) string { return "" }
	}
	body := `{{ template "content" . }}`
	link := `<a href="{{ UnsubscribeURL }}">x</a>`
	for _, tc := range []struct {
		name               string
		base, src, alt     string
		unconditional, any bool
	}{
		{"plain", body, link, "", true, true},
		{"under a subscriber condition", body, `{{ if not .Subscriber.Attribs.compact }}` + link + `{{ end }}`, "", false, true},
		{"in its else", body, `{{ if .Subscriber.Attribs.compact }}short{{ else }}` + link + `{{ end }}`, "", false, true},
		{"under a campaign condition", body, `{{ if eq .Campaign.Status "running" }}` + link + `{{ end }}`, "", true, true},
		{"under a time condition", body, `{{ if eq (Date "15") "09" }}` + link + `{{ end }}`, "", false, true},
		{"after the block", body, `{{- if .Subscriber.Name }}Hi{{ end -}}` + link, "", true, true},
		{"in a range", body, `{{ range .Campaign.Tags }}` + link + `{{ end }}`, "", false, true},
		{"in the template, plain", `<p>{{ template "content" . }}</p>` + link, `<p>Hi</p>`, "", true, true},
		{"body included under a condition", `{{ if not .Subscriber.Attribs.compact }}{{ template "content" . }}{{ else }}<p>Short</p>{{ end }}`, link, "", false, true},
		// The review's case: the body inside a named template called only for the full edition.
		{"body in a named template called under a condition",
			`{{ define "full" }}{{ template "content" . }}{{ end }}{{ if not .Subscriber.Attribs.compact }}{{ template "full" . }}{{ else }}<p>Short</p>{{ end }}`, link, "", false, true},
		{"named template called plainly", `{{ define "full" }}{{ template "content" . }}{{ end }}{{ template "full" . }}`, link, "", true, true},
		{"link in a named template called under a condition", `{{ define "foot" }}` + link + `{{ end }}{{ template "content" . }}{{ with .Subscriber.Attribs.x }}{{ template "foot" . }}{{ end }}`, `<p>Hi</p>`, "", false, true},
		{"named template with another dot", `{{ define "foot" }}` + link + `{{ end }}{{ template "content" . }}{{ template "foot" .Campaign }}`, `<p>Hi</p>`, "", false, true},
		{"body not included", `<p>No body</p>`, link, "", false, false},
		{"defined but never called", `{{ define "foot" }}` + link + `{{ end }}{{ template "content" . }}`, `<p>Hi</p>`, "", false, false},
		{"in the alternative body", body, `<p>Hi</p>`, `Leave: {{ UnsubscribeURL }}`, true, true},
		{"cannot be parsed", body, `{{ if }}`, "", false, false},
		{"none", body, `<p>Hi {{ .Subscriber.Name }}</p>`, "", false, false},
	} {
		u, f := unsubscribeReach(tc.base, tc.src, tc.alt, funcs)
		if u != tc.unconditional || f != tc.any {
			t.Errorf("%s: got %v %v", tc.name, u, f)
		}
	}
}
