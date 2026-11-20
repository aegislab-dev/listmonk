package main

import (
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
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
	// Varies: which parts of the envelope differ between the mails (subject, from, reply_to,
	// cc, bcc). The window does not compare those between readings; everything else it does.
	Varies []string `json:"varies"`
	// CopyLists: the addresses of the Cc and of the Bcc header each as it is, not merged, for
	// the window to compare one without the other.
	CopyLists map[string]string `json:"copy_lists"`
	// Copies: the addresses that the Cc and Bcc headers add to every mail. Each of them gets
	// one copy per recipient.
	Copies []mailCopy `json:"copies"`
	// SenderPerSubscriber: the From or Reply-To header takes its value from each subscriber;
	// the From and Reply-To shown are the example subscriber's.
	SenderPerSubscriber bool      `json:"sender_per_subscriber"`
	FromEmail           string    `json:"from_email"`
	ReplyTo             string    `json:"reply_to"`
	SendAt              null.Time `json:"send_at"`
	// SendTZ is the time zone the send time was chosen in (LST-50, attribs.axm_send_tz),
	// empty when none is stored (the screen then uses Korean time, as the picker does).
	SendTZ string `json:"send_tz"`
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
	// it could not be rendered, so nothing is said. UnsubscribeSampled: how many subscribers it
	// was rendered for. UnsubscribePerSubscriber: the link is printed only under a condition on
	// the subscriber, so the renderings cannot show that every recipient gets it.
	Unsubscribe, UnsubscribeKnown bool
	UnsubscribeSampled            int
	UnsubscribePerSubscriber      bool

	// A saved rule that excludes recent recipients (LST-34) is only recorded, not applied.
	// RecentExcluded is how many it would exclude; -1: could not be counted.
	// HeadersVary: the SMTP servers that may send the mail set different From, Subject or
	// Reply-To headers, so what the recipient sees depends on the server.
	HeadersVary bool
	// Copies: the addresses that Cc and Bcc headers add to every mail. CopiesPerSubscriber:
	// the header takes them from each subscriber, so they cannot be listed.
	Copies              []string
	CopiesPerSubscriber bool
	// FromRefused: the status change will refuse the draft's From (LST-45: a placeholder, or
	// not on the team's From list, or a From header while there is a list), with the reason.
	FromRefused string

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

// stockFrom reports whether the From of a campaign is the stock or an unfilled value, or
// has blanks around it. The pre-send check also counts LST-45's placeholderFrom.
func stockFrom(from string) bool {
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

	// The status change refuses such a draft (LST-45): the window does not offer to send it.
	if f.FromRefused != "" {
		add("from.refused", models.PreflightBlock, map[string]any{"message": f.FromRefused})
	}

	if stockFrom(f.From) || placeholderFrom(f.From) {
		add("from.placeholder", models.PreflightWarn, map[string]any{"from": f.From})
	} else {
		add("from.placeholder", models.PreflightOK, nil)
	}

	if (f.Messenger == "email" || strings.HasPrefix(f.Messenger, "email-")) && f.CampType != models.CampaignTypeOptin {
		if f.UnsubscribeKnown {
			switch {
			case f.Unsubscribe && f.UnsubscribePerSubscriber:
				// Never a pass on a sample: it says what it saw and that it cannot vouch for all.
				add("unsubscribe.perSubscriber", models.PreflightWarn, map[string]any{"n": f.UnsubscribeSampled})
			case f.Unsubscribe:
				add("unsubscribe.missing", models.PreflightOK, nil)
			default:
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

	// Cc or Bcc addresses that come from each subscriber: every mail may also go to addresses
	// of that person's, as many as the header gives for each (none, one or more): no count can
	// be said (LST-48 review: an upper bound of one per mail was too low).
	if f.CopiesPerSubscriber {
		add("mail.copiesPerSubscriber", models.PreflightWarn, nil)
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
	// As UpdateCampaignStatus (LST-45): without account senders, a draft starts only with a
	// From that the From list (or, with no list, the placeholder rule) allows, and one that
	// matches the list in another spelling (another display name) is sent in the list's
	// spelling. Show that one (LST-48 review: the window showed "North Desk", the mail went
	// out as the list's "South Desk").
	fromRefused := ""
	if len(a.cfg.SenderDomains) == 0 && camp.Status == models.CampaignStatusDraft {
		if len(a.cfg.FromAddresses) > 0 && hasFromHeader(camp.Headers) {
			fromRefused = a.i18n.T("campaigns.fromHeaderNotAllowed")
		} else if ok, err := a.checkFromAllowed(strings.TrimSpace(camp.FromEmail), a.cfg.FromAddresses); err != nil {
			fromRefused = err.Error()
		} else if ok != "" {
			from = ok
		}
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
	// Headers taken from each subscriber: their example values are not the ones every mail
	// gets. Cc and Bcc addresses of that kind are not listed (the fixed ones are).
	perSub := perSubscriberHeaders(a, sendCamp, statuses[0], env, server)
	copies := envelopeCopies(camp.Messenger, server, env.Headers, perSub)
	perSubscriber := perSub["Cc"] || perSub["Bcc"]
	senderPerSubscriber := perSub["From"] || perSub["Reply-To"]
	subjectNote := ""
	varies := map[string]bool{}
	if !env.Unrendered {
		varies = envelopeVaries(a, sendCamp, statuses, campFrom, server, policy)
	}
	switch {
	case env.Unrendered:
		subjectNote = "unrendered"
	case len(varies) > 0:
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
	facts.CopiesPerSubscriber = perSubscriber
	facts.FromRefused = fromRefused
	// Whether the e-mail that is rendered has the link to leave, as every recipient gets it:
	// in every status, for the example subscriber, for one with no attributes at all, and for
	// a few of the real recipients (a link that depends on the subscriber's fields).
	subs := append([]models.Subscriber{dummySubscriber, bareSubscriber}, a.core.GetPreflightSample(ctx, id, 10)...)
	var renders []string
	for _, st := range statuses {
		for i := range subs {
			renders = append(renders, st+"#"+strconv.Itoa(i))
		}
	}
	facts.Unsubscribe, facts.UnsubscribeKnown = unsubscribeInAll(renders, func(key string) (bool, error) {
		st, n, _ := strings.Cut(key, "#")
		i, _ := strconv.Atoi(n)
		return a.manager.HasUnsubscribeLink(&sendCamp, subs[i], st)
	})
	// The renderings cover a few people. Where the link is printed only under a condition on
	// the subscriber, they cannot vouch for the others (LST-48 review).
	facts.UnsubscribeSampled = len(subs)
	if unconditional, found := unsubscribeUnconditional(sendCamp.TemplateBody, sendCamp.Body, sendCamp.AltBody.String); found && !unconditional {
		facts.UnsubscribePerSubscriber = true
	}
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
		Subject: subject, SubjectRendered: subjectNote == "rendered", SubjectNote: subjectNote, Varies: sortedKeys(varies), Copies: copies, CopyLists: map[string]string{"cc": kindAddresses(camp.Messenger, server, env.Headers, "Cc"), "bcc": kindAddresses(camp.Messenger, server, env.Headers, "Bcc")}, SenderPerSubscriber: senderPerSubscriber, Messenger: camp.Messenger, Mode: mode,
		FromEmail: from, ReplyTo: replyTo, SendAt: camp.SendAt,
		Lists: lists, Recipients: aud.Total, Overlap: aud.Multi, Excluded: aud.Excluded, Remaining: aud.Remaining,
		Sent: camp.Sent, ToSend: camp.ToSend, AsOf: aud.AsOf,
		Checks: preflightChecks(facts),
	}
	if tz, ok := camp.Attribs["axm_send_tz"].(string); ok {
		out.SendTZ = tz
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

// unsubscribeInAll tells whether the mail has the link to leave in every rendering given
// (statuses × subscribers). A rendering that fails is left out (the sender does not send that
// mail either). known is false when nothing can be said: nothing rendered.
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
	return !missing, missing || rendered > 0
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

// chosenHeader is the value of header name that an e-mail goes out with: the campaign's
// first set that has it, over the SMTP server's (internal/messenger/email).
func chosenHeader(name string, server map[string]string, headers models.Headers) (string, bool) {
	for _, set := range headers {
		for k, v := range set {
			if textproto.CanonicalMIMEHeaderKey(k) == name {
				return v, true
			}
		}
	}
	v, ok := server[name]
	return v, ok
}

// envelopeCopies is what the Cc and Bcc headers add to every e-mail of a campaign: the
// campaign's (rendered) headers, the first set that has the header, over the SMTP server's,
// split on commas as the e-mail messenger does (internal/messenger/email). Each address once.
func envelopeCopies(messenger string, server map[string]string, headers models.Headers, skip ...map[string]bool) []mailCopy {
	out := []mailCopy{}
	if messenger != "email" && !strings.HasPrefix(messenger, "email-") {
		return out
	}
	seen := map[string]bool{}
	for _, name := range []string{"Cc", "Bcc"} {
		if len(skip) > 0 && skip[0][name] {
			continue
		}
		val, ok := chosenHeader(name, server, headers)
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

// envelopeVaries tells which parts of the mail's envelope (subject, from, reply_to, cc, bcc)
// differ between the mails of a send. It does not read the templates for it: it renders the
// envelope at two moments — the Date and now functions and the campaign's times (updated_at
// changes as the sent counts are written, started_at is set by the first pick-up, as a restart
// reads them) moved by an amount that changes every field of a clock — and in every status the
// sender may read the campaign in, and sees what changes. So a header of the SMTP server, which
// is never rendered, never varies however it is written, and a header that prints a subject
// with the time ({{ .Subject }}) does (LST-48 reviews).
func envelopeVaries(a *App, camp models.Campaign, statuses []string, from string, server map[string]string, policy bool) map[string]bool {
	parts := func(e manager.Envelope) map[string]string {
		s, f, r := sendEnvelope(e.Subject, from, camp.ReplyTo, camp.Messenger, server, e.Headers, policy)
		return map[string]string{"subject": s, "from": f, "reply_to": r,
			"cc": kindAddresses(camp.Messenger, server, e.Headers, "Cc"), "bcc": kindAddresses(camp.Messenger, server, e.Headers, "Bcc")}
	}
	out := map[string]bool{}
	t0 := time.Now()
	base, err := a.manager.RenderedEnvelope(&camp, dummySubscriber, statuses[0], t0)
	if err != nil {
		return out
	}
	want := parts(base)
	later := camp
	moved := t0.Add(37*time.Hour + 13*time.Minute + 17*time.Second + 250*time.Millisecond)
	later.UpdatedAt = null.TimeFrom(moved)
	later.StartedAt = null.TimeFrom(moved)
	for _, v := range []struct {
		c  models.Campaign
		at time.Time
	}{{camp, t0}, {later, moved}} {
		for _, st := range statuses {
			e, err := a.manager.RenderedEnvelope(&v.c, dummySubscriber, st, v.at)
			if err != nil {
				for k := range want {
					out[k] = true
				}
				return out
			}
			for k, val := range parts(e) {
				if val != want[k] {
					out[k] = true
				}
			}
		}
	}
	return out
}

// kindAddresses is the addresses one header (Cc or Bcc) adds to every e-mail, in order, each
// as written and lowercased: the list of that header alone, so that one header that varies
// does not make the other look so (LST-48 review: an address common to a varying Cc and a fixed
// Bcc was taken out of the Bcc).
func kindAddresses(messenger string, server map[string]string, headers models.Headers, name string) string {
	if messenger != "email" && !strings.HasPrefix(messenger, "email-") {
		return ""
	}
	val, ok := chosenHeader(name, server, headers)
	if !ok {
		return ""
	}
	var out []string
	for _, part := range strings.Split(val, ",") {
		addr := strings.TrimSpace(part)
		if a, err := mail.ParseAddress(addr); err == nil {
			addr = a.Address
		}
		if addr != "" {
			out = append(out, strings.ToLower(addr))
		}
	}
	return strings.Join(out, ",")
}

// bareSubscriber is an example subscriber with nothing but an address: a mail whose parts
// depend on attributes the example subscriber has is rendered without them too.
var bareSubscriber = models.Subscriber{
	Email: "bare@example.invalid", UUID: "22222222-2222-2222-2222-222222222222", Attribs: models.JSON{},
}

// otherSubscriber is a second example subscriber, unlike dummySubscriber in every field, to
// find what a template takes from the subscriber.
var otherSubscriber = models.Subscriber{
	Email: "other@example.invalid", Name: "Other Example", UUID: "11111111-1111-1111-1111-111111111111",
	Attribs: models.JSON{"city": "Seoul"},
}

// perSubscriberHeaders tells which of the Cc, Bcc, From and Reply-To headers that apply take
// their value from the subscriber, so that it differs from mail to mail and the example
// subscriber's value is not the one every recipient gets (LST-48 review: a Bcc of each
// person's owner was not counted; From and Reply-To of each person's owner were shown as the
// example's). A header does when its value names .Subscriber or renders otherwise for another
// example subscriber. Each header is judged on its own: a fixed Cc stays listed beside a Bcc
// taken from the subscriber.
func perSubscriberHeaders(a *App, camp models.Campaign, status string, first manager.Envelope, server map[string]string) map[string]bool {
	out := map[string]bool{}
	other, err := a.manager.RenderedEnvelope(&camp, otherSubscriber, status)
	for _, name := range []string{"Cc", "Bcc", "From", "Reply-To"} {
		raw, ok := chosenHeader(name, server, camp.Headers)
		if !ok {
			continue
		}
		if strings.Contains(raw, "{{") && strings.Contains(raw, "Subscriber") {
			out[name] = true
			continue
		}
		if err == nil {
			v1, _ := chosenHeader(name, server, first.Headers)
			v2, _ := chosenHeader(name, server, other.Headers)
			if v1 != v2 {
				out[name] = true
			}
		}
	}
	return out
}

// sortedKeys is the keys of a set, sorted, never nil.
func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var reTplAction = regexp.MustCompile(`(?s)\{\{-?\s*(.*?)\s*-?\}\}`)

// unsubscribeUnconditional reads the template sources of a mail (the template, the body, the
// alternative body) and tells whether one place that prints UnsubscribeURL is not inside an
// if, with or range that reads the subscriber (.Subscriber, or a $variable, which may hold it).
// found is false when no source prints it. Rendering for a few subscribers cannot show that
// every recipient gets the link (LST-48 review: the link was only in the "full" edition, and the
// people sent the "compact" one were not in the sample); this reads where the link is.
func unsubscribeUnconditional(sources ...string) (unconditional, found bool) {
	for _, src := range sources {
		var stack []string
		for _, m := range reTplAction.FindAllStringSubmatch(src, -1) {
			a := strings.TrimSpace(m[1])
			word := a
			if i := strings.IndexAny(a, " \t\n("); i >= 0 {
				word = a[:i]
			}
			switch {
			case strings.HasPrefix(a, "/*"):
			case word == "if" || word == "with" || word == "range":
				stack = append(stack, a)
			case word == "define" || word == "block":
				stack = append(stack, "")
			case word == "else":
				// An else branch depends on the condition above it and on its own.
				if len(stack) > 0 {
					stack[len(stack)-1] += " " + a
				}
			case word == "end":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case strings.Contains(a, "UnsubscribeURL"):
				found = true
				dep := false
				for _, cond := range stack {
					if strings.Contains(cond, "Subscriber") || strings.Contains(cond, "$") {
						dep = true
					}
				}
				if !dep {
					unconditional = true
				}
			}
		}
	}
	return unconditional, found
}
