package main

import (
	"errors"
	"net/http"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// trackingCheckReq carries the unsaved editor state. A field that is left out keeps
// the value stored on the campaign.
type trackingCheckReq struct {
	ContentType *string `json:"content_type" form:"content_type"`
	Body        *string `json:"body" form:"body"`
	TemplateID  int     `json:"template_id" form:"template_id"`
	Messenger   *string `json:"messenger" form:"messenger"`
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
