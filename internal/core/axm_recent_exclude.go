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

	var campaign struct {
		Status, Type string
		Started      bool
	}
	if err := tx.Stmtx(c.q.AxmLockRecentCampaign).GetContext(ctx, &campaign, campaignID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, c.recentCampaignNotFound()
		}
		return out, c.recentError(err)
	}
	if campaign.Type != models.CampaignTypeRegular {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.regularOnly"))
	}
	if !models.CanEditRecentRule(campaign.Status, campaign.Started) {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	}
	// AXM (LST-65): a follow-up campaign goes to the people who got the original, so the rule would
	// leave out all of them (or race the follow-up's own judgement of each batch): it takes none.
	if rule.Enabled {
		var followup bool
		if err := tx.Stmtx(c.q.AxmFollowupIsFollowup).GetContext(ctx, &followup, campaignID); err != nil {
			return out, c.recentError(err)
		}
		if followup {
			return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.followupNone"))
		}
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
	// AXM (LST-65): the rule decides who a send reaches, so a send window opened before the change
	// must be told the campaign changed (the status change refuses a stale updated_at).
	if _, err := tx.Stmtx(c.q.AxmTouchRecentCampaign).ExecContext(ctx, campaignID); err != nil {
		return out, c.recentError(err)
	}
	if err := tx.Commit(); err != nil {
		return out, c.recentError(err)
	}
	return out, nil
}

// HoldRecentBatch (LST-65) returns which of the subscribers ids (a batch the sender has just read for
// campaign campID) the campaign's recent-send rule leaves out, and remembers them as left out of this
// campaign for good. It is empty for a campaign without a rule. An error means the batch must not be
// sent: the rule could not be applied.
func (c *Core) HoldRecentBatch(ctx context.Context, campID int, ids []int) (map[int]bool, error) {
	arr := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		arr[i] = int64(id)
	}
	var held []int64
	if err := c.q.AxmRecentHoldBatch.SelectContext(ctx, &held, campID, arr); err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(held))
	for _, id := range held {
		out[int(id)] = true
	}
	return out, nil
}

// RecentRuleOn checks whether a regular campaign has a saved rule that the sender must honor.
// Even if all chosen sources disappear, the saved rule still keeps previous holds in force.
func (c *Core) RecentRuleOn(campaignID int) (bool, error) {
	var on bool
	if err := c.q.AxmRecentHasRule.Get(&on, campaignID); err != nil {
		return false, c.recentError(err)
	}
	return on, nil
}

// RecentHeldCount is how many people the recent-send rule has left out of the campaign so far.
func (c *Core) RecentHeldCount(campaignID int) (int, error) {
	var n int
	if err := c.q.AxmRecentHeldCount.Get(&n, campaignID); err != nil {
		return 0, c.recentError(err)
	}
	return n, nil
}

// recentListLockKey serializes the making of recent-exclusion lists (see MakeRecentExcludeList).
const recentListLockKey int64 = 746046

// RecentListOptions carries what the handler decides about the new list.
type RecentListOptions struct {
	Name         string
	ResolveTeams func(source []string) ([]string, error)
	// UserID, when set, must be able to read every source list in the transaction's snapshot
	// (the caller checked the session's permissions, which can be older than the snapshot).
	UserID int
	// AfterTeams, when set, runs after the teams are decided and before the transaction (tests
	// change the source lists there).
	AfterTeams func()
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

	// The teams are resolved first, on the pool: resolving reads the database, and the request
	// must not ask the pool for a second connection while it holds one (db.max_open may be 1).
	// The transaction then checks that the source lists still have those teams; if a team was
	// changed in between, the whole decision is made again.
	for attempt := 1; ; attempt++ {
		out = models.RecentListResult{}
		if err := c.q.AxmRecentSourceLists.SelectContext(ctx, &out.SourceLists, ids); err != nil {
			return out, c.recentError(err)
		}
		if len(out.SourceLists) != len(ids) {
			return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("globals.messages.invalidID"))
		}
		tags := recentSourceTeams(out.SourceLists)
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
		if o.AfterTeams != nil {
			o.AfterTeams()
		}

		out, err = c.makeRecentExcludeList(ctx, ids, rule, selfID, o, tags, teams, out)
		if errors.Is(err, errRecentTeamsChanged) {
			if attempt < 3 {
				continue
			}
			return out, c.recentError(err)
		}
		if err != nil {
			return out, err
		}
		break
	}
	// The connection of the transaction is back in the pool here.
	_ = c.refreshCache(matListSubStats, false)
	return out, nil
}

// errRecentTeamsChanged: the source lists' teams changed between deciding the new list's teams
// and the transaction's snapshot.
var errRecentTeamsChanged = errors.New("the source lists' teams changed while the list was being made")

// recentSourceTeams is the union of the source lists' team tags, in order.
func recentSourceTeams(sources []models.RecentSourceList) []string {
	tags := []string{}
	for _, source := range sources {
		for _, tag := range models.TeamTags(source.Tags) {
			if !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}
	return tags
}

// makeRecentExcludeList does the work of MakeRecentExcludeList on one connection: the lock and
// the transaction share it, so the request never holds one connection while waiting for another.
func (c *Core) makeRecentExcludeList(ctx context.Context, ids pq.Int64Array, rule models.RecentRule, selfID int,
	o RecentListOptions, sourceTeams, teams []string, out models.RecentListResult) (models.RecentListResult, error) {
	// Names are made unique by looking at the committed lists, so two makers must not run at
	// once: the second one starts its snapshot only after the first one committed. The session
	// lock is taken before the transaction takes its snapshot.
	conn, err := c.db.Connx(ctx)
	if err != nil {
		return out, c.recentError(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, recentListLockKey); err != nil {
		return out, c.recentError(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, recentListLockKey)

	tx, err := conn.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, c.recentError(err)
	}
	defer tx.Rollback()

	// The lists are read again in the snapshot (a list deleted meanwhile is refused).
	out.SourceLists = nil
	if err := tx.Stmtx(c.q.AxmRecentSourceLists).SelectContext(ctx, &out.SourceLists, ids); err != nil {
		return out, c.recentError(err)
	}
	if len(out.SourceLists) != len(ids) {
		return out, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("globals.messages.invalidID"))
	}
	// The count below reads the teams of this snapshot (a same-team rule counts by them), and the
	// new list's teams were decided from the teams read before: they must be the same.
	if now := recentSourceTeams(out.SourceLists); len(now) != len(sourceTeams) ||
		slices.ContainsFunc(now, func(t string) bool { return !slices.Contains(sourceTeams, t) }) {
		return out, errRecentTeamsChanged
	}
	// A list moved to another team meanwhile can take the user's access with it: read access is
	// checked in the same snapshot the people are read from.
	if o.UserID != 0 {
		var readable []int
		if err := tx.Stmtx(c.q.AxmRecentReadableLists).SelectContext(ctx, &readable, o.UserID, ids); err != nil {
			return out, c.recentError(err)
		}
		if len(readable) != len(ids) {
			return out, echo.NewHTTPError(http.StatusForbidden, c.i18n.T("campaigns.recent.listForbidden"))
		}
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
	return out, nil
}
