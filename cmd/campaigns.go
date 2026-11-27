package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/notifs"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"gopkg.in/volatiletech/null.v6"
)

// campReq is a wrapper over the Campaign model for receiving
// campaign creation and update data from APIs.
type campReq struct {
	models.Campaign
	ReplyToInput *string `json:"reply_to"`
	// RecentRule is included only when a scheduled edit changes the rule, so the
	// campaign update, rule change, and recipient check commit as one operation.
	RecentRule *models.RecentRule `json:"recent_rule"`

	// This overrides Campaign.Lists to receive and
	// write a list of int IDs during creation and updation.
	// Campaign.Lists is JSONText for sending lists children
	// to the outside world.
	ListIDs []int `json:"lists"`

	MediaIDs []int `json:"media"`

	// This is only relevant to campaign test requests.
	SubscriberEmails pq.StringArray `json:"subscribers"`

	// The From value stored for a paused campaign being updated. Set by
	// UpdateCampaign (never bound from JSON): an unchanged From on a paused
	// campaign is not re-checked against the From list.
	storedFrom    *string
	storedHeaders models.Headers

	// AXM (LST-41): the stand-in subscriber of a test request (see axm_test_send.go).
	Sample *testSample `json:"sample"`
}

// campContentReq wraps params coming from API requests for converting
// campaign content formats.
type campContentReq struct {
	models.Campaign
	From string `json:"from"`
	To   string `json:"to"`
}

var (
	reFromAddress = regexp.MustCompile(`((.+?)\s)?<(.+?)@(.+?)>`)
	reSlug        = regexp.MustCompile(`[^\p{L}\p{M}\p{N}]`)
)

