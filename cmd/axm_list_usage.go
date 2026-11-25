package main

import (
	"net/http"
	"strconv"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// listUseMaxIDs bounds ?list_id= (a page of the list table is far below it).
const listUseMaxIDs = 200

// GetListCampaigns returns the campaigns that used the given lists (?list_id=, repeated):
// the import screen warns about a list that already went out, and the list table shows
// where each one was used. Lists the user may not read are left out without a word. The
// campaign number, status, count and dates show to anyone who may read the list; the name
// only to a user who may read campaigns (LST-63).
func (a *App) GetListCampaigns(c echo.Context) error {
	user := auth.GetUser(c)

	var ids []int
	for _, v := range c.QueryParams()["list_id"] {
		id, err := strconv.Atoi(v)
		if err != nil || id < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid ID")
		}
		ids = append(ids, id)
	}
	if len(ids) > listUseMaxIDs {
		return echo.NewHTTPError(http.StatusBadRequest, "too many lists")
	}

	seen := make([]int, 0, len(ids))
	for _, id := range ids {
		if user.HasListPerm(auth.PermTypeGet, id) == nil || user.HasListPerm(auth.PermTypeManage, id) == nil {
			seen = append(seen, id)
		}
	}

	rows, err := a.core.GetListCampaigns(seen)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.Ts("globals.messages.errorFetching",
			"name", "{globals.terms.campaigns}", "error", ""))
	}
	if !user.HasPerm(auth.PermCampaignsGetAll) && !user.HasPerm(auth.PermCampaignsGet) {
		for i := range rows {
			rows[i].Name = ""
		}
	}
	if rows == nil {
		rows = []models.ListCampaignUse{}
	}
	return c.JSON(http.StatusOK, okResp{rows})
}
