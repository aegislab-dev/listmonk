package manager

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// HasUnsubscribeLink renders the campaign for one subscriber and tells whether the e-mail
// that comes out has the link to leave: the rendered text or alt body holds the
// unsubscribe URL, or an <a> of the HTML body points at it (the pre-send confirmation, LST-48). It reads what the recipient would
// get, not the template source: a link behind a condition that does not hold, or in a
// template that is not used, does not count. It registers no links and writes nothing.
//
// status is a state the campaign has when the sender reads it, see SendStatuses.
//
// The business footer (LST-7) is not added: it carries no unsubscribe link.
func (m *Manager) HasUnsubscribeLink(c *models.Campaign, sub models.Subscriber, status string) (bool, error) {
	msg, err := m.checkRender(c, sub, status)
	if err != nil {
		return false, err
	}
	return hasUnsubscribeLink(msg), nil
}

// Envelope is the subject line and the custom headers of a campaign's mail as the sender
// renders them. Unrendered: a subject or header with a template expression could not be
// rendered for the subscriber given, and is as written.
type Envelope struct {
	Subject    string
	Headers    models.Headers
	Unrendered bool
}

// RenderedEnvelope renders the subject line and the custom headers of the campaign's mail
// to sub as the sender does when the campaign has the given status: a subject or a header
// value with a template expression ({{ .Campaign.Name }}, {{ .Campaign.UUID }} and the like)
// goes out as its result, not as written (LST-48 review). Each is rendered on its own, apart
// from the body: a body that cannot be rendered for sub does not hide a subject that can.
//
// at, when given, is the clock the templates read (Date, now) instead of the real one: rendering
// at two moments tells what changes with the time of each mail.
func (m *Manager) RenderedEnvelope(c *models.Campaign, sub models.Subscriber, status string, at ...time.Time) (Envelope, error) {
	var clock *time.Time
	if len(at) > 0 {
		clock = &at[0]
	}
	msg, err := m.prepareCheck(c, sub, status, clock)
	if err != nil {
		return Envelope{}, err
	}
	out := Envelope{Subject: msg.Campaign.Subject, Headers: msg.Campaign.Headers}
	var b bytes.Buffer
	if msg.Campaign.SubjectTpl != nil {
		if err := msg.Campaign.SubjectTpl.ExecuteTemplate(&b, models.ContentTpl, msg); err == nil {
			out.Subject = b.String()
			// As render: the headers then read the rendered subject ({{ .Subject }}).
			msg.subject = out.Subject
		} else {
			out.Unrendered = true
		}
	}
	if msg.Campaign.HeaderTpls != nil {
		out.Headers = make(models.Headers, len(msg.Campaign.Headers))
		for i, set := range msg.Campaign.Headers {
			out.Headers[i] = make(map[string]string, len(set))
			for hdr, val := range set {
				out.Headers[i][hdr] = val
				if tpl := msg.Campaign.HeaderTpls[i][hdr]; tpl != nil {
					b.Reset()
					if err := tpl.ExecuteTemplate(&b, models.ContentTpl, msg); err == nil {
						out.Headers[i][hdr] = b.String()
					} else {
						out.Unrendered = true
					}
				}
			}
		}
	}
	return out, nil
}

// checkRender compiles a copy of the campaign, with tracking functions that touch nothing,
// and renders its mail to sub in the given status. It registers no links and writes nothing.
func (m *Manager) checkRender(c *models.Campaign, sub models.Subscriber, status string) (*CampaignMessage, error) {
	msg, err := m.prepareCheck(c, sub, status, nil)
	if err != nil {
		return nil, err
	}
	if err := msg.render(); err != nil {
		return nil, err
	}
	return msg, nil
}

// prepareCheck compiles a copy of the campaign for a check, with tracking functions that
// touch nothing, and makes its (not yet rendered) message to sub in the given status.
//
// clock, when not nil, is the time the Date and now template functions give.
func (m *Manager) prepareCheck(c *models.Campaign, sub models.Subscriber, status string, clock *time.Time) (*CampaignMessage, error) {
	// Compile on a copy, with the tracking functions that do not touch the database.
	// The campaign's own UUID: a subject, header or body can print it. The tracking functions
	// below do not use it to register anything.
	camp := *c
	// A body may ask what state the campaign is in: render it with the one given (see
	// SendStatuses).
	camp.Status = status
	// The attachments as a real send fills them before rendering (newPipe: inline images,
	// then the media), as LST-44's CheckTracking: templates can read them.
	camp.Attachments = append([]models.Attachment(nil), c.Attachments...)
	if err := m.LoadInlineImages(&camp); err != nil {
		return nil, err
	}
	funcs := m.TemplateFuncs(&camp)
	funcs["TrackLink"] = func(u string, _ *CampaignMessage) string { return u }
	funcs["TrackView"] = func(_ *CampaignMessage) template.HTML { return "" }
	if clock != nil {
		at := *clock
		funcs["Date"] = func(layout string) string {
			if layout == "" {
				layout = time.ANSIC
			}
			return at.Format(layout)
		}
		if _, ok := funcs["now"]; ok {
			funcs["now"] = func() time.Time { return at }
		}
	}
	if err := camp.CompileTemplate(funcs); err != nil {
		return nil, err
	}
	if err := m.attachMedia(&camp); err != nil {
		return nil, err
	}

	sub.Attribs = models.PlainNumbers(sub.Attribs)
	return &CampaignMessage{
		Campaign:   &camp,
		Subscriber: sub,
		subject:    camp.Subject,
		from:       camp.FromEmail,
		to:         sub.Email,
		unsubURL:   fmt.Sprintf(m.cfg.UnsubURL, camp.UUID, sub.UUID),
	}, nil
}

// hasUnsubscribeLink tells whether a rendered message has the link to leave.
func hasUnsubscribeLink(msg *CampaignMessage) bool {
	camp := msg.Campaign
	if bytes.Contains(msg.altBody, []byte(msg.unsubURL)) {
		return true
	}
	if camp.ContentType == models.CampaignContentTypePlain {
		return bytes.Contains(msg.body, []byte(msg.unsubURL))
	}
	// An HTML body has the link when an <a> points at the address. The address in an
	// attribute that cannot be clicked, or in the text, is not a link to leave.
	found := false
	rewriteTrackingHTML(msg.body, func(tag, attr, value string) string {
		if tag == "a" && attr == "href" && strings.TrimSpace(value) == msg.unsubURL {
			found = true
		}
		return value
	})
	return found
}

// SendStatuses are the statuses a campaign may have when the sender reads it to render its
// mail, for any of the people it is sent to. The sender reads a campaign in the state it was
// found in, before it marks it as running (next-campaigns): a campaign started without a
// send time is "running"; one with a send time is "scheduled" when it is first picked up.
// That pick-up marks it "running" in the database, so when the sender reads it again for the
// rest of the list (after a restart of the app, which continues it, LST-20) it renders the
// remaining mail as "running" (LST-48 review: 9 of 12 mails lost the link that only the
// "scheduled" state had). A body that depends on the status must be right in every one.
func SendStatuses(sendAt null.Time) []string {
	if sendAt.Valid {
		return []string{models.CampaignStatusScheduled, models.CampaignStatusRunning}
	}
	return []string{models.CampaignStatusRunning}
}