// GetCampaigns handles retrieval of campaigns.
func (a *App) GetCampaigns(c echo.Context) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	var (
		hasAllPerm     = user.HasPerm(auth.PermCampaignsGetAll)
		permittedLists []int
	)

	if !hasAllPerm {
		// Either the user has campaigns:get_all permissions and can view all campaigns,
		// or the campaigns are filtered by the lists the user has get|manage access to.
		hasAllPerm, permittedLists = user.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage)
	}

	var (
		pg = a.pg.NewFromURL(c.Request().URL.Query())

		status    = c.QueryParams()["status"]
		tags      = c.QueryParams()["tag"]
		query     = strings.TrimSpace(c.FormValue("query"))
		orderBy   = c.FormValue("order_by")
		order     = c.FormValue("order")
		noBody, _ = strconv.ParseBool(c.QueryParam("no_body"))
	)

	// Query and retrieve campaigns from the DB.
	res, total, err := a.core.QueryCampaigns(query, status, tags, orderBy, order, hasAllPerm, permittedLists, pg.Offset, pg.Limit)
	if err != nil {
		return err
	}

	for i := range res {
		if err := a.campaignReplyVerified(&res[i]); err != nil {
			return err
		}
	}
	// Remove the body from the response if requested.
	if noBody {
		for i := range res {
			res[i].Body = ""
			res[i].BodySource.Valid = false
		}
	}

	// Paginate the response.
	if len(res) == 0 {
		return c.JSON(http.StatusOK, okResp{models.PageResults{Results: []models.Campaign{}}})
	}

	out := models.PageResults{
		Query:   query,
		Results: res,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// GetCampaign handles retrieval of campaigns.
func (a *App) GetCampaign(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}

	// Get the campaign from the DB.
	out, err := a.core.GetCampaign(id, "", "")
	if err != nil {
		return err
	}

	// Blank out the body if requested.
	noBody, _ := strconv.ParseBool(c.QueryParam("no_body"))
	if noBody {
		out.Body = ""
	}

	if err := a.campaignReplyVerified(&out); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// PreviewCampaign renders the HTML preview of a campaign body.
func (a *App) PreviewCampaign(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}

	var (
		isPost      = c.Request().Method == http.MethodPost
		contentType = c.FormValue("content_type")
		tplID, _    = strconv.Atoi(c.FormValue("template_id"))
	)
	// For visual content, template ID for previewing is irrelevant.
	if contentType == models.CampaignContentTypeVisual || tplID < 1 {
		tplID = 0
	}

	// Get the campaign from the DB for previewing with the `template_body` field.
	camp, err := a.core.GetCampaignForPreview(id, tplID)
	if err != nil {
		return err
	}

	// There's a body in the request to preview instead of the body in the DB.
	if isPost {
		camp.ContentType = contentType
		camp.Body = c.FormValue("body")

		// For visual campaigns, template body from the DB shouldn't be used.
		if contentType == models.CampaignContentTypeVisual {
			camp.TemplateBody = ""
		}
	}

	// Use a dummy campaign ID to prevent views and clicks from {{ TrackView }}
	// and {{ TrackLink }} being registered on preview.
	camp.UUID = dummySubscriber.UUID
	if err := camp.CompileTemplate(a.manager.TemplateFuncs(&camp)); err != nil {
		a.log.Printf("error compiling template: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("templates.errorCompiling", "error", err.Error()))
	}

	// AXM (LST-41): a preview may be asked for with the sample of the test box.
	previewSub := dummySubscriber
	if isPost {
		if previewSub, err = a.previewSample(c); err != nil {
			return err
		}
	}

	// Render the message body.
	msg, err := a.manager.NewCampaignMessage(&camp, previewSub)
	if err != nil {
		a.log.Printf("error rendering message: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("templates.errorRendering", "error", err.Error()))
	}

	// Plaintext headers for plain body.
	if camp.ContentType == models.CampaignContentTypePlain {
		return c.String(http.StatusOK, string(msg.Body()))
	}

	return renderSandboxedHTML(c, string(msg.Body()))
}

// PreviewCampaignArchive renders the public campaign archives page.
func (a *App) PreviewCampaignArchive(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}

	// Fetch the campaign body from the DB.
	tplID, _ := strconv.Atoi(c.FormValue("template_id"))
	camp, err := a.core.GetCampaignForPreview(id, tplID)
	if err != nil {
		return err
	}

	camp.ArchiveMeta = json.RawMessage([]byte(c.FormValue("archive_meta")))

	// "Compile" the campaign template with appropriate data.
	res, err := a.compileArchiveCampaigns([]models.Campaign{camp})
	if err != nil {
		return c.Render(http.StatusInternalServerError, tplMessage,
			makeMsgTpl(a.i18n.T("public.errorTitle"), "", a.i18n.Ts("public.errorFetchingCampaign")))
	}

	// Render the campaign body.
	out := res[0].Campaign
	msg, err := a.manager.NewCampaignMessage(out, res[0].Subscriber)
	if err != nil {
		a.log.Printf("error rendering campaign: %v", err)
		return c.Render(http.StatusInternalServerError, tplMessage,
			makeMsgTpl(a.i18n.T("public.errorTitle"), "", a.i18n.Ts("public.errorFetchingCampaign")))
	}

	return renderSandboxedHTML(c, string(msg.Body()))
}

// CampaignContent handles campaign content (body) format conversions.
func (a *App) CampaignContent(c echo.Context) error {
	var camp campContentReq
	if err := c.Bind(&camp); err != nil {
		return err
	}

	// Convert formats, eg: markdown to HTML.
	out, err := camp.ConvertContent(camp.From, camp.To)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// CreateCampaign handles campaign creation.
// Newly created campaigns are always drafts.
func (a *App) CreateCampaign(c echo.Context) error {
	var o campReq
	if err := c.Bind(&o); err != nil {
		return err
	}

	o.Status = models.CampaignStatusDraft

	// Filter lists against the current user's permitted lists.
	user := auth.GetUser(c)
	listIDs, err := campaignWriteListIDs(user, o.ListIDs) // AXM (LST-68)
	if err != nil {
		return err
	}
	o.ListIDs = listIDs

	// If the campaign's 'opt-in', prepare a default message.
	switch o.Type {
	case models.CampaignTypeOptin:
		op, err := a.makeOptinCampaignMessage(o)
		if err != nil {
			return err
		}
		o = op
	case "":
		o.Type = models.CampaignTypeRegular
	}

	if o.Messenger == "" {
		o.Messenger = "email"
	}

	if len(a.cfg.SenderDomains) > 0 && o.ReplyToInput == nil {
		o.ReplyTo = defaultReplyTo(user, a.cfg)
	}
	// Validate.
	if c, err := a.validateCampaignFields(o, user); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	} else {
		o = c
	}

	if o.ArchiveTemplateID.Valid && o.ArchiveTemplateID.Int != 0 {
		o.ArchiveTemplateID = o.TemplateID
	}

	out, err := a.core.CreateCampaign(o.Campaign, o.ListIDs, o.MediaIDs)
	if err != nil {
		return err
	}

	if err := a.campaignReplyVerified(&out); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateCampaign handles campaign modification.
// Campaigns that are done cannot be modified.
func (a *App) UpdateCampaign(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}

	// Retrieve the campaign from the DB.
	cm, err := a.core.GetCampaign(id, "", "")
	if err != nil {
		return err
	}

	if !canEditCampaign(cm.Status) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.cantUpdate"))
	}

	// Clear attribs to avoid merging old and new values as json.Unmarshal in JSON.scan() merges maps,
	// merging values already in the DB and incoming values. If this is nil, then DB values remain
	// unchanged.
	cm.Attribs = nil

	// Bind below writes into the maps that cm shares with o, so keep a deep
	// copy of the stored headers to compare with later.
	origHeaders := make(models.Headers, 0, len(cm.Headers))
	for _, set := range cm.Headers {
		cp := make(map[string]string, len(set))
		for k, v := range set {
			cp[k] = v
		}
		origHeaders = append(origHeaders, cp)
	}

	// Read the incoming params into the existing campaign fields from the DB.
	// This allows updating of values that have been sent whereas fields
	// that are not in the request retain the old values.
	// Headers are decoded from nothing: JSON decoding into the stored maps
	// would keep keys the request left out (a removed From header).
	// An explicit "headers": null clears them, as before; leaving the key out
	// keeps them.
	var probe struct {
		Headers json.RawMessage `json:"headers"`
	}
	if body, err := io.ReadAll(c.Request().Body); err == nil {
		c.Request().Body = io.NopCloser(bytes.NewReader(body))
		_ = json.Unmarshal(body, &probe)
	}
	o := campReq{Campaign: cm}
	o.Headers = nil
	if err := c.Bind(&o); err != nil {
		return err
	}
	if o.Headers == nil && len(probe.Headers) == 0 {
		o.Headers = cm.Headers
	}

	o.Status = cm.Status
	if len(a.cfg.SenderDomains) > 0 && cm.Status == models.CampaignStatusPaused {
		o.FromEmail = cm.FromEmail
	}
	if cm.Status == models.CampaignStatusPaused {
		o.storedFrom = &cm.FromEmail
		o.storedHeaders = origHeaders
	}

	// Filter lists against the current user's permitted lists.
	user := auth.GetUser(c)
	if o.RecentRule != nil {
		if err := a.core.ValidateRecentRule(o.RecentRule); err != nil {
			return err
		}
		if err := a.resolveRecentSources(o.RecentRule, id, c); err != nil {
			return err
		}
		// Match the dedicated rule endpoint: if every selected source was deleted,
		// there is no new rule to save (existing holds remain in the campaign ledger).
		if o.RecentRule.Scope == models.RecentScopeCampaigns && len(o.RecentRule.CampaignIDs) == 0 {
			*o.RecentRule = models.RecentRule{}
		}
	}
	listIDs, err := campaignWriteListIDs(user, o.ListIDs) // AXM (LST-68)
	if err != nil {
		return err
	}
	o.ListIDs = listIDs

	// AXM (LST-67): the last list taken off a scheduled campaign is not "invalid list IDs": say what
	// is wrong and how to go on (the check of the people on the lists is made when it is saved).
	if cm.Status == models.CampaignStatusScheduled && cm.Type == models.CampaignTypeRegular && o.SendAt.Valid && len(o.ListIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.recheck.noLists"))
	}

	if c, err := a.validateCampaignFields(o, user); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	} else {
		o = c
	}

	// A send_at value does not activate a draft. The status under the campaign row lock
	// decides whether the scheduled-save check is needed.
	if len(a.cfg.SenderDomains) > 0 && (cm.Status == models.CampaignStatusScheduled || cm.Status == models.CampaignStatusPaused) {
		if err := a.requireVerifiedReply(&o.Campaign); err != nil {
			return err
		}
	}
	var out models.Campaign
	if o.RecentRule != nil {
		// The status read above is not the update's serialization point. If another editor has
		// locked and scheduled this campaign meanwhile, the core rechecks that live status under
		// the row lock and saves the rule with the schedule check in the same transaction.
		out, err = a.core.UpdateCampaignWithRecentRule(c.Request().Context(), id, o.Campaign, o.ListIDs, o.MediaIDs,
			*o.RecentRule, user.ID, a.scheduledSaveCheck)
	} else if cm.Status == models.CampaignStatusScheduled {
		// AXM (LST-67): a scheduled campaign goes out at its time with nothing between, so what
		// it would reach is judged again when it is saved, and a save that leaves nobody to send
		// to is refused (the schedule stays as it was).
		out, err = a.core.UpdateScheduledCampaign(c.Request().Context(), id, o.Campaign, o.ListIDs, o.MediaIDs, a.scheduledSaveCheck)
	} else {
		out, err = a.core.UpdateCampaign(id, o.Campaign, o.ListIDs, o.MediaIDs)
	}
	if err != nil {
		return err
	}
	if o.RecentRule != nil {
		a.audit(c, auditCampaignRecentRule, auditTarget{auditTargetCampaign, id, cm.Name}, auditNoCount, map[string]any{
			"days": o.RecentRule.Days, "scope": o.RecentRule.Scope, "campaign_ids": o.RecentRule.CampaignIDs, "enabled": o.RecentRule.Enabled,
		})
	}

	if err := a.campaignReplyVerified(&out); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateCampaignStatus handles campaign status modification.
