package manager

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/knadh/listmonk/models"
)

// BusinessFooterLabels are resolved once using the instance language.
type BusinessFooterLabels struct {
	Representative string
	RegistrationNo string
	Phone          string
	Email          string
}

// BuildBusinessFooter renders plain settings as safe HTML and text once at startup.
func BuildBusinessFooter(info models.BusinessFooter, labels BusinessFooterLabels) ([]byte, []byte) {
	if !info.Enabled {
		return nil, nil
	}
	labeled := func(label, value string) string {
		if value == "" {
			return ""
		}
		return label + " " + value
	}
	join := func(items ...string) string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item != "" {
				out = append(out, item)
			}
		}
		return strings.Join(out, " · ")
	}
	lines := []string{
		join(info.CompanyName, labeled(labels.Representative, info.Representative), labeled(labels.RegistrationNo, info.RegistrationNo)),
		info.Address,
		join(labeled(labels.Phone, info.Phone), labeled(labels.Email, info.Email)),
	}
	var markup, text strings.Builder
	markup.WriteString(`<div class="axm-business-footer" style="text-align:center;font-size:12px;line-height:18px;color:#888;padding:0 30px 30px;">`)
	text.WriteString("--")
	for _, line := range lines {
		if line == "" {
			continue
		}
		markup.WriteString(`<p style="margin:0;">` + html.EscapeString(line) + `</p>`)
		text.WriteString("\n" + line)
	}
	if info.Extra != "" {
		extra := strings.ReplaceAll(strings.ReplaceAll(info.Extra, "\r\n", "\n"), "\r", "\n")
		markup.WriteString(`<p style="margin:6px 0 0;">` + strings.ReplaceAll(html.EscapeString(extra), "\n", "<br>") + `</p>`)
		text.WriteString("\n" + extra)
	}
	markup.WriteString("</div>")
	return []byte(markup.String()), []byte(text.String())
}

var businessFooterBodyEnd = regexp.MustCompile(`(?i)</body>`)

// insertHTMLFooter places the block after the existing footer, before the last body end.
func insertHTMLFooter(body, footer []byte, marker string) []byte {
	if len(footer) == 0 || (marker != "" && bytes.Contains(body, []byte(marker))) {
		return body
	}
	pos := len(body)
	if matches := businessFooterBodyEnd.FindAllIndex(body, -1); len(matches) > 0 {
		pos = matches[len(matches)-1][0]
	}
	out := make([]byte, 0, len(body)+len(footer))
	out = append(out, body[:pos]...)
	out = append(out, footer...)
	return append(out, body[pos:]...)
}

func appendTextFooter(body, footer []byte, marker string) []byte {
	if len(footer) == 0 || (marker != "" && bytes.Contains(body, []byte(marker))) {
		return body
	}
	out := make([]byte, 0, len(body)+2+len(footer))
	out = append(out, body...)
	out = append(out, '\n', '\n')
	return append(out, footer...)
}
