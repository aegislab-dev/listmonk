package core

import (
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// Statistics dashboard (LST-64). The numbers are aggregates of every team, the same for
// everyone, so they are cached for a few minutes whoever asks; what a person may see of them
// is decided after the cache, by the handler.
const statsCacheTTL = 5 * time.Minute

type statsCacheEntry struct {
	at   time.Time
	data any
}

var (
	statsCacheMu sync.Mutex
	statsCache   = map[string]statsCacheEntry{}
)

func statsCached(key string, load func() (any, error)) (any, error) {
	statsCacheMu.Lock()
	e, ok := statsCache[key]
	statsCacheMu.Unlock()
	if ok && time.Since(e.at) < statsCacheTTL {
		return e.data, nil
	}
	// Loaded without the lock: a load may read another cached value (recipients read the campaigns).
	d, err := load()
	if err != nil {
		return nil, err
	}
	statsCacheMu.Lock()
	if len(statsCache) > 64 {
		statsCache = map[string]statsCacheEntry{}
	}
	statsCache[key] = statsCacheEntry{at: time.Now(), data: d}
	statsCacheMu.Unlock()
	return d, nil
}

// ResetStatsCache forgets the cached statistics (tests).
func ResetStatsCache() {
	statsCacheMu.Lock()
	statsCache = map[string]statsCacheEntry{}
	statsCacheMu.Unlock()
}

// StatsCampaigns returns the campaigns that started in [from, to). The slice is shared by the
// cache: callers copy before changing.
func (c *Core) StatsCampaigns(from, to time.Time) ([]models.StatsCampaign, error) {
	d, err := statsCached("c|"+from.Format(time.RFC3339)+"|"+to.Format(time.RFC3339), func() (any, error) {
		out := []models.StatsCampaign{}
		if err := c.q.AxmStatsCampaigns.Select(&out, from, to, models.TeamTagPrefix); err != nil {
			c.log.Printf("error reading campaign statistics: %v", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("stats.errorReading"))
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(d.([]models.StatsCampaign)), nil
}

// StatsTeams returns every team role with its list count.
func (c *Core) StatsTeams() ([]models.StatsTeam, error) {
	out := []models.StatsTeam{}
	if err := c.q.AxmStatsTeams.Select(&out); err != nil {
		c.log.Printf("error reading teams: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("stats.errorReading"))
	}
	return out, nil
}

// StatsRecipients sums the send records of the finished campaigns by group (by = "age" or
// "country") for everyone (team "") and for each team tag of the campaign, and merges the
// groups of fewer than models.StatsMinGroup recipients into models.StatsOtherKey.
func (c *Core) StatsRecipients(from, to time.Time, by string) ([]models.StatsRecipientGroup, error) {
	if by != "age" && by != "country" {
		return nil, echo.NewHTTPError(http.StatusBadRequest, c.i18n.Ts("globals.messages.invalidFields", "name", "by"))
	}
	d, err := statsCached("r|"+by+"|"+from.Format(time.RFC3339)+"|"+to.Format(time.RFC3339), func() (any, error) {
		rows := []models.StatsRecipientRow{}
		if err := c.q.AxmStatsRecipients.Select(&rows, from, to, by); err != nil {
			c.log.Printf("error reading recipient statistics: %v", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("stats.errorReading"))
		}
		camps, err := c.StatsCampaigns(from, to)
		if err != nil {
			return nil, err
		}
		teams := map[int][]string{}
		for _, ca := range camps {
			teams[ca.ID] = ca.TeamTags
		}
		return MergeRecipientGroups(rows, teams, by == "country"), nil
	})
	if err != nil {
		return nil, err
	}
	return d.([]models.StatsRecipientGroup), nil
}

// MergeRecipientGroups sums rows into groups per team ("" for everyone) and, when small is
// set, merges the groups smaller than models.StatsMinGroup into one "other" group.
func MergeRecipientGroups(rows []models.StatsRecipientRow, teams map[int][]string, small bool) []models.StatsRecipientGroup {
	type k struct{ team, key string }
	sum := map[k]*models.StatsRecipientGroup{}
	add := func(team string, r models.StatsRecipientRow) {
		g := sum[k{team, r.Key}]
		if g == nil {
			g = &models.StatsRecipientGroup{Team: team, Key: r.Key}
			sum[k{team, r.Key}] = g
		}
		g.Sent += r.Sent
		g.Opened += r.Opened
		g.Clicked += r.Clicked
		g.Bounced += r.Bounced
	}
	for _, r := range rows {
		add("", r)
		for _, t := range teams[r.CampaignID] {
			add(t, r)
		}
	}
	out := []models.StatsRecipientGroup{}
	other := map[string]*models.StatsRecipientGroup{}
	for key, g := range sum {
		if small && g.Sent < models.StatsMinGroup {
			o := other[key.team]
			if o == nil {
				o = &models.StatsRecipientGroup{Team: key.team, Key: models.StatsOtherKey}
				other[key.team] = o
			}
			o.Sent += g.Sent
			o.Opened += g.Opened
			o.Clicked += g.Clicked
			o.Bounced += g.Bounced
			continue
		}
		out = append(out, *g)
	}
	for _, o := range other {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Team != out[j].Team {
			return out[i].Team < out[j].Team
		}
		if out[i].Sent != out[j].Sent {
			return out[i].Sent > out[j].Sent
		}
		return out[i].Key < out[j].Key
	})
	return out
}
