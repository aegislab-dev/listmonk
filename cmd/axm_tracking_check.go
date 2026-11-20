package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"gopkg.in/volatiletech/null.v6"
)

// trackingCheckReq carries the unsaved editor state. A field that is left out keeps
// the value stored on the campaign.
type trackingCheckReq struct {
	ContentType *string `json:"content_type" form:"content_type"`
	Body        *string `json:"body" form:"body"`
	TemplateID  int     `json:"template_id" form:"template_id"`
	Messenger   *string `json:"messenger" form:"messenger"`

	// What a template can read from the campaign besides its body: a body may add the
	// pixel or a link depending on any of these.
	Name      *string         `json:"name" form:"name"`
	Subject   *string         `json:"subject" form:"subject"`
	FromEmail *string         `json:"from_email" form:"from_email"`
	ReplyTo   *string         `json:"reply_to" form:"reply_to"`
	AltBody   *string         `json:"altbody" form:"altbody"`
	Headers   json.RawMessage `json:"headers"`

	// Mode "test" checks what a test send does with these values; otherwise what saving
	// them does. The two differ for custom headers (see below).
	Mode    string       `json:"mode" form:"mode"`
	Tags    *[]string    `json:"tags"`
	Attribs *models.JSON `json:"attribs"`

	// What the campaign will be when it is sent (not for mode "test", which sends it as
	// it is now): Status is the action ("running" to start or resume, "scheduled"), and
	// SendAt the send time on the page (null for none; left out: the stored one).
	Status *string         `json:"status"`
	SendAt json.RawMessage `json:"send_at"`

	// The rest of what saving takes from the page and a template can read (not for
	// mode "test": a test send keeps the stored values, except the media it is given).
	Archive           *bool           `json:"archive"`
	ArchiveSlug       json.RawMessage `json:"archive_slug"`
	ArchiveTemplateID json.RawMessage `json:"archive_template_id"`
	ArchiveMeta       json.RawMessage `json:"archive_meta"`
	BodySource        json.RawMessage `json:"body_source"`
	Media             *[]int          `json:"media"`
}

// CheckCampaignTracking tells, before a campaign is sent, whether its views and clicks
// would be recorded (LST-44). It renders the campaign like the preview does and reads
// the result; it registers no links and writes nothing.
func (a *App) CheckCampaignTracking(c echo.Context) error {
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}

	var req trackingCheckReq
	if err := c.Bind(&req); err != nil {
		return err
	}

	contentType := ""
	if req.ContentType != nil {
		contentType = *req.ContentType
		switch contentType {
		case models.CampaignContentTypeRichtext, models.CampaignContentTypeHTML, models.CampaignContentTypeMarkdown,
			models.CampaignContentTypePlain, models.CampaignContentTypeVisual:
		default:
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "content_type"))
		}
	}

	// Like the preview: a visual campaign has no template, and no template in the
	// request means the campaign's own.
	tplID := req.TemplateID
	if contentType == models.CampaignContentTypeVisual || tplID < 1 {
		tplID = 0
	}
	// A test send reads the campaign like the preview (TestCampaign); a real send reads
	// it like next-campaigns: with the default template when it has none, its media
	// and no lists (LST-44 review: a template can read all of these).
	var camp models.Campaign
	var err error
	if req.Mode == "test" {
		camp, err = a.core.GetCampaignForPreview(id, tplID)
	} else {
		camp, err = a.core.GetCampaignForSendCheck(id, tplID)
	}
	if err != nil {
		return err
	}

	// The query above read the body of the requested template; the campaign that the
	// templates see must name the same one ({{ .Campaign.TemplateID }}), as in a test send.
	if tplID > 0 {
		camp.TemplateID = null.IntFrom(tplID)
	}
	if req.ContentType != nil {
		camp.ContentType = contentType
	}
	if req.Body != nil {
		camp.Body = *req.Body
	}
	if req.Messenger != nil && *req.Messenger != "" {
		camp.Messenger = *req.Messenger
	}
	if camp.ContentType == models.CampaignContentTypeVisual {
		camp.TemplateBody = ""
	}
	if req.Name != nil {
		camp.Name = *req.Name
	}
	if req.Subject != nil {
		camp.Subject = *req.Subject
	}
	if err := a.trackingSender(c, &camp, req); err != nil {
		return err
	}
	if req.AltBody != nil {
		camp.AltBody = null.StringFrom(*req.AltBody)
	} else if req.Body != nil {
		// The editor state without an alt body: there is none.
		camp.AltBody = null.String{}
	}
	if len(req.Headers) > 0 {
		// Saving and a test send both replace the headers with the request's
		// (UpdateCampaign decodes them from nothing), so decode the same way and
		// the check sees the headers that will really be sent.
		camp.Headers = nil
		if err := json.Unmarshal(req.Headers, &camp.Headers); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "headers"))
		}
	}
	if req.Tags != nil {
		camp.Tags = pq.StringArray(*req.Tags)
	}
	if req.Attribs != nil {
		camp.Attribs = *req.Attribs
	}
	if req.Media != nil {
		// As TestCampaign and saving: the media on the page.
		camp.MediaIDs = nil
		for _, m := range *req.Media {
			if m > 0 {
				camp.MediaIDs = append(camp.MediaIDs, int64(m))
			}
		}
	}
	if req.Mode != "test" {
		if req.Archive != nil {
			camp.Archive = *req.Archive
		}
		for _, f := range []struct {
			raw json.RawMessage
			to  any
		}{{req.ArchiveSlug, &camp.ArchiveSlug}, {req.ArchiveTemplateID, &camp.ArchiveTemplateID}, {req.BodySource, &camp.BodySource}} {
			if len(f.raw) > 0 {
				if err := json.Unmarshal(f.raw, f.to); err != nil {
					return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "archive"))
				}
			}
		}
		if len(req.ArchiveMeta) > 0 {
			camp.ArchiveMeta = append(json.RawMessage(nil), req.ArchiveMeta...)
		}
	}

	// Templates can read the campaign's status and sender. A test send renders the
	// campaign as it is; a real send renders it as it is when the manager picks it up,
	// after the status change. Render it that way here too.
	if req.Mode != "test" {
		if err := a.trackingSendState(c, &camp, req); err != nil {
			return err
		}
	}

	rep, err := a.manager.CheckTracking(&camp, dummySubscriber)
	if err != nil {
		key := "templates.errorRendering"
		var checkErr *manager.TrackingCheckError
		if errors.As(err, &checkErr) && checkErr.Compile {
			key = "templates.errorCompiling"
		}
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts(key, "error", err.Error()))
	}

	return c.JSON(http.StatusOK, okResp{rep})
}