func (a *App) UpdateCampaignStatus(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}

	req := struct {
		Status string `json:"status"`
		// UpdatedAt is the campaign's updated_at as the pre-send confirmation showed it
		// (LST-48). When given, a campaign changed since is not started.
		UpdatedAt string `json:"updated_at"`
		// SendAtPassed: the confirmation said that the send time has passed (LST-48).
		SendAtPassed bool `json:"send_at_passed"`
	}{}
	if err := c.Bind(&req); err != nil {
		return err
	}
	// A draft can retain its send time after a schedule is cancelled. Recheck the
	// remaining audience before accepting a new schedule so sent or held people do
	// not turn a zero-recipient campaign into an allowed one.
	if req.Status == models.CampaignStatusScheduled || req.Status == models.CampaignStatusRunning {
		camp, err := a.core.GetCampaignForSendCheck(id, 0)
		if err != nil {
			return err
		}
		// Resuming a paused campaign is allowed even when its remaining audience is empty.
		// The preflight warns in that case and the manager completes the campaign without
		// sending another message. Drafts (including cancelled schedules) still need the
		// zero-recipient guard before a new start or schedule.
		pausedResume := camp.Status == models.CampaignStatusPaused && req.Status == models.CampaignStatusRunning
		if !pausedResume && camp.Type == models.CampaignTypeRegular &&
			(camp.Status == models.CampaignStatusDraft || camp.Status == models.CampaignStatusPaused) {
			hasSendHistory := camp.StartedAt.Valid || camp.LastSubscriberID > 0 || camp.Sent > 0 || camp.LegacySendHistory
			lists, aud, err := a.core.GetPreflightAudience(c.Request().Context(), id, camp.LastSubscriberID, hasSendHistory)
			if err != nil {
				return err
			}
			hasLists := false
			for _, list := range lists {
				hasLists = hasLists || !list.Deleted
			}
			hasRecipients := aud.Total-aud.Recent > 0
			if hasSendHistory {
				hasRecipients = aud.Remaining > 0
			}
			// Keep historical empty-draft behavior unless the zero audience is caused by
			// continuation history or the recent-recipient rule. Those cases are the ones
			// where an unschedule/re-schedule or immediate start could replay or lose people.
			if !hasRecipients && (hasSendHistory || aud.Recent > 0) {
				if err := a.scheduledSaveCheck(hasLists, hasRecipients); err != nil {
					return err
				}
			}
		}
	}

	from := ""
	if len(a.cfg.SenderDomains) > 0 && (req.Status == models.CampaignStatusRunning || req.Status == models.CampaignStatusScheduled) {
		camp, err := a.core.GetCampaign(id, "", "")
		if err != nil {
			return err
		}
		if err := a.requireVerifiedReply(&camp); err != nil {
			return err
		}
		if camp.Status == models.CampaignStatusDraft {
			from = resolveFrom(auth.GetUser(c), a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
		}
	}
	// A draft is only started or scheduled with a From that the From list (or,
	// with no list, the placeholder rule) allows. This also covers the start
	// button on the campaigns list, which sends without opening the campaign.
	// A From that matches the list in another spelling is stored in the
	// list's spelling as the campaign starts.
	if len(a.cfg.SenderDomains) == 0 && (req.Status == models.CampaignStatusRunning || req.Status == models.CampaignStatusScheduled) {
		camp, err := a.core.GetCampaign(id, "", "")
		if err != nil {
			return err
		}
		if camp.Status == models.CampaignStatusDraft {
			if len(a.cfg.FromAddresses) > 0 && hasFromHeader(camp.Headers) {
				return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.fromHeaderNotAllowed"))
			}
			ok, err := a.checkFromAllowed(strings.TrimSpace(camp.FromEmail), a.cfg.FromAddresses)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			if ok != camp.FromEmail {
				from = ok
			}
		}
	}
	// Update the campaign status in the DB.
	out, err := a.core.UpdateCampaignStatus(id, req.Status, from, req.UpdatedAt, req.SendAtPassed)
	if err != nil {
		return err
	}

	// If the campaign is being stopped, send the signal to the manager to stop it in flight.
	if req.Status == models.CampaignStatusPaused || req.Status == models.CampaignStatusCancelled {
		a.manager.StopCampaign(id)
	}
	a.auditCampaignStatus(c, req.Status, out)

	if err := a.campaignReplyVerified(&out); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateCampaignArchive handles campaign status modification.
func (a *App) UpdateCampaignArchive(c echo.Context) error {
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}

	req := struct {
		Archive     bool        `json:"archive"`
		TemplateID  int         `json:"archive_template_id"`
		Meta        models.JSON `json:"archive_meta"`
		ArchiveSlug string      `json:"archive_slug"`
	}{}
	if err := c.Bind(&req); err != nil {
		return err
	}

	if req.ArchiveSlug != "" {
		// Format the slug to be alpha-numeric-dash.
		s := strings.ToLower(req.ArchiveSlug)
		s = strings.TrimSpace(reSlug.ReplaceAllString(s, " "))
		s = regexpSpaces.ReplaceAllString(s, "-")
		req.ArchiveSlug = s
	}

	if err := a.core.UpdateCampaignArchive(id, req.Archive, req.TemplateID, req.Meta, req.ArchiveSlug); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{req})
}

