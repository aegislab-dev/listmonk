package main

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/internal/messenger/email"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"gopkg.in/volatiletech/null.v6"
)

// preflightResponse is the answer of GET /api/campaigns/:id/preflight (LST-48).
type preflightResponse struct {
	// UpdatedAt is the campaign's own value, to be sent back unchanged with the status
	// change: the server then refuses to start a campaign that was edited after this read.
	UpdatedAt null.Time `json:"updated_at"`
	Status    string    `json:"status"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Messenger string    `json:"messenger"`
	Mode      string    `json:"mode"`

	// What the recipients will really see: the stored values, replaced by what the custom
	// headers of an e-mail campaign set, which the mail goes out with (see sendEnvelope).
	Subject string `json:"subject"`
	// SubjectRendered: the subject field has a template expression and Subject is its result
	// for the example subscriber.
	SubjectRendered bool `json:"subject_rendered"`
	// SubjectNote: "rendered" (shown as rendered for the example subscriber), "unrendered"
	// (it could not be rendered and is shown as written), "varies" (it differs between the
	// mails of the send: with the status or the campaign's times), or "".
	SubjectNote string `json:"subject_note"`
	// Copies: the addresses that the Cc and Bcc headers add to every mail. Each of them gets
	// one copy per recipient.
	Copies    []mailCopy `json:"copies"`
	FromEmail string     `json:"from_email"`
	ReplyTo   string     `json:"reply_to"`
	SendAt    null.Time  `json:"send_at"`
	// PausedAt is when a paused campaign was paused (the last pause in the audit log, else
	// the campaign's last change).
	PausedAt null.Time `json:"paused_at"`

	Lists      []models.PreflightList `json:"lists"`
	Recipients int                    `json:"recipients"`
	// Overlap: people on more than one list; Excluded: people on the lists who are left out.
	Overlap  int `json:"overlap"`
	Excluded int `json:"excluded"`
	// Resume: the people still to be sent, the number sent so far and the total counted at the start.
	Remaining int       `json:"remaining"`
	Sent      int       `json:"sent"`
	ToSend    int       `json:"to_send"`
	AsOf      time.Time `json:"as_of"`
	// Now is the server's clock when the answer leaves: the window keeps time by it, not by a
	// browser clock that may be behind (LST-48 review).
	Now time.Time `json:"now"`

	Checks []models.PreflightCheck `json:"checks"`
	// Tracking is the tracking check of the saved campaign (LST-44); null with a
	// TrackingError when the body cannot be rendered.
	Tracking      *manager.TrackingReport `json:"tracking"`
	TrackingError string                  `json:"tracking_error,omitempty"`
}

// preflightFacts is everything the checks are decided from.
type preflightFacts struct {
	Mode        string
	CampType    string
	Messenger   string
	ContentType string

	Total, Remaining int
	// Lists of the campaign, and how many of them still exist.
	ListCount, LiveLists int

	SendAt null.Time
	Now    time.Time

	// The address the campaign will be sent from, as stored.
	From string

	// Unsubscribe: whether the rendered e-mail has the link to leave. UnsubscribeKnown false:
	// it could not be rendered, so nothing is said.
	Unsubscribe, UnsubscribeKnown bool

	// A saved rule that excludes recent recipients (LST-34) is only recorded, not applied.
	// RecentExcluded is how many it would exclude; -1: could not be counted.
	// HeadersVary: the SMTP servers that may send the mail set different From, Subject or
	// Reply-To headers, so what the recipient sees depends on the server.
	HeadersVary bool
	// Copies: the addresses that Cc and Bcc headers add to every mail.
	Copies []string

	RecentEnabled  bool
	RecentDays     int
	RecentExcluded int
}

// Hosts that stand for "not set up yet": the example from the stock install and the
// reserved example domains.
var placeholderHosts = map[string]bool{
	"listmonk.yoursite.com": true, "yoursite.com": true, "example.com": true, "example.org": true,
	"example.net": true, "example.test": true, "example.invalid": true, "localhost": true,
}

var rePlaceholderLocal = regexp.MustCompile(`(?i)^(change-?me|your-?(name|email|address)|you|name|email)(@|$)`)

// placeholderFrom reports whether the From of a campaign is a stock or unfilled value, or
// has blanks around it.
func placeholderFrom(from string) bool {
	if from != strings.TrimSpace(from) {
		return true
	}
	addr := from
	if a, err := mail.ParseAddress(from); err == nil {
		addr = a.Address
	}
	addr = strings.TrimSpace(strings.ToLower(addr))
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return true
	}
	return rePlaceholderLocal.MatchString(addr) || placeholderHosts[addr[i+1:]]
}

// preflightChecks decides the lines of "checks before sending", in the order the screen
// shows them. The tracking lines come from the tracking check and are not here. Only
// "block" stops the confirmation, and only for a regular campaign that would reach nobody.
func preflightChecks(f preflightFacts) []models.PreflightCheck {
	var out []models.PreflightCheck
	add := func(key, level string, params map[string]any) {
		out = append(out, models.PreflightCheck{Key: key, Level: level, Params: params})
	}

	switch {
	case f.LiveLists == 0:
		// A resumed campaign with nothing left only warns, like one with nobody left, and so
		// does an opt-in campaign (approved design v1, decision 1 and A5: only a regular
		// campaign that reaches nobody is stopped).
		level := models.PreflightBlock
		if f.Mode == models.PreflightModeResume || f.CampType == models.CampaignTypeOptin {
			level = models.PreflightWarn
		}
		add("lists.none", level, nil)
	default:
		count := f.Total
		if f.Mode == models.PreflightModeResume {
			count = f.Remaining
		}
		if count == 0 {
			// Opt-in campaigns and resumed ones: the number is an estimate or follows other
			// rules, so it only warns.
			level := models.PreflightBlock
			if f.CampType == models.CampaignTypeOptin || f.Mode == models.PreflightModeResume {
				level = models.PreflightWarn
			}
			add("recipients.zero", level, nil)
		}
	}

	// A campaign with a send time is scheduled by the status change, whether it is a draft or
	// a paused one that is resumed: a time that has passed means it goes out right away.
	if (f.Mode == models.PreflightModeSchedule || f.Mode == models.PreflightModeResume) && f.SendAt.Valid && !f.SendAt.Time.After(f.Now) {
		add("schedule.past", models.PreflightWarn, nil)
	}

	if placeholderFrom(f.From) {
		add("from.placeholder", models.PreflightWarn, map[string]any{"from": f.From})
	} else {
		add("from.placeholder", models.PreflightOK, nil)
	}

	if (f.Messenger == "email" || strings.HasPrefix(f.Messenger, "email-")) && f.CampType != models.CampaignTypeOptin {
		if f.UnsubscribeKnown {
			if f.Unsubscribe {
				add("unsubscribe.missing", models.PreflightOK, nil)
			} else {
				add("unsubscribe.missing", models.PreflightWarn, nil)
			}
		}
	}

	// Every mail also goes to the Cc and Bcc addresses: they get one copy per recipient, and
	// the count of people in the title does not hold them (LST-48 review).
	if len(f.Copies) > 0 {
		count := f.Total
		if f.Mode == models.PreflightModeResume {
			count = f.Remaining
		}
		add("mail.copies", models.PreflightWarn, map[string]any{
			"addresses": strings.Join(f.Copies, ", "), "n": len(f.Copies), "mails": count * len(f.Copies),
		})
	}

	if f.HeadersVary {
		add("mail.headers", models.PreflightWarn, nil)
	}

	if f.RecentEnabled && f.CampType == models.CampaignTypeRegular {
		p := map[string]any{"days": f.RecentDays}
		if f.RecentExcluded >= 0 {
			p["excluded"] = f.RecentExcluded
		}
		add("recent.notApplied", models.PreflightWarn, p)
	}
	return out
}

// sendEnvelope is what a recipient of the campaign sees as the subject, the sender and the
// reply address. An e-mail goes out with the campaign's custom headers set over the values
// of the campaign (internal/messenger/email: a header named From, Subject or Reply-To
// replaces the field; of several header sets the first that has it counts), and under the
// sender policy the campaign's reply address is set after them. Other messengers do not
// use the headers.
func sendEnvelope(subject, from, replyTo, messenger string, server map[string]string, headers models.Headers, policy bool) (string, string, string) {
	email := messenger == "email" || strings.HasPrefix(messenger, "email-")
	if !email {
		return subject, from, ""
	}
	// The reply address of a campaign is only used under the sender policy.
	campReply := replyTo
	replyTo = ""
	// The SMTP server sets its headers first and the campaign's replace them.
	if v, ok := server["Subject"]; ok {
		subject = v
	}
	if v, ok := server["From"]; ok {
		from = v
	}
	if v, ok := server["Reply-To"]; ok {
		replyTo = v
	}
	first := func(name string) (string, bool) {
		for _, set := range headers {
			for k, v := range set {
				if textproto.CanonicalMIMEHeaderKey(k) == name {
					return v, true
				}
			}
		}
		return "", false
	}
	if v, ok := first("Subject"); ok {
		subject = v
	}
	if v, ok := first("From"); ok {
		from = v
	}
	if v, ok := first("Reply-To"); ok {
		replyTo = v
	}
	// Under the sender policy the campaign's reply address is set after all of them.
	if policy && campReply != "" {
		replyTo = campReply
	}
	return subject, from, replyTo
}

// preflightMode is what the status change will do with a campaign in this state. The
// status query turns "running" into "scheduled" when the campaign has a send time, so the
// request alone does not say.
func preflightMode(status string, sendAt null.Time) string {
	switch {
	case status == models.CampaignStatusPaused:
		return models.PreflightModeResume
	case sendAt.Valid:
		return models.PreflightModeSchedule
	default:
		return models.PreflightModeStart
	}
}

// GetCampaignPreflight returns what the pre-send confirmation shows (LST-48): the people
// the campaign would reach, its lists, the address it goes out from and the checks. It
// reads and writes nothing else.
func (a *App) GetCampaignPreflight(c echo.Context) error {
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}

	// The campaign as the send reads it (next-campaigns, LST-44's axm-campaign-for-send-check):
	// the default template when it has none, its media, no lists. What is rendered below is
	// then what goes out.
	camp, err := a.core.GetCampaignForSendCheck(id, 0)
	if err != nil {
		return err
	}
	if camp.Status != models.CampaignStatusDraft && camp.Status != models.CampaignStatusPaused {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.onlyPausedDraft"))
	}
	mode := preflightMode(camp.Status, camp.SendAt)

	ctx := c.Request().Context()
	afterID := 0
	if mode == models.PreflightModeResume {
		afterID = camp.LastSubscriberID
	}
	lists, aud, err := a.core.GetPreflightAudience(ctx, id, afterID, mode == models.PreflightModeResume)
	if err != nil {
		return err
	}

	// A draft started under the sender policy is sent from the starting user's address.
	from := camp.FromEmail
	if len(a.cfg.SenderDomains) > 0 && camp.Status == models.CampaignStatusDraft {
		from = resolveFrom(auth.GetUser(c), a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
	}

	// What the recipients will see, with the custom headers applied.
	// The SMTP servers of the messenger set headers too, before the campaign's own.
	var server map[string]string
	headersVary := false
	for _, m := range a.messengers {
		if em, ok := m.(*email.Emailer); ok && m.Name() == camp.Messenger {
			server, headersVary = em.ServerHeaders(from, "From", "Subject", "Reply-To", "Cc", "Bcc")
		}
	}
	// The sender address the campaign itself has when it is sent, which templates can read.
	campFrom := from

	// The campaign as the sender reads it: with the sender set at the start of a draft, and
	// in each status it may be read in (a scheduled one is read again as "running" after a
	// restart). The mail has to be right in all of them.
	sendCamp := camp
	sendCamp.FromEmail = campFrom
	// The status change sets updated_at; templates can read it (.Campaign.UpdatedAt).
	sendCamp.UpdatedAt = null.TimeFrom(time.Now())
	statuses := manager.SendStatuses(camp.SendAt)

	// The subject and the custom headers go out rendered when they hold a template expression:
	// show them as the first mail of the send has them, for the example subscriber. Each is
	// rendered on its own (a body that the example subscriber cannot fill does not hide them);
	// one that cannot be rendered is shown as written and the window says so. One that uses the
	// subscriber's fields differs from person to person; one that differs with the status or
	// the campaign's times (the sender reads it again after a restart, and the sent counts
	// move updated_at) is said to vary.
	policy := len(a.cfg.SenderDomains) > 0
	asWritten, _, _ := sendEnvelope(camp.Subject, from, camp.ReplyTo, camp.Messenger, server, camp.Headers, policy)
	env := manager.Envelope{Subject: camp.Subject, Headers: camp.Headers, Unrendered: hasTemplate(camp.Subject, camp.Headers)}
	if e, err := a.manager.RenderedEnvelope(&sendCamp, dummySubscriber, statuses[0]); err == nil {
		env = e
	}
	subject, from, replyTo := sendEnvelope(env.Subject, from, camp.ReplyTo, camp.Messenger, server, env.Headers, policy)
	copies := envelopeCopies(camp.Messenger, server, env.Headers)
	subjectNote := ""
	switch {
	case env.Unrendered:
		subjectNote = "unrendered"
	case envelopeVaries(a, sendCamp, statuses, env, campFrom, server, policy):
		subjectNote = "varies"
	case subject != asWritten:
		subjectNote = "rendered"
	}

	facts := preflightFacts{
		Mode: mode, CampType: camp.Type, Messenger: camp.Messenger, ContentType: camp.ContentType,
		Total: aud.Total, Remaining: aud.Remaining, ListCount: len(lists),
		SendAt: camp.SendAt, Now: time.Now(), From: from, RecentExcluded: -1, HeadersVary: headersVary,
	}
	for _, c := range copies {
		facts.Copies = append(facts.Copies, c.Address)
	}
	// Whether the e-mail that is rendered has the link to leave, as every recipient gets it.
	facts.Unsubscribe, facts.UnsubscribeKnown = unsubscribeInAll(statuses, func(status string) (bool, error) {
		return a.manager.HasUnsubscribeLink(&sendCamp, dummySubscriber, status)
	})
	var listIDs []int
	for _, l := range lists {
		if !l.Deleted {
			facts.LiveLists++
			listIDs = append(listIDs, l.ID.Int)
		}
	}

	// A saved "exclude recent recipients" rule is not applied to the send yet: say so, with
	// the number it would exclude.
	if camp.Type == models.CampaignTypeRegular {
		if rule, err := a.core.GetRecentRule(id); err == nil && rule.Enabled {
			facts.RecentEnabled, facts.RecentDays = true, rule.Days
			if len(listIDs) > 0 {
				if counts, err := a.core.RecentExcludeCounts(ctx, listIDs, rule, id); err == nil {
					facts.RecentExcluded = counts.Excluded
				}
			}
		}
	}

	out := preflightResponse{
		UpdatedAt: camp.UpdatedAt, Status: camp.Status, Type: camp.Type, Name: camp.Name,
		Subject: subject, SubjectRendered: subjectNote == "rendered", SubjectNote: subjectNote, Copies: copies, Messenger: camp.Messenger, Mode: mode,
		FromEmail: from, ReplyTo: replyTo, SendAt: camp.SendAt,
		Lists: lists, Recipients: aud.Total, Overlap: aud.Multi, Excluded: aud.Excluded, Remaining: aud.Remaining,
		Sent: camp.Sent, ToSend: camp.ToSend, AsOf: aud.AsOf,
		Checks: preflightChecks(facts),
	}
	if out.Lists == nil {
		out.Lists = []models.PreflightList{}
	}

	if mode == models.PreflightModeResume {
		out.PausedAt = a.core.GetCampaignPausedAt(id, camp.UpdatedAt)
	}

	// The tracking of the saved campaign, as the tracking check reports it for the send (the
	// status and sender of LST-44's trackingSendState), in all the statuses it is read in.
	rep, err := trackingInAll(statuses, func(status string) (manager.TrackingReport, error) {
		c := sendCamp
		c.Status = status
		return a.manager.CheckTracking(&c, dummySubscriber)
	})
	if err != nil {
		out.TrackingError = err.Error()
	} else {
		out.Tracking = &rep
	}

	out.Now = time.Now()
	return c.JSON(http.StatusOK, okResp{out})
}

// unsubscribeInAll tells whether the mail has the link to leave in every status the sender
// may render it in. known is false when nothing can be said: no status rendered without the
// link, and one could not be rendered at all.
func unsubscribeInAll(statuses []string, has func(status string) (bool, error)) (ok, known bool) {
	rendered, missing := 0, false
	for _, st := range statuses {
		h, err := has(st)
		if err != nil {
			continue
		}
		rendered++
		if !h {
			missing = true
		}
	}
	return !missing, missing || (rendered == len(statuses) && rendered > 0)
}

// trackingInAll checks the tracking in each status and puts the reports together: what is
// missing in any one of them is missing for the people who get that mail (LST-48 review: a
// pixel and an untracked link only while "scheduled" lost the link warning when the report
// of "running" was shown instead). Views are missing when one report says so; the links are
// those of all reports, each distinct one counted once, from the full lists (not the capped
// samples of untracked addresses). A status that cannot be rendered is an error, as the send
// of it would be.
func trackingInAll(statuses []string, check func(status string) (manager.TrackingReport, error)) (manager.TrackingReport, error) {
	var (
		out   manager.TrackingReport
		links []manager.TrackedLink
		seen  = map[manager.TrackedLink]bool{}
	)
	for i, st := range statuses {
		r, err := check(st)
		if err != nil {
			return manager.TrackingReport{}, err
		}
		if i == 0 {
			out = r
		} else if r.View == manager.TrackingViewMissing {
			out.View = r.View
		}
		for _, l := range r.Links.All {
			if !seen[l] {
				seen[l] = true
				links = append(links, l)
			}
		}
	}
	if len(statuses) > 1 {
		lk := manager.TrackingLinks{UntrackedURLs: []string{}, All: links}
		for _, l := range links {
			lk.Total++
			switch l.Kind {
			case manager.TrackingViewManual:
				lk.Manual++
			case manager.TrackingViewAuto:
				lk.Auto++
			default:
				lk.Untracked++
				if len(lk.UntrackedURLs) < manager.MaxUntrackedURLs {
					lk.UntrackedURLs = append(lk.UntrackedURLs, l.Dest)
				}
			}
		}
		out.Links = lk
	}
	if out.State != manager.TrackingStateOff && (out.View == manager.TrackingViewMissing || out.Links.Untracked > 0) {
		out.State = manager.TrackingStateWarn
	}
	return out, nil
}

// mailCopy is an address that a Cc or Bcc header adds to every mail of a campaign.
type mailCopy struct {
	Kind    string `json:"kind"` // cc | bcc
	Address string `json:"address"`
}

// hasTemplate tells whether a subject or a header value holds a template expression.
func hasTemplate(subject string, headers models.Headers) bool {
	if strings.Contains(subject, "{{") {
		return true
	}
	for _, set := range headers {
		for _, v := range set {
			if strings.Contains(v, "{{") {
				return true
			}
		}
	}
	return false
}

// envelopeCopies is what the Cc and Bcc headers add to every e-mail of a campaign: the
// campaign's (rendered) headers, the first set that has the header, over the SMTP server's,
// split on commas as the e-mail messenger does (internal/messenger/email). Each address once.
func envelopeCopies(messenger string, server map[string]string, headers models.Headers) []mailCopy {
	out := []mailCopy{}
	if messenger != "email" && !strings.HasPrefix(messenger, "email-") {
		return out
	}
	seen := map[string]bool{}
	for _, name := range []string{"Cc", "Bcc"} {
		val, ok := server[name]
		for _, set := range headers {
			found := false
			for k, v := range set {
				if textproto.CanonicalMIMEHeaderKey(k) == name {
					val, ok, found = v, true, true
					break
				}
			}
			if found {
				break
			}
		}
		if !ok {
			continue
		}
		for _, part := range strings.Split(val, ",") {
			addr := strings.TrimSpace(part)
			if a, err := mail.ParseAddress(addr); err == nil {
				addr = a.Address
			}
			key := strings.ToLower(addr)
			if addr == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, mailCopy{Kind: strings.ToLower(name), Address: addr})
		}
	}
	return out
}

// envelopeVaries tells whether the subject, sender, reply address or copies of the mail
// differ between the mails of a send: rendered in every status the sender may read the
// campaign in, and with the campaign's times moved on (updated_at changes as the sent counts
// are written, started_at is set by the first pick-up), as a restart reads them.
func envelopeVaries(a *App, camp models.Campaign, statuses []string, first manager.Envelope, from string, server map[string]string, policy bool) bool {
	key := func(e manager.Envelope) string {
		s, f, r := sendEnvelope(e.Subject, from, camp.ReplyTo, camp.Messenger, server, e.Headers, policy)
		return fmt.Sprintf("%s\x00%s\x00%s\x00%v", s, f, r, envelopeCopies(camp.Messenger, server, e.Headers))
	}
	want := key(first)
	later := camp
	moved := time.Now().Add(36 * time.Hour)
	later.UpdatedAt = null.TimeFrom(moved)
	later.StartedAt = null.TimeFrom(moved)
	for _, c := range []models.Campaign{camp, later} {
		for _, st := range statuses {
			e, err := a.manager.RenderedEnvelope(&c, dummySubscriber, st)
			if err != nil || key(e) != want {
				return true
			}
		}
	}
	return false
}
