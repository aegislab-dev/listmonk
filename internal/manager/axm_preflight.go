package manager

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// HasUnsubscribeLink renders the campaign for one subscriber and tells whether the e-mail
// that comes out has the link to leave: the rendered text or alt body holds the
// unsubscribe URL, or an <a> of the HTML body points at it (the pre-send confirmation, LST-48). It reads what the recipient would
// get, not the template source: a link behind a condition that does not hold, or in a
// template that is not used, does not count. It registers no links and writes nothing.
//
// status is the state of the campaign when the sender reads it, see SendStatus.
//
// The business footer (LST-7) is not added: it carries no unsubscribe link.
func (m *Manager) HasUnsubscribeLink(c *models.Campaign, sub models.Subscriber, status string) (bool, error) {
	// Compile on a copy, with the tracking functions that do not touch the database.
	camp := *c
	camp.UUID = dummyUUID
	// A body may ask what state the campaign is in: render it with the one the sender will
	// read it in (see SendStatus).
	camp.Status = status
	funcs := m.TemplateFuncs(&camp)
	funcs["TrackLink"] = func(u string, _ *CampaignMessage) string { return u }
	funcs["TrackView"] = func(_ *CampaignMessage) template.HTML { return "" }
	if err := camp.CompileTemplate(funcs); err != nil {
		return false, err
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
		return false, err
	}
	if bytes.Contains(msg.altBody, []byte(msg.unsubURL)) {
		return true, nil
	}
	if camp.ContentType == models.CampaignContentTypePlain {
		return bytes.Contains(msg.body, []byte(msg.unsubURL)), nil
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
	return found, nil
}

// SendStatus is the status a campaign has when the sender reads it to render its mail. The
// sender reads a campaign in the state it was found in, before it marks it as running
// (next-campaigns): a campaign that was started with a send time is "scheduled" then, and
// one without is "running". A body that depends on the status renders differently.
func SendStatus(sendAt null.Time) string {
	if sendAt.Valid {
		return models.CampaignStatusScheduled
	}
	return models.CampaignStatusRunning
}
