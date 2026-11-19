package main

import (
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
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
	Subject   string    `json:"subject"`
	FromEmail string    `json:"from_email"`
	ReplyTo   string    `json:"reply_to"`
	SendAt    null.Time `json:"send_at"`
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
		// A resumed campaign with nothing left only warns, like one with nobody left.
		level := models.PreflightBlock
		if f.Mode == models.PreflightModeResume {
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
func sendEnvelope(subject, from, replyTo, messenger string, headers models.Headers, policy bool) (string, string, string) {
	email := messenger == "email" || strings.HasPrefix(messenger, "email-")
	if !email {
		return subject, from, ""
	}
	if !policy {
		// The reply address of a campaign is only used under the sender policy.
		replyTo = ""
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
	if v, ok := first("Reply-To"); ok && !(policy && replyTo != "") {
		replyTo = v
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

	camp, err := a.core.GetCampaignForPreview(id, 0)
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
	subject, from, replyTo := sendEnvelope(camp.Subject, from, camp.ReplyTo, camp.Messenger, camp.Headers, len(a.cfg.SenderDomains) > 0)

	facts := preflightFacts{
		Mode: mode, CampType: camp.Type, Messenger: camp.Messenger, ContentType: camp.ContentType,
		Total: aud.Total, Remaining: aud.Remaining, ListCount: len(lists),
		SendAt: camp.SendAt, Now: time.Now(), From: from, RecentExcluded: -1,
	}
	// Whether the e-mail that is rendered has the link to leave, as the recipient gets it.
	if has, err := a.manager.HasUnsubscribeLink(&camp, dummySubscriber); err == nil {
		facts.Unsubscribe, facts.UnsubscribeKnown = has, true
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
		Subject: subject, Messenger: camp.Messenger, Mode: mode,
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

	// The tracking of the saved campaign, as the tracking check reports it.
	if rep, err := a.manager.CheckTracking(&camp, dummySubscriber); err != nil {
		out.TrackingError = err.Error()
	} else {
		out.Tracking = &rep
	}

	return c.JSON(http.StatusOK, okResp{out})
}
