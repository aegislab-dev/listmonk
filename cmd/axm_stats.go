package main

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	null "gopkg.in/volatiletech/null.v6"
)

// Statistics dashboard (LST-64). Results are public to everyone who may see analytics, in
// every team: counts, rates, the name and the subject of a campaign. Who the lists are, who
// is on them, addresses and bodies stay behind the permissions they already had: a campaign
// that the caller could not open carries no list ids or names here.

const statsDefaultDays = 90

// statsRange reads ?from=&to= (RFC 3339 or YYYY-MM-DD); the default is the last 90 days.
func statsRange(c echo.Context) (time.Time, time.Time, error) {
	to := time.Now().UTC().Truncate(5 * time.Minute).Add(5 * time.Minute)
	from := to.AddDate(0, 0, -statsDefaultDays)
	parse := func(s string) (time.Time, error) {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, nil
		}
		return time.Parse("2006-01-02", s)
	}
	if s := c.QueryParam("from"); s == "all" {
		from = time.Time{}
	} else if s != "" {
		t, err := parse(s)
		if err != nil {
			return from, to, echo.NewHTTPError(http.StatusBadRequest, "invalid from")
		}
		from = t
	}
	if s := c.QueryParam("to"); s != "" {
		t, err := parse(s)
		if err != nil {
			return from, to, echo.NewHTTPError(http.StatusBadRequest, "invalid to")
		}
		to = t
	}
	if !to.After(from) {
		return from, to, echo.NewHTTPError(http.StatusBadRequest, "invalid range")
	}
	return from, to, nil
}

// statsOwn tells whether the caller may read the campaign, by the rule checkCampaignPerm uses
// for reading: campaigns:get_all, a permission over every list, or one of its lists.
func statsOwn(user auth.User, c models.StatsCampaign) bool {
	if user.HasPerm(auth.PermCampaignsGetAll) {
		return true
	}
	if !user.HasPerm(auth.PermCampaignsGet) {
		return false
	}
	all, permitted := user.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage)
	if all {
		return true
	}
	for _, id := range c.ListIDs {
		if slices.Contains(permitted, int(id)) {
			return true
		}
	}
	return false
}

// GetStatsCampaigns returns the campaigns of the range with their counts and the teams.
func (a *App) GetStatsCampaigns(c echo.Context) error {
	user := auth.GetUser(c)
	from, to, err := statsRange(c)
	if err != nil {
		return err
	}
	if c.QueryParam("overlap") == "true" {
		id := 0
		if raw := c.QueryParam("id"); raw != "" {
			id, err = strconv.Atoi(raw)
			if err != nil || id <= 0 {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid campaign id")
			}
		}
		overlap, err := a.core.StatsOverlap(from, to, id)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, okResp{overlap})
	}
	camps, err := a.core.StatsCampaigns(from, to)
	if err != nil {
		return err
	}
	teams, err := a.core.StatsTeams()
	if err != nil {
		return err
	}
	for i := range camps {
		ca := &camps[i]
		ca.Own = statsOwn(user, *ca)
		if ca.Own {
			for j, id := range ca.ListIDs {
				ca.Lists = append(ca.Lists, models.StatsList{
					ID: int(id), Name: ca.ListNames[j], Source: ca.ListSources[j], CreatedAt: ca.ListCreatedAt[j],
				})
			}
		}
		// Proof v1 uses the current tracking configuration and whole-campaign event counts.
		historicalOff := ca.TrackingOff && ca.Viewers == 0 && ca.Clickers == 0
		ca.TrackingOff = historicalOff || !a.cfg.Privacy.IndividualTracking || a.cfg.Privacy.DisableTracking
		if !a.cfg.Privacy.IndividualTracking || a.cfg.Privacy.DisableTracking {
			ca.CombinedViewers, ca.CombinedClickers = null.Int{}, null.Int{}
		}
	}
	return c.JSON(http.StatusOK, okResp{struct {
		GeneratedAt time.Time              `json:"generated_at"`
		From        time.Time              `json:"from"`
		To          time.Time              `json:"to"`
		MyTeam      string                 `json:"my_team"`
		Teams       []models.StatsTeam     `json:"teams"`
		Campaigns   []models.StatsCampaign `json:"campaigns"`
	}{time.Now(), from, to, a.teamTagOf(user), teams, camps}})
}

// GetStatsRecipients returns the recipient groups (by=age|country) for everyone and for each
// team. Groups of fewer than 30 recipients are merged into "other" by the server.
func (a *App) GetStatsRecipients(c echo.Context) error {
	from, to, err := statsRange(c)
	if err != nil {
		return err
	}
	groups, err := a.core.StatsRecipients(from, to, c.QueryParam("by"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{groups})
}
