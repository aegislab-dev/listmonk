package main

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

type reactionRequest struct {
	models.ReactionSettings
	TrackingOnly bool   `json:"tracking_only"`
	Name         string `json:"name"`
	Team         string `json:"team"`
	NoTeam       bool   `json:"no_team"`
}

// reactionScope checks the same campaign read permission as the campaign page and the
// additional analytics permission. List-making permission is checked by listMaker.
func (a *App) reactionScope(c echo.Context, user auth.User) (core.ReactionScope, error) {
	if user.UserRole.ID != auth.SuperAdminRoleID &&
		(!user.HasPerm(auth.PermCampaignsGetAnalytics) ||
			(!user.HasPerm(auth.PermCampaignsGet) && !user.HasPerm(auth.PermCampaignsGetAll))) {
		return core.ReactionScope{}, auth.ErrPermDenied
	}
	if err := a.checkCampaignPerm(auth.PermTypeGet, getID(c), c); err != nil {
		return core.ReactionScope{}, err
	}
	allLists, permitted := user.GetPermittedLists(auth.PermTypeGet)
	if !allLists && permitted == nil {
		permitted = []int{}
	}
	return core.ReactionScope{
		AllSubscribers: user.HasPerm(auth.PermSubscribersGetAll),
		PermittedLists: permitted, IndividualTracking: a.cfg.Privacy.IndividualTracking,
	}, nil
}

func (a *App) reactionTeams(user auth.User, req reactionRequest, source []string) ([]string, error) {
	if user.HasPerm(auth.PermListManageAll) {
		if req.NoTeam {
			if strings.TrimSpace(req.Team) != "" {
				return nil, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.reactionList.chooseOneTeamOption"))
			}
			return nil, nil
		}
		if strings.TrimSpace(req.Team) == "" && len(source) == 0 {
			return nil, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.team.required"))
		}
		if !a.core.HasTeamRoles() {
			return nil, echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.reactionList.chooseOneTeamOption"))
		}
	}
	set, tags, err := a.teamsForNewList(user, req.Team, req.NoTeam, func() ([]string, error) {
		return source, nil
	})
	if err != nil {
		return nil, err
	}
	if set {
		return tags, nil
	}
	return source, nil
}

func (a *App) PreviewReactionList(c echo.Context) error {
	user := auth.GetUser(c)
	scope, err := a.reactionScope(c, user)
	if err != nil {
		return err
	}
	var req reactionRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	out, err := a.core.PreviewReactionList(c.Request().Context(), getID(c), req.ReactionSettings, scope, req.TrackingOnly)
	if err != nil {
		return a.reactionListError(err)
	}
	if !user.HasPerm(auth.PermListManageAll) {
		out.SourceTeams = []string{}
	}
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) MakeReactionList(c echo.Context) error {
	user := auth.GetUser(c)
	scope, err := a.reactionScope(c, user)
	if err != nil {
		return err
	}
	var req reactionRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) > drawNameMax {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.invalidName"))
	}
	if err := req.ReactionSettings.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("campaigns.reactionList.invalidCondition"))
	}
	out, err := a.core.MakeReactionList(c.Request().Context(), getID(c), req.ReactionSettings, core.ReactionListOptions{
		Name: req.Name, Scope: scope,
		ResolveTeams: func(source []string) ([]string, error) { return a.reactionTeams(user, req, source) },
	})
	if err != nil {
		return a.reactionListError(err)
	}
	a.refreshTeamUsers()
	list := out.List
	a.audit(c, auditListCreate, auditTarget{auditTargetList, list.ID, list.Name}, list.SubscriberCount, map[string]any{
		"type": list.Type, "optin": list.Optin, "teams": teamNames(list.Tags),
		"made_from_campaign": map[string]any{
			"id": out.Campaign.ID, "name": out.Campaign.Name, "open": req.Open, "click": req.Click,
			"counts": struct {
				Received int `json:"received"`
				models.ReactionCounts
			}{out.Tracking.Sends, out.Counts},
		},
	})
	return c.JSON(http.StatusOK, okResp{out})
}

func (a *App) reactionListError(err error) error {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	a.log.Printf("error preparing campaign reaction list: %v", err)
	return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("campaigns.reactionList.error"))
}
