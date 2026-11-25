package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	null "gopkg.in/volatiletech/null.v6"
)

// A follow-up campaign (LST-47) is a draft made together with a reaction list: the people who
// got a finished campaign and did not react to it (or reacted in some other way), as a list,
// plus a copy of the campaign that sends only to that list. The person making it schedules
// it like any campaign. Before it starts the sender checks the list again (see
// core.RecheckFollowups), and its unsubscribe link also covers the original's lists.

// followupDraft copies the finished campaign into a draft that passes the same checks as a
// copy made in the editor. The list it will send to does not exist yet.
func (a *App) followupDraft(c echo.Context, user auth.User, parentID int, name string) (*core.FollowupDraft, error) {
	parent, err := a.core.GetCampaign(parentID, "", "")
	if err != nil {
		return nil, err
	}
	if parent.Type != models.CampaignTypeRegular {
		return nil, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.followup.notRegular"))
	}

	var lists, media []struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(parent.Lists, &lists)
	_ = json.Unmarshal(parent.Media, &media)
	req := campReq{Campaign: parent}
	for _, l := range lists {
		req.ListIDs = append(req.ListIDs, l.ID)
	}
	for _, m := range media {
		req.MediaIDs = append(req.MediaIDs, m.ID)
	}
	reply := parent.ReplyTo
	req.ReplyToInput = &reply

	name = strings.TrimSpace(name)
	if name == "" {
		name = a.core.FollowupName(parent.Name)
	}
	if utf8.RuneCountInString(name) > stdInputMaxLen {
		return nil, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.fieldInvalidName"))
	}
	req.Name = name
	req.Status = models.CampaignStatusDraft
	req.SendAt = null.Time{}
	// The copy has no send time, so it keeps no time zone for one, but every other attribute
	// of the campaign stays: the body may read them ({{ .Campaign.Attribs.x }}). The copy is
	// made so that the parent's own map is not changed. The slug stays unique.
	req.Attribs = nil
	if len(parent.Attribs) > 0 {
		req.Attribs = make(models.JSON, len(parent.Attribs))
		for k, v := range parent.Attribs {
			if k != sendTZKey {
				req.Attribs[k] = v
			}
		}
	}
	req.ArchiveSlug = null.String{}
	req.Tags = append(append([]string{}, parent.Tags...), models.FollowupTag)
	if len(req.ListIDs) == 0 {
		// The check only needs some list; the real one is the new list.
		req.ListIDs = []int{0}
	}
	if o, err := a.validateCampaignFields(req, user); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
	} else {
		req = o
	}
	return &core.FollowupDraft{Campaign: req.Campaign, MediaIDs: req.MediaIDs, UserID: user.ID}, nil
}

// canMakeFollowup is true for a user who may change the campaign: the draft is a campaign
// the user makes, so the campaign permissions apply as they do to any new campaign. The
// original is not changed, so one of its lists being the user's is enough (LST-68: a change
// to the original asks for all of them).
func (a *App) canMakeFollowup(c echo.Context, user auth.User, id int) bool {
	if user.UserRole.ID != auth.SuperAdminRoleID &&
		!user.HasPerm(auth.PermCampaignsManage) && !user.HasPerm(auth.PermCampaignsManageAll) {
		return false
	}
	return a.checkCampaignPermMode(auth.PermTypeManage, id, c, false) == nil
}

// GetCampaignFollowup answers whether a campaign is the original or the follow-up of a link,
// with the condition, the counts made at the time and what the checks before sending removed.
func (a *App) GetCampaignFollowup(c echo.Context) error {
	id := getID(c)
	if err := a.checkCampaignPerm(auth.PermTypeGet, id, c); err != nil {
		return err
	}
	out, err := a.core.GetFollowupInfo(c.Request().Context(), id, true)
	if err != nil {
		a.log.Printf("error reading the follow-up of campaign %d: %v", id, err)
		return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("campaigns.followup.error"))
	}
	// The other campaign of the link may belong to a team this user cannot read: say nothing
	// about its name or state then. A follow-up whose original is hidden still shows its own
	// condition and counts; an original whose follow-up is hidden shows no link at all.
	switch out.Role {
	case "parent":
		if a.checkCampaignPerm(auth.PermTypeGet, out.Followup.ID, c) != nil {
			return c.JSON(http.StatusOK, okResp{models.FollowupInfo{Role: "none"}})
		}
	case "followup":
		if a.checkCampaignPerm(auth.PermTypeGet, out.Parent.ID, c) != nil {
			out.Parent = &models.FollowupRef{ID: out.Parent.ID}
		}
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// hideUnreadableFollowup takes an existing follow-up out of a preview when the user could
// not open that campaign, so the dialog does not offer a link that leads nowhere.
func (a *App) hideUnreadableFollowup(c echo.Context, out *models.ReactionPreview) {
	f := out.Followup
	if f == nil || f.Existing == nil {
		return
	}
	if a.checkCampaignPerm(auth.PermTypeGet, f.Existing.ID, c) != nil {
		f.Existing, f.Reason = nil, "exists_hidden"
	}
}

// followupTracking tells whether opens and clicks are recorded per subscriber now: individual
// tracking on and tracking as a whole not switched off (LST-47, the check before sending).
func (a *App) followupTracking() bool {
	return a.cfg.Privacy.IndividualTracking && !a.cfg.Privacy.DisableTracking
}

func (a *App) auditFollowup(c echo.Context, out models.ReactionResult, s models.ReactionSettings) {
	if out.Followup == nil {
		return
	}
	a.audit(c, auditCampaignFollowup, auditTarget{auditTargetCampaign, out.Followup.ID, out.Followup.Name}, out.Counts.Eligible, map[string]any{
		"parent": map[string]any{"id": out.Campaign.ID, "name": out.Campaign.Name},
		"list":   map[string]any{"id": out.List.ID, "name": out.List.Name},
		"open":   s.Open, "click": s.Click,
		"counts": struct {
			Received int `json:"received"`
			models.ReactionCounts
		}{out.Tracking.Sends, out.Counts},
	})
}
