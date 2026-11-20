package manager

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"github.com/knadh/listmonk/models"
	htmlparser "golang.org/x/net/html"
)

func (m *Manager) trackingSubscriber(msg *CampaignMessage) string {
	if !m.cfg.IndividualTracking {
		return dummyUUID
	}
	return msg.Subscriber.UUID
}

func (m *Manager) trackMessageLink(url string, msg *CampaignMessage) string {
	if m.cfg.DisableTracking {
		return url
	}
	// Template arguments may contain HTML entities. Automatic tracking below
	// already receives decoded attributes and must not decode them twice.
	return m.trackLink(strings.ReplaceAll(url, "&amp;", "&"), msg.Campaign.UUID, m.trackingSubscriber(msg))
}

func (m *Manager) trackMessageView(msg *CampaignMessage) template.HTML {
	if m.cfg.DisableTracking {
		return ""
	}
	return template.HTML(fmt.Sprintf(`<img src="%s" alt="" />`,
		fmt.Sprintf(m.cfg.ViewTrackURL, msg.Campaign.UUID, m.trackingSubscriber(msg))))
}

// trackVisualMessage runs after personalization, before the business footer.
// Stored bodies/sources are untouched. Previews use a dummy campaign UUID and
// must not register links or generate hits against a real campaign.
func (m *Manager) trackVisualMessage(msg *CampaignMessage) {
	if m.cfg.DisableTracking || !tracksVisualAutomatically(msg.Campaign) ||
		msg.Campaign.UUID == "" || msg.Campaign.UUID == dummyUUID || msg.Subscriber.UUID == "" {
		return
	}

	pixelURL := fmt.Sprintf(m.cfg.ViewTrackURL, msg.Campaign.UUID, m.trackingSubscriber(msg))
	hasPixel := false
	msg.body = rewriteTrackingHTML(msg.body, func(tag, attr, value string) string {
		if tag == "img" && attr == "src" && value == pixelURL {
			hasPixel = true
		}
		if (tag == "a" || tag == "area" || tag == "v:roundrect") && attr == "href" && m.canTrackVisualLink(value) {
			return m.trackLink(browserURL(value), msg.Campaign.UUID, m.trackingSubscriber(msg))
		}
		return value
	})
	if !hasPixel {
		msg.body = insertHTMLFooter(msg.body, []byte(m.trackMessageView(msg)), "")
	}
}

// isEmailMessenger tells whether a messenger name is the e-mail messenger or one of
// the named SMTP servers ("email-...").
func isEmailMessenger(name string) bool {
	return name == "email" || strings.HasPrefix(name, "email-")
}

// tracksVisualAutomatically is the content half of the automatic tracking rule. The
// send path (trackVisualMessage) and the pre-send check (CheckTracking) both use it,
// so that the check cannot promise tracking that the send does not add.
func tracksVisualAutomatically(c *models.Campaign) bool {
	return c.ContentType == models.CampaignContentTypeVisual && isEmailMessenger(c.Messenger)
}

// browserURL is an href as a browser follows it: the URL parser of browsers drops
// leading and trailing spaces and control characters and every tab and newline.
// href=" https://example.test/ " is a working link (LST-44 review).
func browserURL(value string) string {
	value = strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
	return strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(value)
}

func (m *Manager) canTrackVisualLink(value string) bool {
	u, err := url.Parse(browserURL(value))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return !m.isServiceURL(u)
}

// isServiceURL tells whether an absolute URL is one of this instance's own service
// paths: existing tracking links, unsubscribe/manage/opt-in and browser views. Those
// stay direct. Only this instance's paths count, not external sites with similarly
// named paths. RootURL may include an installation prefix.
func (m *Manager) isServiceURL(u *url.URL) bool {
	root, err := url.Parse(m.cfg.RootURL)
	if err == nil && strings.EqualFold(u.Scheme, root.Scheme) && strings.EqualFold(u.Host, root.Host) {
		base := strings.TrimRight(root.Path, "/")
		for _, path := range []string{"/link/", "/subscription/", "/campaign/"} {
			if strings.HasPrefix(u.Path, base+path) {
				return true
			}
		}
	}
	return false
}

// Match whole attributes, including their quoted values, so a string such as
// title='some href="..."' cannot be mistaken for the actual href attribute.
var trackingAttribute = regexp.MustCompile(`([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?`)

// rewriteTrackingHTML changes only selected attribute values. Re-serializing
// an HTML DOM would alter email layout, entities and Outlook markup.
func rewriteTrackingHTML(body []byte, rewrite func(tag, attr, value string) string) []byte {
	var out bytes.Buffer
	z := htmlparser.NewTokenizer(bytes.NewReader(body))
	for {
		typ := z.Next()
		raw := z.Raw()
		if typ == htmlparser.ErrorToken {
			out.Write(raw)
			return out.Bytes()
		}
		if typ == htmlparser.CommentToken && bytes.HasPrefix(bytes.ToLower(raw), []byte("<!--[if ")) && bytes.HasSuffix(raw, []byte("-->")) {
			out.WriteString("<!--")
			out.Write(rewriteTrackingHTML(raw[4:len(raw)-3], rewrite))
			out.WriteString("-->")
			continue
		}
		if typ != htmlparser.StartTagToken && typ != htmlparser.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		// TagName normalizes the tokenizer's buffer in place.
		raw = bytes.Clone(raw)
		name, _ := z.TagName()
		tag := strings.ToLower(string(name))
		start := 1 + len(name)
		pos := 0
		for _, loc := range trackingAttribute.FindAllSubmatchIndex(raw[start:len(raw)-1], -1) {
			attr := strings.ToLower(string(raw[start+loc[2] : start+loc[3]]))
			for group := 4; group <= 8; group += 2 {
				if loc[group] < 0 {
					continue
				}
				lo, hi := start+loc[group], start+loc[group+1]
				value := html.UnescapeString(string(raw[lo:hi]))
				if next := rewrite(tag, attr, value); next != value {
					out.Write(raw[pos:lo])
					out.WriteString(html.EscapeString(next))
					pos = hi
				}
				break
			}
		}
		out.Write(raw[pos:])
	}
}
