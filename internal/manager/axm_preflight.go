package manager

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/knadh/listmonk/models"
)

// HasUnsubscribeLink renders the campaign for one subscriber and tells whether the e-mail
// that comes out has the link to leave: the rendered body or alt body holds the
// unsubscribe URL (the pre-send confirmation, LST-48). It reads what the recipient would
// get, not the template source: a link behind a condition that does not hold, or in a
// template that is not used, does not count. It registers no links and writes nothing.
//
// The business footer (LST-7) is not added: it carries no unsubscribe link.
func (m *Manager) HasUnsubscribeLink(c *models.Campaign, sub models.Subscriber) (bool, error) {
	// Compile on a copy, with the tracking functions that do not touch the database.
	camp := *c
	camp.UUID = dummyUUID
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
	url := []byte(msg.unsubURL)
	return bytes.Contains(msg.body, url) || bytes.Contains(msg.altBody, url), nil
}
