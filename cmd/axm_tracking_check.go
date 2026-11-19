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
	camp, err := a.core.GetCampaignForPreview(id, tplID)
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
	if req.FromEmail != nil {
		camp.FromEmail = *req.FromEmail
	}
	if req.AltBody != nil {
		camp.AltBody = null.StringFrom(*req.AltBody)
	} else if req.Body != nil {
		// The editor state without an alt body: there is none.
		camp.AltBody = null.String{}
	}
	if len(req.Headers) > 0 {
		// A test send replaces the headers. Saving decodes the request into the stored
		// campaign, where a header that is no longer in the request stays in the set it
		// was in (UpdateCampaign binds over the stored row). Decode the same way, so the
		// check sees the headers that will really be sent.
		if req.Mode == "test" {
			camp.Headers = nil
		}
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