// DeleteCampaign handles campaign deletion.
// A campaign that is running or paused, or that has sent mail (it has send records, or sent
// within their retention period), is not deleted (409): see Core.DeleteCampaignsGuarded.
func (a *App) DeleteCampaign(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}

	// Delete the campaign from the DB.
	camps, err := a.core.DeleteCampaignsGuarded([]int{id}, "", false, true, nil)
	if err != nil {
		return err
	}
	if len(camps) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
	}
	a.auditCampaignsDeleted(c, camps)

	return c.JSON(http.StatusOK, okResp{true})
}

// DeleteCampaigns deletes multiple campaigns by IDs or by query. If one of them is sending or
// has sent mail, none is deleted (409).
func (a *App) DeleteCampaigns(c echo.Context) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	var (
		hasAllPerm     = user.HasPerm(auth.PermCampaignsManageAll)
		permittedLists []int
	)

	if !hasAllPerm {
		// Either the user has campaigns:manage_all permissions and can manage all campaigns,
		// or the campaigns are filtered by the lists the user has get|manage access to.
		hasAllPerm, permittedLists = campaignWriteLists(user) // AXM (LST-68)
	}

	var (
		ids   []int
		query string
		all   bool
	)

	// Check for IDs in query params.
	if len(c.Request().URL.Query()["id"]) > 0 {
		var err error
		ids, err = parseStringIDs(c.Request().URL.Query()["id"])
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest,
				a.i18n.Ts("globals.messages.errorInvalidIDs", "error", err.Error()))
		}
	} else {
		// Check for query param.
		query = strings.TrimSpace(c.FormValue("query"))
		all = c.FormValue("all") == "true"
	}

	// Validate that either IDs or query is provided.
	if len(ids) == 0 && (query == "" && !all) {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("globals.messages.errorInvalidIDs", "error", "id or query required"))
	}

	// Delete the campaigns from the DB.
	camps, err := a.core.DeleteCampaignsGuarded(ids, query, true, hasAllPerm, permittedLists)
	if err != nil {
		return err
	}
	a.auditCampaignsDeleted(c, camps)

	return c.JSON(http.StatusOK, okResp{true})
}

