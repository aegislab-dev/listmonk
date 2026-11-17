package main

import (
	"encoding/json"

	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// campaignActions maps the status a campaign is set to onto the audit action.
var campaignActions = map[string]string{
	models.CampaignStatusRunning:   auditCampaignStart,
	models.CampaignStatusScheduled: auditCampaignSchedule,
	models.CampaignStatusDraft:     auditCampaignUnschedule,
	models.CampaignStatusPaused:    auditCampaignPause,
	models.CampaignStatusCancelled: auditCampaignCancel,
}

// auditCampaignStatus writes a campaign start, schedule, unschedule, pause or cancel, with the
// campaign, the lists it goes to and how many people that is (when the campaign says).
func (a *App) auditCampaignStatus(c echo.Context, status string, camp models.Campaign) {
	action, ok := campaignActions[status]
	if !ok {
		return
	}
	d := map[string]any{"status": camp.Status}
	if len(camp.Lists) > 0 {
		d["lists"] = json.RawMessage(camp.Lists)
	}
	count := auditNoCount
	if camp.ToSend > 0 {
		count = camp.ToSend
	}
	a.audit(c, action, auditTarget{auditTargetCampaign, camp.ID, camp.Name}, count, d)
}

// auditCampaignsDeleted writes campaign.delete: one row for each campaign deleted.
func (a *App) auditCampaignsDeleted(c echo.Context, camps []core.CampaignRef) {
	for _, camp := range camps {
		a.audit(c, auditCampaignDelete, auditTarget{auditTargetCampaign, camp.ID, camp.Name}, auditNoCount, nil)
	}
}
