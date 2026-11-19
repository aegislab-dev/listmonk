package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// drawNameMax is the longest list name the list form accepts.
const drawNameMax = 200

type drawRequest struct {
	models.DrawSettings
	Name string `json:"name"`

	// Team (a team tag) and NoTeam are for admins drawing from a list that has no team.
	Team   string `json:"team"`
	NoTeam bool   `json:"no_team"`
}

// PreviewListDraw counts a draw from the list without making anything. It needs the
// permission to make lists (lists:manage_all, or a team member) and to read the source.
func (a *App) PreviewListDraw(c echo.Context) error {
	var req drawRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	if err := a.checkDrawSource(auth.GetUser(c), getID(c)); err != nil {
		return err
	}
	out, err := a.core.PreviewListDraw(getID(c), req.DrawSettings)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// DrawList makes a new ordinary list of people drawn from the list.
func (a *App) DrawList(c echo.Context) error {
	var req drawRequest
	if err := c.Bind(&req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) > drawNameMax {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.invalidName"))
	}
	user := auth.GetUser(c)
	if err := a.checkDrawSource(user, getID(c)); err != nil {
		return err
	}
	setTeams, teamTags, err := a.teamsForNewList(user, req.Team, req.NoTeam, func() ([]string, error) {
		src, err := a.core.GetList(getID(c), "")
		return models.TeamTags(src.Tags), err
	})
	if err != nil {
		return err
	}
	list, draw, err := a.core.DrawList(getID(c), req.DrawSettings, core.DrawOpts{
		Name:     req.Name,
		UserID:   user.ID,
		SetTeams: setTeams,
		TeamTags: teamTags,
		Describe: func(source string, at time.Time, r models.DrawCounts) string {
			return a.i18n.Ts("lists.draw.description", "source", source, "time", at.Format("2006-01-02 15:04 MST"),
				"num", strconv.Itoa(r.WillDraw), "target", strconv.Itoa(r.Target),
				"drawn", strconv.Itoa(r.AlreadyDrawn), "received", strconv.Itoa(r.AlreadyReceived),
				"candidates", strconv.Itoa(r.Candidates))
		},
	})
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.audit(c, auditListCreate, auditTarget{auditTargetList, list.ID, list.Name}, list.SubscriberCount, map[string]any{
		"type": list.Type, "optin": list.Optin, "teams": teamNames(list.Tags),
		"drawn_from": map[string]any{"id": getID(c), "name": draw.SourceName},
	})
	return c.JSON(http.StatusOK, okResp{struct {
		List models.List     `json:"list"`
		Draw models.ListDraw `json:"draw"`
	}{list, draw}})
}

// GetListDraws returns the draw records of a source list (?source_id=) or of some drawn
// lists (?list_id=, repeated). A record shows to a user who may read its drawn list or its
// source.
func (a *App) GetListDraws(c echo.Context) error {
	user := auth.GetUser(c)
	hasAll, permitted := user.GetPermittedLists(auth.PermTypeGet)
	allowed := map[int]bool{}
	for _, id := range permitted {
		allowed[id] = true
	}

	sourceID, _ := strconv.Atoi(c.FormValue("source_id"))
	if sourceID < 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid ID")
	}
	if sourceID > 0 && !hasAll && !allowed[sourceID] {
		return auth.ErrPermDenied
	}
	var listIDs []int
	for _, v := range c.QueryParams()["list_id"] {
		id, err := strconv.Atoi(v)
		if err != nil || id < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid ID")
		}
		listIDs = append(listIDs, id)
	}
	limit := 500
	if len(listIDs) > 0 {
		limit = len(listIDs)
	}
	rows, err := a.core.GetListDraws(sourceID, listIDs, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("lists.draw.error"))
	}
	out := make([]models.ListDraw, 0, len(rows))
	for _, d := range rows {
		seen := hasAll ||
			(d.ListID != nil && allowed[*d.ListID]) ||
			(d.SourceListID != nil && allowed[*d.SourceListID])
		if !seen {
			continue
		}
		out = append(out, d)
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// teamsForNewList resolves an explicit team or falls back to the source's team tags.
func (a *App) teamsForNewList(user auth.User, team string, noTeam bool, sourceTeams func() ([]string, error)) (bool, []string, error) {
	if !user.HasPerm(auth.PermListManageAll) {
		own := a.teamTagOf(user)
		if own == "" {
			return false, nil, auth.ErrPermDenied
		}
		return true, []string{own}, nil
	}
	if t := cleanTags([]string{team}); len(t) == 1 {
		if err := a.checkAdminTeams(t, false); err != nil {
			return false, nil, err
		}
		return true, t, nil
	}
	teams, err := sourceTeams()
	if err != nil {
		return false, nil, err
	}
	if err := a.checkAdminTeams(teams, noTeam); err != nil {
		return false, nil, err
	}
	return false, nil, nil
}

// checkDrawSource lets a user who manages all lists draw from any list, and anybody else only
// from a list they may read.
func (a *App) checkDrawSource(user auth.User, id int) error {
	if user.HasPerm(auth.PermListManageAll) {
		return nil
	}
	return user.HasListPerm(auth.PermTypeGet, id)
}