// trackingSendState sets what a real send of the campaign will see and a test send does
// not: the status and the sender after the start or schedule (LST-44 review: a template
// with {{ if eq .Campaign.Status "draft" }} added the pixel to a test send only).
func (a *App) trackingSendState(c echo.Context, camp *models.Campaign, req trackingCheckReq) error {
	if len(req.SendAt) > 0 {
		var at null.Time
		if err := json.Unmarshal(req.SendAt, &at); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "send_at"))
		}
		camp.SendAt = at
	}

	status := models.CampaignStatusRunning
	if req.Status != nil {
		switch *req.Status {
		case models.CampaignStatusRunning, models.CampaignStatusScheduled:
			status = *req.Status
		default:
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "status"))
		}
	}
	// As the update-campaign-status query: starting a campaign with a send time
	// schedules it. The manager then sends the row as it read it, before it marks it
	// running, so a scheduled campaign is sent with the status "scheduled".
	if status == models.CampaignStatusRunning && camp.SendAt.Valid {
		status = models.CampaignStatusScheduled
	}

	// As UpdateCampaignStatus: with sender domains, starting a draft sets the sender
	// from the account of the person who starts it.
	if a.cfg != nil && len(a.cfg.SenderDomains) > 0 && camp.Status == models.CampaignStatusDraft {
		camp.FromEmail = resolveFrom(auth.GetUser(c), a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
	}
	camp.Status = status
	return nil
}

// trackingSender sets the sender (from, reply-to) that templates read, the way the send
// that is checked sets it (LST-44 review: a template that adds the pixel for one sender
// only). A test send takes the sender from the request and, with sender domains, from
// the account of the person who sends the test (TestCampaign), not the stored one.
// Otherwise the sender is the stored one (a reply-to on the page counts, as saving it
// does); the start of a draft then sets the starter's (trackingSendState).
func (a *App) trackingSender(c echo.Context, camp *models.Campaign, req trackingCheckReq) error {
	user := auth.GetUser(c)
	var s campReq
	if req.Mode == "test" {
		if req.FromEmail != nil {
			s.FromEmail = *req.FromEmail
		}
	} else {
		s.FromEmail, s.ReplyTo, s.Status = camp.FromEmail, camp.ReplyTo, camp.Status
		if req.FromEmail != nil {
			s.FromEmail = *req.FromEmail
		}
	}
	s.ReplyToInput = req.ReplyTo

	stored := s
	s, err := a.campaignSenderLoose(s, user)
	if err != nil && req.Mode != "test" {
		// Saving refuses an invalid sender (one that is still being typed) and keeps the
		// stored one: check with that.
		stored.ReplyToInput = nil
		stored.FromEmail = camp.FromEmail
		s, err = a.campaignSenderLoose(stored, user)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	camp.ReplyTo = s.ReplyTo
	switch {
	case req.Mode == "test" && len(a.cfg.SenderDomains) > 0:
		camp.FromEmail = resolveFrom(user, a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
	case req.Mode == "test" || len(a.cfg.SenderDomains) == 0:
		camp.FromEmail = s.FromEmail
	default:
		// With sender domains, saving would set the sender of the person who saves, but
		// a campaign that is only looked at (a reader, or a scheduled campaign that is
		// not saved again) is sent with the stored sender. Keep it; the start of a draft
		// sets the starter's (trackingSendState), and the page checks again after a save.
	}
	return nil
}
