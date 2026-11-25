package core

import (
	"net/http"
	"slices"
	"strconv"
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
	d, err := statsCached("c|"+from.UTC().Format(time.RFC3339Nano)+"|"+to.UTC().Format(time.RFC3339Nano), func() (any, error) {
		out := []models.StatsCampaign{}
		if err := c.q.AxmStatsCampaigns.Select(&out, from, to, models.TeamTagPrefix, models.StatsSourceTagPrefix); err != nil {
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

// StatsOverlap is loaded separately from the campaign results to keep them responsive.
func (c *Core) StatsOverlap(from, to time.Time, id int) ([]models.StatsOverlap, error) {
	key := "o|" + from.UTC().Format(time.RFC3339Nano) + "|" + to.UTC().Format(time.RFC3339Nano) + "|" + strconv.Itoa(id)
	d, err := statsCached(key, func() (any, error) {
		out := []models.StatsOverlap{}
		if err := c.q.AxmStatsOverlap.Select(&out, from, to, id); err != nil {
			c.log.Printf("error reading send overlap: %v", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("stats.errorReading"))
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return d.([]models.StatsOverlap), nil
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
	d, err := statsCached("r|"+by+"|"+from.UTC().Format(time.RFC3339Nano)+"|"+to.UTC().Format(time.RFC3339Nano), func() (any, error) {
		groups := []models.StatsRecipientGroup{}
		if err := c.q.AxmStatsRecipients.Select(&groups, from, to, by, models.TeamTagPrefix,
			models.StatsMinGroup, models.StatsNoTeamKey, models.StatsOtherKey); err != nil {
			c.log.Printf("error reading recipient statistics: %v", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("stats.errorReading"))
		}
		return groups, nil
	})
	if err != nil {
		return nil, err
	}
	return d.([]models.StatsRecipientGroup), nil
}
