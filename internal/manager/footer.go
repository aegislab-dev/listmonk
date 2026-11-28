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

// BusinessFooterItem is one entry of a footer line. Labeled entries ("대표 …",
// "사업자등록번호 …", "전화 …", "이메일 …") are short and are kept on one line
// (NoWrap) so that a narrow screen does not split a number in two.
type BusinessFooterItem struct {
	Text   string
	NoWrap bool
}

// BusinessFooterBlock is the business footer split into lines, shared by the
// campaign mail footer and the footer of the public pages (LST-76).
type BusinessFooterBlock struct {
	// Lines of items, shown with " · " between the items.
	Lines [][]BusinessFooterItem
	// Extra holds the lines of the free text, as entered.
	Extra []string
}

// NewBusinessFooterBlock splits the settings into lines. Empty values, their
// labels and empty lines are left out. It is empty when the footer is off.
func NewBusinessFooterBlock(info models.BusinessFooter, labels BusinessFooterLabels) BusinessFooterBlock {
	var out BusinessFooterBlock
	if !info.Enabled {
		return out
	}
	plain := func(value string) BusinessFooterItem { return BusinessFooterItem{Text: value} }
	labeled := func(label, value string) BusinessFooterItem {
		if value == "" {
			return BusinessFooterItem{}
		}
		return BusinessFooterItem{Text: label + " " + value, NoWrap: true}
	}
	for _, line := range [][]BusinessFooterItem{
		{plain(info.CompanyName), labeled(labels.Representative, info.Representative), labeled(labels.RegistrationNo, info.RegistrationNo)},
		{plain(info.Address)},
		{labeled(labels.Phone, info.Phone), labeled(labels.Email, info.Email)},
	} {
		items := make([]BusinessFooterItem, 0, len(line))
		for _, item := range line {
			if item.Text != "" {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			out.Lines = append(out.Lines, items)
		}
	}
	if info.Extra != "" {
		out.Extra = strings.Split(strings.ReplaceAll(strings.ReplaceAll(info.Extra, "\r\n", "\n"), "\r", "\n"), "\n")
	}
	return out
}

// Empty tells whether there is nothing to show.
func (b BusinessFooterBlock) Empty() bool {
	return len(b.Lines) == 0 && len(b.Extra) == 0
}

// BuildBusinessFooter renders plain settings as safe HTML and text once at startup.
func BuildBusinessFooter(info models.BusinessFooter, labels BusinessFooterLabels) ([]byte, []byte) {
	if !info.Enabled {
		return nil, nil
	}
	block := NewBusinessFooterBlock(info, labels)
	var markup, text strings.Builder
	markup.WriteString(`<div class="axm-business-footer" style="text-align:center;font-size:12px;line-height:18px;color:#888;padding:0 30px 30px;">`)
	text.WriteString("--")
	for _, line := range block.Lines {
		markup.WriteString(`<p style="margin:0;">`)
		texts := make([]string, 0, len(line))
		for i, item := range line {
			if i > 0 {
				markup.WriteString(" · ")
			}
			if item.NoWrap {
				markup.WriteString(`<span style="white-space:nowrap;">` + html.EscapeString(item.Text) + "</span>")
			} else {
				markup.WriteString(html.EscapeString(item.Text))
			}
			texts = append(texts, item.Text)
		}
		markup.WriteString("</p>")
		text.WriteString("\n" + strings.Join(texts, " · "))
	}
	if len(block.Extra) > 0 {
		escaped := make([]string, len(block.Extra))
		for i, l := range block.Extra {
			escaped[i] = html.EscapeString(l)
		}
		markup.WriteString(`<p style="margin:6px 0 0;">` + strings.Join(escaped, "<br>") + `</p>`)
		text.WriteString("\n" + strings.Join(block.Extra, "\n"))
	}
	markup.WriteString("</div>")
	return []byte(markup.String()), []byte(text.String())
}

var businessFooterBodyEnd = regexp.MustCompile(`(?i)</body[\t\n\f\r ]*>`)

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
