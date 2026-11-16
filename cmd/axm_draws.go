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
}

// PreviewListDraw counts a draw from the list without making anything. It needs the same
// permission as making a list (lists:manage_all), which covers every list.
func (a *App) PreviewListDraw(c echo.Context) error {
	var req drawRequest
	if err := c.Bind(&req); err != nil {
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
	list, draw, err := a.core.DrawList(getID(c), req.DrawSettings, core.DrawOpts{
		Name:   req.Name,
		UserID: user.ID,
		Describe: func(source string, at time.Time, r models.DrawCounts) string {
			return a.i18n.Ts("lists.draw.description", "source", source, "time", at.Format("2006-01-02 15:04"),
				"num", strconv.Itoa(r.WillDraw), "target", strconv.Itoa(r.Target),
				"drawn", strconv.Itoa(r.AlreadyDrawn), "received", strconv.Itoa(r.AlreadyReceived),
				"candidates", strconv.Itoa(r.Candidates))
		},
	})
	if err != nil {
		return err
	}
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
