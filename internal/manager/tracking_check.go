package manager

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"

	"github.com/knadh/listmonk/models"
)

// AXM (LST-44): the pre-send tracking check. It answers "will this campaign record
// views and clicks?" for a draft, before anything is sent.
//
// The check renders the campaign the way a send does (same template compilation and
// execution), but with probe functions in place of TrackLink and TrackView: nothing is
// registered in the links table, no real campaign UUID is used and no public tracking
// URL is produced. What the send adds after rendering (automatic tracking of visual
// e-mails, the business footer) is not rendered; the automatic part is decided by the
// same rule the send uses (tracksVisualAutomatically, canTrackVisualLink).

const (
	// .invalid is reserved (RFC 2606): a probe URL can never be a real destination.
	trackingProbeLink  = "https://axm-tracking-probe.invalid/l?u="
	trackingProbePixel = "https://axm-tracking-probe.invalid/v"

	// maxUntrackedURLs caps the list of untracked links in a report.
	maxUntrackedURLs = 20
)

// Results of a tracking check.
const (
	TrackingStateOK   = "ok"
	TrackingStateWarn = "warn"
	TrackingStateOff  = "off"

	TrackingViewManual      = "manual"      // {{ TrackView }} is in the rendered body
	TrackingViewAuto        = "auto"        // added at send time (visual e-mail)
	TrackingViewMissing     = "missing"     // an HTML e-mail without a pixel
	TrackingViewUnsupported = "unsupported" // plain text cannot carry a pixel
	TrackingViewNA          = "n/a"         // not an e-mail messenger
	TrackingViewOff         = "off"         // tracking is disabled globally
)

// TrackingLinks counts the links of a rendered campaign body by how their clicks are
// recorded. A destination is counted once per kind, however often it appears.
type TrackingLinks struct {
	Total         int      `json:"total"`
	Manual        int      `json:"manual"`
	Auto          int      `json:"auto"`
	Untracked     int      `json:"untracked"`
	UntrackedURLs []string `json:"untracked_urls"`
}

// TrackingReport is the result of CheckTracking.
type TrackingReport struct {
	State       string        `json:"state"`
	Disabled    bool          `json:"disabled"`
	Individual  bool          `json:"individual"`
	Applicable  bool          `json:"applicable"`
	Auto        bool          `json:"auto"`
	ContentType string        `json:"content_type"`
	Messenger   string        `json:"messenger"`
	View        string        `json:"view"`
	Links       TrackingLinks `json:"links"`
}

// TrackingCheckError is a campaign that cannot be compiled or rendered, and so cannot
// be checked (or sent).
type TrackingCheckError struct {
	Compile bool
	Err     error
}

func (e *TrackingCheckError) Error() string { return e.Err.Error() }
func (e *TrackingCheckError) Unwrap() error { return e.Err }

var plainTextURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// CheckTracking renders the campaign for one subscriber and reports whether views and
// clicks would be recorded. The campaign that is passed in is not changed.
func (m *Manager) CheckTracking(c *models.Campaign, sub models.Subscriber) (TrackingReport, error) {
	rep := TrackingReport{
		State:       TrackingStateOK,
		Disabled:    m.cfg.DisableTracking,
		Individual:  m.cfg.IndividualTracking,
		Applicable:  isEmailMessenger(c.Messenger),
		Auto:        tracksVisualAutomatically(c),
		ContentType: c.ContentType,
		Messenger:   c.Messenger,
		Links:       TrackingLinks{UntrackedURLs: []string{}},
	}
	if rep.Disabled {
		rep.State, rep.View, rep.Auto = TrackingStateOff, TrackingViewOff, false
		return rep, nil
	}

	// Compile on a copy: the compiled templates hold the probe functions and must
	// never reach a send.
	camp := *c
	camp.UUID = dummyUUID
	funcs := m.TemplateFuncs(&camp)
	funcs["TrackLink"] = func(u string, _ *CampaignMessage) string {
		// Same decoding as trackMessageLink. QueryEscape writes a space as "+", which
		// the HTML escaper would then rewrite; keep the probe free of such characters.
		u = strings.ReplaceAll(u, "&amp;", "&")
		return trackingProbeLink + strings.ReplaceAll(url.QueryEscape(u), "+", "%20")
	}
	funcs["TrackView"] = func(_ *CampaignMessage) template.HTML {
		return template.HTML(`<img src="` + trackingProbePixel + `" alt="" />`)
	}
	if err := camp.CompileTemplate(funcs); err != nil {
		return rep, &TrackingCheckError{Compile: true, Err: err}
	}

	sub.Attribs = models.PlainNumbers(sub.Attribs)
	msg := CampaignMessage{
		Campaign:   &camp,
		Subscriber: sub,
		subject:    camp.Subject,
		from:       camp.FromEmail,
		to:         sub.Email,
		unsubURL:   fmt.Sprintf(m.cfg.UnsubURL, camp.UUID, sub.UUID),
	}
	if err := msg.render(); err != nil {
		return rep, &TrackingCheckError{Err: err}
	}

	var (
		plain    = c.ContentType == models.CampaignContentTypePlain
		hasPixel = false
		seen     = map[string]bool{}
	)
	link := func(value string) {
		kind, dest := m.trackingLinkKind(value, rep.Auto)
		if kind == "" || seen[kind+"\x00"+dest] {
			return
		}
		seen[kind+"\x00"+dest] = true
		rep.Links.Total++
		switch kind {
		case TrackingViewManual:
			rep.Links.Manual++
		case TrackingViewAuto:
			rep.Links.Auto++
		default:
			rep.Links.Untracked++
			if len(rep.Links.UntrackedURLs) < maxUntrackedURLs {
				rep.Links.UntrackedURLs = append(rep.Links.UntrackedURLs, dest)
			}
		}
	}

	if plain {
		// The plain body is rendered by html/template, so its text carries entities.
		for _, u := range plainTextURL.FindAllString(html.UnescapeString(string(msg.body)), -1) {
			link(strings.TrimRight(u, ".,;:!?)]}"))
		}
	} else {
		rewriteTrackingHTML(msg.body, func(tag, attr, value string) string {
			if tag == "img" && attr == "src" && value == trackingProbePixel {
				hasPixel = true
			}
			if (tag == "a" || tag == "area" || tag == "v:roundrect") && attr == "href" {
				link(value)
			}
			return value
		})
	}

	switch {
	case !rep.Applicable:
		rep.View = TrackingViewNA
	case plain:
		rep.View = TrackingViewUnsupported
	case hasPixel:
		rep.View = TrackingViewManual
	case rep.Auto:
		rep.View = TrackingViewAuto
	default:
		rep.View = TrackingViewMissing
	}
	if rep.View == TrackingViewMissing || rep.Links.Untracked > 0 {
		rep.State = TrackingStateWarn
	}
	return rep, nil
}

// trackingLinkKind tells how the clicks of one rendered link are recorded: "manual"
// (TrackLink was applied), "auto" (the send wraps it) or "untracked". The second value
// is the destination. Links that are never tracked by design (service links of this
// instance, mailto:, tel:, anchors, relative URLs) give an empty kind and are not counted.
func (m *Manager) trackingLinkKind(value string, auto bool) (string, string) {
	if rest, ok := strings.CutPrefix(value, trackingProbeLink); ok {
		if dest, err := url.QueryUnescape(rest); err == nil {
			return TrackingViewManual, dest
		}
		return TrackingViewManual, value
	}
	if !m.canTrackVisualLink(value) {
		return "", ""
	}
	if auto {
		return TrackingViewAuto, value
	}
	return "untracked", value
}