// GetRunningCampaignStats returns stats of a given set of campaign IDs.
func (a *App) GetRunningCampaignStats(c echo.Context) error {
	// Get the running campaign stats from the DB.
	out, err := a.core.GetRunningCampaignStats()
	if err != nil {
		return err
	}

	if len(out) == 0 {
		return c.JSON(http.StatusOK, okResp{[]struct{}{}})
	}

	// Compute rate.
	for i, c := range out {
		if c.Started.Valid && c.UpdatedAt.Valid {
			diff := max(int(c.UpdatedAt.Time.Sub(c.Started.Time).Minutes()), 1)

			rate := c.Sent / diff
			if rate > c.Sent || rate > c.ToSend {
				rate = c.Sent
			}

			// Rate since the starting of the campaign.
			out[i].NetRate = rate

			// Realtime running rate over the last minute.
			out[i].Rate = a.manager.GetCampaignStats(c.ID).SendRate
		}
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// TestCampaign handles the sending of a campaign message to
// arbitrary subscribers for testing.
func (a *App) TestCampaign(c echo.Context) error {
	// Get the campaign ID.
	id := getID(c)

	// Check if the user has access to the campaign.
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}

	// Get and validate fields.
	var req campReq
	if err := c.Bind(&req); err != nil {
		return err
	}

	// Validate.
	if c, err := a.validateCampaignFields(req, auth.GetUser(c)); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	} else {
		req = c
	}
	if err := a.requireVerifiedReply(&req.Campaign); err != nil {
		return err
	}
	if len(req.SubscriberEmails) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.noSubsToTest"))
	}

	// Sanitize subscriber e-mails.
	for i := range req.SubscriberEmails {
		req.SubscriberEmails[i] = strings.ToLower(strings.TrimSpace(req.SubscriberEmails[i]))
	}

	// AXM (LST-41): upstream sent only to subscribers the user may see and silently dropped
	// every other address. The user's own address and the test domains may receive a test
	// too, as a sample subscriber, and an address that may not receive one stops the request.
	recips, sample, slot, err := a.testRecipients(c, &req, auth.GetUser(c))
	if err != nil {
		return err
	}

	// No subscribers.
	if len(recips) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.noKnownSubsToTest"))
	}

	// Get the campaign from the DB for previewing.
	tplID, _ := strconv.Atoi(c.FormValue("template_id"))
	// AXM (LST-44): the admin UI posts JSON, where FormValue finds nothing and the
	// stored template was used instead of the one selected in the editor.
	if tplID < 1 {
		tplID = req.TemplateID.Int
	}
	camp, err := a.core.GetCampaignForPreview(id, tplID)
	if err != nil {
		a.testLimiter.giveBack(auth.GetUser(c).ID, slot, len(recips))
		return err
	}

	// Override certain values from the DB with incoming values.
	savedName := camp.Name
	camp.Name = req.Name
	camp.Subject = req.Subject
	camp.FromEmail = req.FromEmail
	camp.ReplyTo = req.ReplyTo
	if len(a.cfg.SenderDomains) > 0 {
		camp.FromEmail = resolveFrom(auth.GetUser(c), a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
	}
	camp.Body = req.Body
	camp.AltBody = req.AltBody
	camp.Messenger = req.Messenger
	camp.ContentType = req.ContentType
	camp.Headers = req.Headers
	camp.TemplateID = req.TemplateID
	for _, id := range req.MediaIDs {
		if id > 0 {
			camp.MediaIDs = append(camp.MediaIDs, int64(id))
		}
	}

	// Send the test messages.
	for i, r := range recips {
		sub := r.Sub

		// AXM (LST-41): a sample subscriber's message carries the dummy campaign UUID.
		msgCamp := &camp
		if sub.UUID == dummyUUID {
			msgCamp = sampleCampaign(camp)
		}
		if err := a.sendTestMessage(sub, msgCamp); err != nil {
			a.log.Printf("error sending test message: %v", err)
			// AXM (LST-41): the messages that did go out are on the audit log, and the
			// ones that did not do not count towards the limit.
			a.testLimiter.giveBack(auth.GetUser(c).ID, slot, len(recips)-i)
			if i > 0 {
				a.auditTestSend(c, id, savedName, recips[:i], sample)
			}
			return echo.NewHTTPError(http.StatusInternalServerError,
				a.i18n.Ts("campaigns.errorSendTest", "error", err.Error()))
		}
	}
	a.auditTestSend(c, id, savedName, recips, sample)

	return c.JSON(http.StatusOK, okResp{true})
}

