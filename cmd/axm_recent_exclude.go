package main

import (
	"math"
	"net/http"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

type recentPreviewRequest struct {
	models.RecentRule
	ListIDs    []int `json:"list_ids"`
	CampaignID int   `json:"campaign_id"`
}

func (a *App) checkRecentSources(rule models.RecentRule, c echo.Context) error {
	if !rule.Enabled || rule.Scope != models.RecentScopeCampaigns {
		return nil
	}
	user := auth.GetUser(c)
	if !user.HasPerm(auth.PermCampaignsGetAll) && !user.HasPerm(auth.PermCampaignsGet) {
		return auth.ErrPermDenied
	}
	for _, id := range rule.CampaignIDs {
		if err := a.checkCampaignPerm(auth.PermTypeGet, int(id), c); err != nil {
			return err
		}
		if _, err := a.core.GetRecentCampaign(int(id)); err != nil {
			if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusBadRequest {
				return auth.ErrPermDenied
			}
			return err
		}
	}
	return nil
}

func (a *App) PreviewRecentExclusion(c echo.Context) error {
	var req recentPreviewRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
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
	}
	if err := a.checkRecentSources(req.RecentRule, c); err != nil {
		return err
	}
	for _, id := range req.ListIDs {
		if id <= 0 || id > math.MaxInt32 {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
		}
	}
	user := auth.GetUser(c)
	lists := user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, req.ListIDs)
	out, err := a.core.RecentExcludeCounts(c.Request().Context(), lists, req.RecentRule, req.CampaignID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) GetRecentRule(c echo.Context) error {
	id := getID(c)
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}
	if _, err := a.core.GetRecentCampaign(id); err != nil {
		return err
	}
	out, err := a.core.GetRecentRule(id)
	if err != nil {
		return err
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
	if !canEditCampaign(cm.Status) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.cantUpdate"))
	}
	var rule models.RecentRule
	if err := c.Bind(&rule); err != nil {
		return err
	}
	if err := a.core.ValidateRecentRule(&rule); err != nil {
		return err
	}
	if err := a.checkRecentSources(rule, c); err != nil {
		return err
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
