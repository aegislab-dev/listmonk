package main

import (
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

type recentPreviewRequest struct {
	models.RecentRule
	ListIDs    []int `json:"list_ids"`
	CampaignID int   `json:"campaign_id"`
}

// resolveRecentSources drops deleted campaigns and checks access to each remaining source.
func (a *App) resolveRecentSources(rule *models.RecentRule, selfID int, c echo.Context) error {
	if !rule.Enabled || rule.Scope != models.RecentScopeCampaigns {
		return nil
	}
	user := auth.GetUser(c)
	if !user.HasPerm(auth.PermCampaignsGetAll) && !user.HasPerm(auth.PermCampaignsGet) {
		return auth.ErrPermDenied
	}
	sources, err := a.core.GetRecentSourceCampaigns(c.Request().Context(), rule.CampaignIDs)
	if err != nil {
		return err
	}
	rule.CampaignIDs = rule.CampaignIDs[:0]
	for _, source := range sources {
		if err := a.checkCampaignPerm(auth.PermTypeGet, source.ID, c); err != nil {
			return err
		}
		if source.ID == selfID || source.Type != models.CampaignTypeRegular {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.recent.invalidSource"))
		}
		rule.CampaignIDs = append(rule.CampaignIDs, int64(source.ID))
	}
	return nil
}

// checkRecentRequest validates the rule, the campaign and the list IDs shared by the
// preview and the list-making endpoints. forList also requires a campaign whose target can
// still be changed.
func (a *App) checkRecentRequest(c echo.Context, req *recentPreviewRequest, forList bool) error {
	req.Enabled = true
	if err := a.core.ValidateRecentRule(&req.RecentRule); err != nil {
		return err
	}
	if req.CampaignID < 0 || req.CampaignID > math.MaxInt32 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.invalid"))
	}
	if req.CampaignID != 0 {
		if err := a.checkCampaignPerm(auth.PermTypeManage, req.CampaignID, c); err != nil {
			return err
		}
		cm, err := a.core.GetRecentCampaign(req.CampaignID)
		if err != nil {
			return err
		}
		if cm.Type != models.CampaignTypeRegular {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.recent.regularOnly"))
		}
		if forList && cm.Status != models.CampaignStatusDraft && cm.Status != models.CampaignStatusScheduled {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.cantUpdate"))
		}
	}
	if err := a.resolveRecentSources(&req.RecentRule, req.CampaignID, c); err != nil {
		return err
	}
	for _, id := range req.ListIDs {
		if id <= 0 || id > math.MaxInt32 {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
		}
	}
	return nil
}

func (a *App) PreviewRecentExclusion(c echo.Context) error {
	var req recentPreviewRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	if err := a.checkRecentRequest(c, &req, false); err != nil {
		return err
	}
	user := auth.GetUser(c)
	lists := user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, req.ListIDs)
	out, err := a.core.RecentExcludeCounts(c.Request().Context(), lists, req.RecentRule, req.CampaignID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

type recentListRequest struct {
	recentPreviewRequest
	Name   string `json:"name"`
	Team   string `json:"team"`
	NoTeam bool   `json:"no_team"`
}

// MakeRecentExcludeList turns the preview's "will send" people into a new private list.
// Unlike the preview it refuses lists the user cannot read: a list made from some of the
// campaign's lists must not replace the campaign's whole target.
func (a *App) MakeRecentExcludeList(c echo.Context) error {
	var req recentListRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) > drawNameMax {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.invalidName"))
	}
	if len(req.ListIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.recent.selectLists"))
	}
	if err := a.checkRecentRequest(c, &req.recentPreviewRequest, true); err != nil {
		return err
	}
	if req.Scope == models.RecentScopeCampaigns && len(req.CampaignIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.recent.invalidCampaigns"))
	}
	user := auth.GetUser(c)
	if len(user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, req.ListIDs)) != len(req.ListIDs) {
		return echo.NewHTTPError(http.StatusForbidden, a.i18n.T("campaigns.recent.listForbidden"))
	}
	out, err := a.core.MakeRecentExcludeList(c.Request().Context(), req.ListIDs, req.RecentRule, req.CampaignID, core.RecentListOptions{
		Name:         req.Name,
		UserID:       user.ID,
		ResolveTeams: func(source []string) ([]string, error) { return a.reactionTeams(user, req.Team, req.NoTeam, source) },
	})
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	detail := map[string]any{
		"days": req.Days, "scope": req.Scope, "campaign_ids": req.CampaignIDs,
		"lists": out.SourceLists, "counts": out.Counts, "as_of": out.AsOf,
	}
	if req.CampaignID != 0 {
		if cm, err := a.core.GetRecentCampaign(req.CampaignID); err == nil {
			detail["campaign"] = map[string]any{"id": req.CampaignID, "name": cm.Name}
		}
	}
	list := out.List
	a.audit(c, auditListCreate, auditTarget{auditTargetList, list.ID, list.Name}, list.SubscriberCount, map[string]any{
		"type": list.Type, "optin": list.Optin, "teams": teamNames(list.Tags), "made_from_recent_exclude": detail,
	})
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) GetRecentRule(c echo.Context) error {
	id := getID(c)
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}
	cm, err := a.core.GetRecentCampaign(id)
	if err != nil {
		return err
	}
	out, err := a.core.GetRecentRule(id)
	if err != nil {
		return err
	}
	if out.Held, err = a.core.RecentHeldCount(id); err != nil {
		return err
	}
	if cm.Status == models.CampaignStatusFinished && out.Enabled && c.QueryParam("include_coverage") == "true" {
		// Coverage is advisory. Keep the saved rule and counts available if the ledger read fails.
		out.RecordsFrom, _ = a.core.RecentSendsCoverage(c.Request().Context())
	}
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) PutRecentRule(c echo.Context) error {
	id := getID(c)
	if err := a.checkCampaignPerm(auth.PermTypeManage, id, c); err != nil {
		return err
	}
	cm, err := a.core.GetRecentCampaign(id)
	if err != nil {
		return err
	}
	// AXM (LST-65): the rule is fixed once the campaign starts (a pause does not open it).
	if !models.CanEditRecentRule(cm.Status, cm.StartedAt.Valid) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.cantUpdate"))
	}
	var rule models.RecentRule
	if err := c.Bind(&rule); err != nil {
		return err
	}
	if err := a.core.ValidateRecentRule(&rule); err != nil {
		return err
	}
	if err := a.resolveRecentSources(&rule, id, c); err != nil {
		return err
	}
	if rule.Scope == models.RecentScopeCampaigns && len(rule.CampaignIDs) == 0 {
		rule = models.RecentRule{}
	}
	out, err := a.core.SaveRecentRule(c.Request().Context(), id, rule, auth.GetUser(c).ID)
	if err != nil {
		return err
	}
	a.audit(c, auditCampaignRecentRule, auditTarget{auditTargetCampaign, id, cm.Name}, auditNoCount, map[string]any{
		"days": out.Days, "scope": out.Scope, "campaign_ids": out.CampaignIDs, "enabled": out.Enabled,
	})
	return c.JSON(http.StatusOK, okResp{out})
}
