package core

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
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
// The caller validates the rule and can pass an empty source set after removing deleted campaigns.
func (c *Core) RecentExcludeCounts(ctx context.Context, lists []int, rule models.RecentRule, selfID int) (models.RecentCounts, error) {
	var out models.RecentCounts
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

// GetRecentSourceCampaigns returns the type and ID of each source that still exists.
func (c *Core) GetRecentSourceCampaigns(ctx context.Context, ids pq.Int64Array) (models.Campaigns, error) {
	var sources models.Campaigns
	if err := c.q.AxmRecentSourceCamps.SelectContext(ctx, &sources, ids); err != nil {
		return nil, c.recentError(err)
	}
	return sources, nil
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

// recentListLockKey serializes the making of recent-exclusion lists (see MakeRecentExcludeList).
const recentListLockKey int64 = 746046

// RecentListOptions carries what the handler decides about the new list.
type RecentListOptions struct {
	Name         string
	ResolveTeams func(source []string) ([]string, error)
}

func (c *Core) recentBadRequest(key string) error {
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent."+key))
}

func (c *Core) recentListName(sources []models.RecentSourceList, days int, at time.Time) string {
	base := sources[0].Name
	if len(sources) > 1 {
		base = c.i18n.Ts("lists.recentExclude.moreLists", "name", base, "count", strconv.Itoa(len(sources)-1))
	}
	return listNameWithSuffix(base, c.i18n.Ts("lists.recentExclude.nameSuffix", "days", strconv.Itoa(days), "date", at.Format("2006-01-02")))
}

func (c *Core) describeRecentList(out models.RecentListResult, rule models.RecentRule) string {
	names := make([]string, len(out.SourceLists))
	for i, source := range out.SourceLists {
		names[i] = source.Name + " (#" + strconv.Itoa(source.ID) + ")"
	}
	scope := c.i18n.T("campaigns.recent.scope." + rule.Scope)
	if rule.Scope == models.RecentScopeCampaigns {
		ids := make([]string, len(rule.CampaignIDs))
		for i, id := range rule.CampaignIDs {
			ids[i] = "#" + strconv.FormatInt(id, 10)
		}
		scope += " " + strings.Join(ids, ", ")
	}
	return c.i18n.Ts("lists.recentExclude.description", "lists", strings.Join(names, ", "),
		"days", strconv.Itoa(rule.Days), "scope", scope, "time", out.AsOf.Format("2006-01-02 15:04 MST"),
		"target", groupThousands(out.Counts.Target), "excluded", groupThousands(out.Counts.Excluded),
		"added", groupThousands(out.Counts.Added))
}

// MakeRecentExcludeList counts the audience again and, in the same repeatable-read
// transaction, creates a private list holding everybody the rule does not exclude. The
// caller has validated the rule and checked access to every list. It does not touch any
// campaign: the campaign's target is changed by the normal campaign update.
func (c *Core) MakeRecentExcludeList(ctx context.Context, lists []int, rule models.RecentRule, selfID int, o RecentListOptions) (models.RecentListResult, error) {
	var out models.RecentListResult
	ids := make(pq.Int64Array, 0, len(lists))
	for _, id := range lists {
		if !slices.Contains(ids, int64(id)) {
			ids = append(ids, int64(id))
		}
	}
	if len(ids) == 0 {
		return out, c.recentBadRequest("selectLists")
	}

	// Names are made unique by looking at the committed lists, so two makers must not run at
	// once: the second one starts its snapshot only after the first one committed. The lock is
	// held on a connection of its own, before the transaction takes its snapshot.
	lock, err := c.db.Conn(ctx)
	if err != nil {
		return out, c.recentError(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, recentListLockKey); err != nil {
		return out, c.recentError(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, recentListLockKey)

	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, c.recentError(err)
	}
	defer tx.Rollback()

	if err := tx.Stmtx(c.q.AxmRecentSourceLists).SelectContext(ctx, &out.SourceLists, ids); err != nil {
		return out, c.recentError(err)
	}
	if len(out.SourceLists) != len(ids) {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("globals.messages.invalidID"))
	}
	tags := []string{}
	for _, source := range out.SourceLists {
		for _, tag := range models.TeamTags(source.Tags) {
			if !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}
	if rule.Scope == models.RecentScopeTeam && len(tags) == 0 {
		return out, c.recentBadRequest("noTeam")
	}
	teams, err := o.ResolveTeams(tags)
	if err != nil {
		return out, err
	}
	// A same-team rule counts the teams of the campaign's lists: a list without a team would
	// leave the rule with nothing to count.
	if rule.Scope == models.RecentScopeTeam && len(teams) == 0 {
		return out, c.recentBadRequest("noTeam")
	}

	var counts models.RecentCounts
	if err := tx.Stmtx(c.q.AxmRecentExcludeCounts).GetContext(ctx, &counts, ids, rule.Days, rule.Scope, rule.CampaignIDs, selfID); err != nil {
		return out, c.recentError(err)
	}
	out.AsOf = counts.AsOf
	out.Counts = models.RecentListCounts{Target: counts.Target, Excluded: counts.Excluded, Added: counts.Target - counts.Excluded}
	if out.Counts.Added <= 0 {
		return out, c.recentBadRequest("listEmpty")
	}

	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = c.recentListName(out.SourceLists, rule.Days, out.AsOf)
	}
	if name, err = c.uniqueDerivedListName(ctx, tx, name); err != nil {
		return out, c.recentError(err)
	}
	uu, err := uuid.NewV4()
	if err != nil {
		return out, c.recentError(err)
	}
	preset := out.SourceLists[0].Preset
	if preset == "" {
		preset = DefaultColumnPreset
	}
	out.List = models.List{
		Name: name, UUID: uu.String(), Type: models.ListTypePrivate, Optin: models.ListOptinSingle,
		Status: models.ListStatusActive, AxmPreset: preset, Tags: append(pq.StringArray{models.RecentListTag}, teams...),
		Description: c.describeRecentList(out, rule),
	}
	if err := tx.Stmtx(c.q.AxmDrawCreateList).GetContext(ctx, &out.List.Base, out.List.UUID, name, out.List.Tags, out.List.Description, preset); err != nil {
		return out, c.recentError(err)
	}
	result, err := tx.Stmtx(c.q.AxmRecentAddMembers).ExecContext(ctx, ids, rule.Days, rule.Scope, rule.CampaignIDs, selfID, out.List.ID)
	if err != nil {
		return out, c.recentError(err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return out, c.recentError(err)
	}
	if inserted != int64(out.Counts.Added) {
		return out, c.recentError(errors.New("recent-exclude list membership differs from the transaction count"))
	}
	out.List.SubscriberCount = int(inserted)
	out.List.SubscriberCounts = models.StringIntMap{"confirmed": int(inserted)}
	if err := tx.Commit(); err != nil {
		return out, c.recentError(err)
	}
	_ = c.refreshCache(matListSubStats, false)
	return out, nil
}
