package core

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"slices"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// ValidateRecentRule checks settings shared by preview and save.
func (c *Core) ValidateRecentRule(rule *models.RecentRule) error {
	if !rule.Enabled {
		*rule = models.RecentRule{}
		return nil
	}
	if !slices.Contains(models.RecentDays[:], rule.Days) {
		return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.invalidDays"))
	}
	switch rule.Scope {
	case models.RecentScopeAll, models.RecentScopeTeam:
		rule.CampaignIDs = pq.Int64Array{}
	case models.RecentScopeCampaigns:
		if len(rule.CampaignIDs) < 1 || len(rule.CampaignIDs) > models.RecentMaxCampaigns {
			return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.invalidCampaigns"))
		}
		for _, id := range rule.CampaignIDs {
			if id <= 0 || id > math.MaxInt32 {
				return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.invalidCampaigns"))
			}
		}
		ids := slices.Clone(rule.CampaignIDs)
		slices.Sort(ids)
		rule.CampaignIDs = slices.Compact(ids)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.invalidScope"))
	}
	return nil
}

func (c *Core) recentTeamTags(ctx context.Context, tx *sqlx.Tx, lists pq.Int64Array) ([]string, error) {
	tags := pq.StringArray{}
	if err := tx.Stmtx(c.q.AxmRecentTeamTags).SelectContext(ctx, &tags, lists); err != nil {
		return nil, c.recentError(err)
	}
	return tags, nil
}

func (c *Core) recentError(err error) error {
	c.log.Printf("error handling a recent-send exclusion rule: %v", err)
	return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("campaigns.recent.error")).SetInternal(err)
}

// RecentExcludeCounts reads the audience, history coverage and team tags at one snapshot.
func (c *Core) RecentExcludeCounts(ctx context.Context, lists []int, rule models.RecentRule, selfID int) (models.RecentCounts, error) {
	var out models.RecentCounts
	rule.Enabled = true
	if err := c.ValidateRecentRule(&rule); err != nil {
		return out, err
	}
	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, c.recentError(err)
	}
	defer tx.Rollback()

	listIDs := make(pq.Int64Array, len(lists))
	for i, id := range lists {
		listIDs[i] = int64(id)
	}
	out.TeamTags, err = c.recentTeamTags(ctx, tx, listIDs)
	if err != nil {
		return out, err
	}
	if rule.Scope == models.RecentScopeTeam && len(out.TeamTags) == 0 {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.noTeam"))
	}
	if err := tx.Stmtx(c.q.AxmRecentExcludeCounts).GetContext(ctx, &out,
		listIDs, rule.Days, rule.Scope, rule.CampaignIDs, selfID); err != nil {
		return out, c.recentError(err)
	}
	if err := tx.Stmtx(c.q.AxmRecentSendsCoverage).GetContext(ctx, &out.RecordsFrom); err != nil {
		return out, c.recentError(err)
	}
	out.WillSend = out.Target - out.Excluded
	return out, nil
}

// GetRecentCampaign loads only the metadata needed by the preview and rule endpoints.
func (c *Core) GetRecentCampaign(id int) (models.Campaign, error) {
	var campaign models.Campaign
	if err := c.q.AxmGetRecentCampaign.Get(&campaign, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return campaign, c.recentCampaignNotFound()
		}
		return campaign, c.recentError(err)
	}
	return campaign, nil
}

func (c *Core) recentCampaignNotFound() error {
	return echo.NewHTTPError(http.StatusBadRequest,
		c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
}

func (c *Core) GetRecentRule(campaignID int) (models.RecentRule, error) {
	var rule models.RecentRule
	if err := c.q.AxmGetRecentRule.Get(&rule, campaignID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.RecentRule{}, nil
		}
		return models.RecentRule{}, c.recentError(err)
	}
	return rule, nil
}

// SaveRecentRule locks the campaign so its status cannot change between validation and save.
func (c *Core) SaveRecentRule(ctx context.Context, campaignID int, rule models.RecentRule, userID int) (models.RecentRule, error) {
	var out models.RecentRule
	if err := c.ValidateRecentRule(&rule); err != nil {
		return out, err
	}
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return out, c.recentError(err)
	}
	defer tx.Rollback()

	var campaign struct{ Status, Type string }
	if err := tx.Stmtx(c.q.AxmLockRecentCampaign).GetContext(ctx, &campaign, campaignID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, c.recentCampaignNotFound()
		}
		return out, c.recentError(err)
	}
	if campaign.Type != models.CampaignTypeRegular {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.regularOnly"))
	}
	if !models.CanEditCampaign(campaign.Status) {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	}
	if rule.Enabled && rule.Scope == models.RecentScopeTeam {
		var listIDs []int64
		if err := tx.Stmtx(c.q.AxmRecentCampaignLists).SelectContext(ctx, &listIDs, campaignID); err != nil {
			return out, c.recentError(err)
		}
		tags, err := c.recentTeamTags(ctx, tx, pq.Int64Array(listIDs))
		if err != nil {
			return out, err
		}
		if len(tags) == 0 {
			return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.noTeam"))
		}
	}

	if rule.Enabled {
		err = tx.Stmtx(c.q.AxmUpsertRecentRule).GetContext(ctx, &out,
			campaignID, rule.Days, rule.Scope, rule.CampaignIDs, userID)
	} else {
		_, err = tx.Stmtx(c.q.AxmDeleteRecentRule).ExecContext(ctx, campaignID)
	}
	if err != nil {
		return out, c.recentError(err)
	}
	if err := tx.Commit(); err != nil {
		return out, c.recentError(err)
	}
	return out, nil
}