// GetCampaignViewAnalytics retrieves view counts for a campaign.
func (a *App) GetCampaignViewAnalytics(c echo.Context) error {
	ids, err := parseStringIDs(c.Request().URL.Query()["id"])
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("globals.messages.errorInvalidIDs", "error", err.Error()))
	}

	if len(ids) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("globals.messages.missingFields", "name", "`id`"))
	}

	// Ensure the user has access to campaigns via lists.
	for _, id := range ids {
		if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
			return err
		}
	}

	var (
		typ  = c.Param("type")
		from = c.QueryParams().Get("from")
		to   = c.QueryParams().Get("to")
	)
	if !strHasLen(from, 10, 30) || !strHasLen(to, 10, 30) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("analytics.invalidDates"))
	}

	// Campaign link stats.
	if typ == "links" {
		out, err := a.core.GetCampaignAnalyticsLinks(ids, typ, from, to)
		if err != nil {
			return err
		}

		return c.JSON(http.StatusOK, okResp{out})
	}

	// Get the analytics numbers from the DB for the campaigns.
	out, err := a.core.GetCampaignAnalyticsCounts(ids, typ, from, to)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// sendTestMessage takes a campaign and a subscriber and sends out a sample campaign message.
func (a *App) sendTestMessage(sub models.Subscriber, camp *models.Campaign) error {
	if err := a.manager.LoadInlineImages(camp); err != nil {
		a.log.Printf("error loading inline images: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if err := camp.CompileTemplate(a.manager.TemplateFuncs(camp)); err != nil {
		a.log.Printf("error compiling template: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			a.i18n.Ts("templates.errorCompiling", "error", err.Error()))
	}

	// AXM (LST-44): the media too, before rendering, as the real send does. Otherwise
	// the first test message renders without them and the next ones with them.
	if err := a.manager.AttachMedia(camp); err != nil {
		a.log.Printf("error attaching media: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Create a sample campaign message.
	msg, err := a.manager.NewCampaignMessage(camp, sub)
	if err != nil {
		a.log.Printf("error rendering message: %v", err)
		return echo.NewHTTPError(http.StatusNotFound, a.i18n.Ts("templates.errorRendering", "error", err.Error()))
	}
	// AXM (LST-41): the "view in browser" link of a sample subscriber's message.
	if sub.UUID == dummyUUID && camp.UUID == dummyUUID {
		a.sampleBrowserLink(&msg, camp.ContentType == models.CampaignContentTypePlain)
	}

	return a.manager.PushCampaignMessage(msg)
}

// checkFromAllowed checks a trimmed, non-empty-or-default From value against
// the team's From list. With a list, the value must match an entry and is
// returned in the list's spelling. With no list, free input stays allowed
// except for placeholder addresses.
func (a *App) checkFromAllowed(from string, list []string) (string, error) {
	if len(list) == 0 {
		if from != "" && placeholderFrom(from) {
			return from, errors.New(a.i18n.Ts("campaigns.fromPlaceholder", "email", from))
		}
		return from, nil
	}
	if e, ok := matchFromAddress(from, list); ok {
		return strings.TrimSpace(e), nil
	}
	if placeholderFrom(from) {
		return from, errors.New(a.i18n.Ts("campaigns.fromPlaceholder", "email", from))
	}
	return from, errors.New(a.i18n.Ts("campaigns.fromNotAllowed", "email", from))
}

// validateFrom checks and normalises the From of a campaign when account
// senders are off: trimmed, no line breaks, no placeholder, and (with a From
// list) one of the list's entries in the list's spelling.
func (a *App) validateFrom(c campReq, strict bool) (campReq, error) {
	fromList := a.cfg.FromAddresses
	if strings.ContainsAny(c.FromEmail, "\r\n") {
		return c, errors.New(a.i18n.T("campaigns.fieldInvalidFromEmail"))
	}
	c.FromEmail = strings.TrimSpace(c.FromEmail)
	if c.FromEmail == "" {
		// No From sent: the app default, or with a list its closest entry.
		// The default goes through the same checks, so a placeholder or a
		// stray space in app.from_email is not saved into campaigns.
		if len(fromList) > 0 {
			// The list decides; the default only picks the entry.
			c.FromEmail = defaultFromFor(strings.TrimSpace(a.cfg.FromEmail), fromList)
		} else if strings.ContainsAny(a.cfg.FromEmail, "\r\n") {
			return c, errors.New(a.i18n.T("campaigns.fieldInvalidFromEmail"))
		} else {
			c.FromEmail = strings.TrimSpace(a.cfg.FromEmail)
		}
	}

	// An entry of the list was checked when the list was saved.
	_, listed := matchFromAddress(c.FromEmail, fromList)
	if c.FromEmail != "" && !listed && !reFromAddress.Match([]byte(c.FromEmail)) {
		if _, err := a.importer.SanitizeEmail(c.FromEmail); err != nil {
			return c, errors.New(a.i18n.T("campaigns.fieldInvalidFromEmail"))
		}
	}

	// A paused campaign (maybe already sending) keeps a From it was saved with.
	unchanged := c.Status == models.CampaignStatusPaused && c.storedFrom != nil &&
		c.FromEmail == strings.TrimSpace(*c.storedFrom)
	// A paused campaign may keep a From header it already had, but not gain or
	// change one: the header would decide the From it resumes with.
	if strict && len(fromList) > 0 && hasFromHeader(c.Headers) &&
		!(c.Status == models.CampaignStatusPaused && c.storedFrom != nil && reflect.DeepEqual(c.Headers, c.storedHeaders)) {
		return c, errors.New(a.i18n.T("campaigns.fromHeaderNotAllowed"))
	}
	if !strict {
		// Read-only: use the list's spelling when there is one, never refuse.
		if e, ok := matchFromAddress(c.FromEmail, fromList); ok {
			c.FromEmail = e
		}
		return c, nil
	}
	if !unchanged {
		from, err := a.checkFromAllowed(c.FromEmail, fromList)
		if err != nil {
			return c, err
		}
		c.FromEmail = from
	}
	return c, nil
}

// validateCampaignFields validates incoming campaign field values.
func (a *App) validateCampaignFields(c campReq, user auth.User) (campReq, error) {
	if len(a.cfg.SenderDomains) > 0 && hasReservedHeader(c.Headers) {
		return c, errors.New(a.i18n.T("senders.reservedHeader"))
	}
	c, err := a.campaignSender(c, user)
	if err != nil {
		return c, err
	}

	if !strHasLen(c.Name, 1, stdInputMaxLen) {
		return c, errors.New(a.i18n.T("campaigns.fieldInvalidName"))
	}

	// Larger char limit for subject as it can contain {{ go templating }} logic.
	if !strHasLen(c.Subject, 1, 5000) {
		return c, errors.New(a.i18n.T("campaigns.fieldInvalidSubject"))
	}

	// If no content-type is specified, default to richtext.
	if c.ContentType != models.CampaignContentTypeRichtext &&
		c.ContentType != models.CampaignContentTypeHTML &&
		c.ContentType != models.CampaignContentTypePlain &&
		c.ContentType != models.CampaignContentTypeVisual &&
		c.ContentType != models.CampaignContentTypeMarkdown {
		c.ContentType = models.CampaignContentTypeRichtext
	}

	if c.ContentType != models.CampaignContentTypeVisual {
		c.BodySource.Valid = false
	}

	// If there's a "send_at" date, it should be in the future.
	if c.SendAt.Valid {
		if c.SendAt.Time.Before(time.Now()) {
			return c, errors.New(a.i18n.T("campaigns.fieldInvalidSendAt"))
		}
	}

	if len(c.ListIDs) == 0 {
		return c, errors.New(a.i18n.T("campaigns.fieldInvalidListIDs"))
	}

	if !a.manager.HasMessenger(c.Messenger) {
		// If it's a specific SMTP, but it's no longer available (removed/disabled), fall back to general email messenger.
		if strings.HasPrefix(c.Messenger, "email-") {
			c.Messenger = "email"
		} else {
			return c, errors.New(a.i18n.Ts("campaigns.fieldInvalidMessenger", "name", c.Messenger))
		}
	}

	camp := models.Campaign{Body: c.Body, TemplateBody: tplTag}
	if err := c.CompileTemplate(a.manager.TemplateFuncs(&camp)); err != nil {
		return c, errors.New(a.i18n.Ts("campaigns.fieldInvalidBody", "error", err.Error()))
	}

	if len(c.Headers) == 0 {
		c.Headers = make([]map[string]string, 0)
	}

	// Validate and initialize attribs.
	if c.Attribs != nil {
		if _, err := json.Marshal(c.Attribs); err != nil {
			return c, errors.New(a.i18n.T("subscribers.invalidJSON"))
		}
	}

	// The time zone the send time was chosen in (LST-50) must be a real IANA name.
	if !validSendTZ(c.Attribs) {
		return c, errors.New(a.i18n.T("campaigns.sendTz.invalid"))
	}

	if len(c.ArchiveMeta) == 0 {
		c.ArchiveMeta = json.RawMessage("{}")
	}

	if c.ArchiveSlug.String != "" {
		// Format the slug to be alpha-numeric-dash.
		s := strings.ToLower(c.ArchiveSlug.String)
		s = strings.TrimSpace(reSlug.ReplaceAllString(s, " "))
		s = regexpSpaces.ReplaceAllString(s, "-")

		c.ArchiveSlug = null.NewString(s, true)
	} else {
		// If there's no slug set, set it to NULL in the DB.
		c.ArchiveSlug.Valid = false
	}

	return c, nil
}

// makeOptinCampaignMessage makes a default opt-in campaign message body.
func (a *App) makeOptinCampaignMessage(o campReq) (campReq, error) {
	if len(o.ListIDs) == 0 {
		return o, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.fieldInvalidListIDs"))
	}

	// Fetch double opt-in lists from the given list IDs from the DB.
	lists, err := a.core.GetListsByOptin(o.ListIDs, models.ListOptinDouble)
	if err != nil {
		return o, err
	}

	// There are no double opt-in lists.
	if len(lists) == 0 {
		return o, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.noOptinLists"))
	}

	// Construct the opt-in URL with list IDs.
	listIDs := url.Values{}
	for _, l := range lists {
		listIDs.Add("l", l.UUID)
	}
	// optinURLFunc := template.URL("{{ OptinURL }}?" + listIDs.Encode())
	optinURLAttr := template.HTMLAttr(fmt.Sprintf(`href="{{ OptinURL }}%s"`, listIDs.Encode()))

	// Prepare sample opt-in message for the campaign.
	var b bytes.Buffer

	if err := notifs.Tpls.ExecuteTemplate(&b, "optin-campaign", struct {
		Lists        []models.List
		OptinURLAttr template.HTMLAttr
	}{lists, optinURLAttr}); err != nil {
		a.log.Printf("error compiling 'optin-campaign' template: %v", err)
		return o, echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("templates.errorCompiling", "error", err.Error()))
	}

	o.Body = b.String()
	return o, nil
}

// checkCampaignPerm checks if the user has get or manage access to the given campaign.
// Either the user has blanket get_all/manage_all permissions, or the campaign
// belongs to lists that the user has access to.
func (a *App) checkCampaignPerm(types auth.PermType, id int, c echo.Context) error {
	return a.checkCampaignPermMode(types, id, c, types&auth.PermTypeGet == 0)
}

// checkCampaignPermMode is checkCampaignPerm. strict (AXM, LST-68) asks for a write that reaches
// every list of the campaign (a send, an edit, a delete): all of them must be the user's.
func (a *App) checkCampaignPermMode(types auth.PermType, id int, c echo.Context, strict bool) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	perm := auth.PermCampaignsGet
	if types&auth.PermTypeGet != 0 {
		// It's a get request and there's a blanket get all permission.
		if user.HasPerm(auth.PermCampaignsGetAll) {
			return nil
		}
	} else {
		// It's a manage request and there's a blanket manage_all permission.
		if user.HasPerm(auth.PermCampaignsManageAll) {
			return nil
		}

		perm = auth.PermCampaignsManage
	}

	// There are no *_all campaign permissions. Instead, check if the user access
	// blanket get_all/manage_all list permissions. If yes, then the user can access
	// all campaigns. If there are no *_all permissions, then ensure that the
	// campaign belongs to the lists that the user has access to.
	hasAllPerm, permittedListIDs := user.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage)
	if types&auth.PermTypeGet == 0 {
		// AXM (LST-68): lists:get_all is read access, not a right to change campaigns.
		hasAllPerm, permittedListIDs = campaignWriteLists(user)
	}
	if !hasAllPerm {
		// AXM (LST-68): a change reaches every list of the campaign, so for a write all of them
		// must be the user's (upstream: one of them is enough).
		has := a.core.CampaignHasLists
		if strict {
			has = a.core.CampaignListsWithin
		}
		if ok, err := has(id, permittedListIDs); err != nil {
			return err
		} else if !ok {
			return echo.NewHTTPError(http.StatusForbidden,
				a.i18n.Ts("globals.messages.permissionDenied", "name", perm))
		}
	}

	return nil
}

// canEditCampaign returns true if a campaign is in a status where updating
// its properties is allowed.
func canEditCampaign(status string) bool {
	return models.CanEditCampaign(status)
}

// campaignSender sets the sender fields (from, reply-to) of a campaign request as saving
// and test sends do. AXM: split out of validateCampaignFields so that the pre-send
// tracking check (LST-44) reads the same sender as the send it checks.
func (a *App) campaignSender(c campReq, user auth.User) (campReq, error) {
	return a.campaignSenderMode(c, user, true)
}

// campaignSenderLoose is campaignSender for a read-only look (the tracking
// check): the From still gets its format check and the list's spelling, but a
// From that the From list or the placeholder rule would refuse on save (a saved
// legacy draft, a paused campaign) is read as it is, not refused.
func (a *App) campaignSenderLoose(c campReq, user auth.User) (campReq, error) {
	return a.campaignSenderMode(c, user, false)
}

func (a *App) campaignSenderMode(c campReq, user auth.User, strict bool) (campReq, error) {
	if len(a.cfg.SenderDomains) > 0 {
		if c.ReplyToInput != nil {
			c.ReplyTo = strings.TrimSpace(*c.ReplyToInput)
		}
		if c.ReplyTo != "" {
			if _, err := mail.ParseAddress(c.ReplyTo); err != nil || strings.ContainsAny(c.ReplyTo, "\r\n") {
				return c, errors.New(a.i18n.T("senders.invalidEmail"))
			}
			addr, _ := mail.ParseAddress(c.ReplyTo)
			if addr.Name == "" {
				c.ReplyTo = addr.Address
			} else {
				c.ReplyTo = strconv.Quote(addr.Name) + " " + (&mail.Address{Address: addr.Address}).String()
			}
		}
		if c.Status == "" || c.Status == models.CampaignStatusDraft || c.Status == models.CampaignStatusScheduled {
			c.FromEmail = resolveFrom(user, a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains)
		}
	} else {
		c.ReplyTo = ""
	}
	// With account senders on, the From was set above and neither the From
	// list nor the placeholder rule applies (the original format check stays).
	if len(a.cfg.SenderDomains) == 0 {
		return a.validateFrom(c, strict)
	}
	if c.FromEmail == "" {
		c.FromEmail = a.cfg.FromEmail
	} else if !reFromAddress.Match([]byte(c.FromEmail)) {
		if _, err := a.importer.SanitizeEmail(c.FromEmail); err != nil {
			return c, errors.New(a.i18n.T("campaigns.fieldInvalidFromEmail"))
		}
	}
	return c, nil
}

// scheduledSaveCheck refuses the save of a scheduled regular campaign that would reach nobody
// (LST-67): no list left to send to, or no one on them. It asks audienceBlock, as the
// pre-send window does, so the two cannot disagree on what is blocked. Warnings are not judged
// here: the page reads them from the preflight and shows them.
//
// The check follows the sender, including deliveries recorded before its progress.
func (a *App) scheduledSaveCheck(hasLists, hasRecipients bool) error {
	key, level := audienceBlock(models.CampaignTypeRegular, models.PreflightModeScheduled, hasLists, hasRecipients)
	if level != models.PreflightBlock {
		return nil
	}
	msg := "campaigns.recheck.zero"
	if key == "lists.none" {
		msg = "campaigns.recheck.noLists"
	}
	return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T(msg))
}
