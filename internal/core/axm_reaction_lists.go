package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

type ReactionScope struct {
	AllSubscribers     bool
	PermittedLists     []int // nil means all lists are readable, empty means none
	IndividualTracking bool
	// CanFollowup is set when the user may also make a campaign (LST-47).
	CanFollowup bool
}

type ReactionListOptions struct {
	Name         string
	Scope        ReactionScope
	ResolveTeams func(source []string) ([]string, error)
	// Followup, when set, makes the draft campaign for the new list in the same transaction.
	Followup *FollowupDraft
}

type reactionSource struct {
	Tags   pq.StringArray `db:"tags"`
	Preset string         `db:"axm_preset"`
}

func (c *Core) reactionBadRequest(key string) error {
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.reactionList."+key))
}

func (c *Core) reactionTracking(ctx context.Context, tx *sqlx.Tx, id int, individual bool) (models.ReactionCampaign, models.ReactionTracking, error) {
	var row struct {
		models.ReactionCampaign
		models.ReactionTracking
	}
	if err := tx.Stmtx(c.q.AxmReactionTracking).GetContext(ctx, &row, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = c.reactionBadRequest("unavailable")
		}
		return row.ReactionCampaign, row.ReactionTracking, err
	}
	switch row.Status {
	case models.CampaignStatusFinished, models.CampaignStatusRunning, models.CampaignStatusPaused, models.CampaignStatusCancelled:
	default:
		return row.ReactionCampaign, row.ReactionTracking, c.reactionBadRequest("unavailable")
	}
	row.IndividualTracking = individual
	return row.ReactionCampaign, row.ReactionTracking, nil
}

func (c *Core) reactionSources(ctx context.Context, tx *sqlx.Tx, id int) ([]string, string, error) {
	var sources []reactionSource
	if err := tx.Stmtx(c.q.AxmReactionCampaignLists).SelectContext(ctx, &sources, id); err != nil {
		return nil, "", err
	}
	preset := DefaultColumnPreset
	if len(sources) > 0 {
		preset = sources[0].Preset
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		for _, tag := range models.TeamTags(source.Tags) {
			if !seen[tag] {
				tags = append(tags, tag)
				seen[tag] = true
			}
		}
	}
	return tags, preset, nil
}

func (c *Core) checkReactionTracking(t models.ReactionTracking, s models.ReactionSettings) error {
	switch {
	case t.Sends == 0:
		return c.reactionBadRequest("noSends")
	case s.Open != models.ReactionAny && (!t.IndividualTracking || t.Viewers == 0):
		return c.reactionBadRequest("noOpenHelp")
	case s.Click != models.ReactionAny && (!t.IndividualTracking || t.Clickers == 0):
		return c.reactionBadRequest("noClickHelp")
	}
	return nil
}

func reactionArgs(id int, s models.ReactionSettings, scope ReactionScope) []any {
	return []any{id, s.Open, s.Click, pq.Array(scope.PermittedLists), scope.AllSubscribers}
}

// PreviewReactionList reads the campaign's tracking state and, on request, counts and
// masks matching recipients under one snapshot of the current data.
func (c *Core) PreviewReactionList(ctx context.Context, id int, s models.ReactionSettings, scope ReactionScope, trackingOnly bool) (models.ReactionPreview, error) {
	var out models.ReactionPreview
	if err := s.Validate(); err != nil {
		return out, c.reactionBadRequest("invalidCondition")
	}
	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.CheckedAt = time.Now()
	out.Campaign, out.Tracking, err = c.reactionTracking(ctx, tx, id, scope.IndividualTracking)
	if err != nil {
		return out, err
	}
	out.SourceTeams, _, err = c.reactionSources(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if out.Followup, err = c.followupOffer(ctx, tx, out.Campaign, out.Tracking, scope.CanFollowup); err != nil {
		return out, err
	}
	out.Sample = []models.ReactionSample{}
	if trackingOnly {
		return out, nil
	}
	if err := c.checkReactionTracking(out.Tracking, s); err != nil {
		return out, err
	}

	args := reactionArgs(id, s, scope)
	out.Counts = &models.ReactionCounts{}
	if err := tx.Stmtx(c.q.AxmReactionCounts).GetContext(ctx, out.Counts, args...); err != nil {
		return out, err
	}
	if out.Counts.Eligible > 0 {
		if err := tx.Stmtx(c.q.AxmReactionSample).SelectContext(ctx, &out.Sample, args...); err != nil {
			return out, err
		}
		for i, sample := range out.Sample {
			out.Sample[i] = models.ReactionSample{Name: maskName(sample.Name), Email: maskEmail(sample.Email)}
		}
		out.DefaultName = c.reactionDefaultName(out.Campaign.Name, s, out.CheckedAt, out.Counts.Eligible)
	}
	return out, nil
}

func (c *Core) reactionSummary(s models.ReactionSettings) string {
	var labels []string
	if s.Open != models.ReactionAny {
		labels = append(labels, c.i18n.T("campaigns.reactionList.open."+s.Open))
	}
	if s.Click != models.ReactionAny {
		labels = append(labels, c.i18n.T("campaigns.reactionList.click."+s.Click))
	}
	if len(labels) == 0 {
		return c.i18n.T("campaigns.reactionList.received")
	}
	return strings.Join(labels, "·")
}

func (c *Core) reactionDefaultName(campaign string, s models.ReactionSettings, at time.Time, n int) string {
	suffix := c.i18n.Ts("lists.reaction.nameSuffix", "conditions", c.reactionSummary(s),
		"date", at.Format("2006-01-02"), "count", groupThousands(n))
	return listNameWithSuffix(campaign, suffix)
}

func (c *Core) describeReaction(out models.ReactionResult, s models.ReactionSettings) string {
	n := out.Counts
	return c.i18n.Ts("lists.reaction.description",
		"id", strconv.Itoa(out.Campaign.ID), "conditions", c.reactionSummary(s),
		"time", out.CheckedAt.Format("2006-01-02 15:04 MST"), "sends", groupThousands(out.Tracking.Sends),
		"matched", groupThousands(n.Matched), "blocked", groupThousands(n.Blocklisted),
		"campaignOptOut", groupThousands(n.CampaignOptOut), "allOptOut", groupThousands(n.AllOptOut),
		"outOfScope", groupThousands(n.OutOfScope), "eligible", groupThousands(n.Eligible), "name", out.Campaign.Name)
}

// MakeReactionList rechecks reactions and eligibility, then creates and fills a private
// list in the same transaction. The repeatable snapshot keeps the description and insert
// aligned even when the campaign is still sending.
func (c *Core) MakeReactionList(ctx context.Context, id int, s models.ReactionSettings, o ReactionListOptions) (models.ReactionResult, error) {
	var out models.ReactionResult
	if err := s.Validate(); err != nil {
		return out, c.reactionBadRequest("invalidCondition")
	}
	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.CheckedAt = time.Now()
	out.Campaign, out.Tracking, err = c.reactionTracking(ctx, tx, id, o.Scope.IndividualTracking)
	if err != nil {
		return out, err
	}
	if err := c.checkReactionTracking(out.Tracking, s); err != nil {
		return out, err
	}
	sourceTeams, preset, err := c.reactionSources(ctx, tx, id)
	if err != nil {
		return out, err
	}
	teams, err := o.ResolveTeams(sourceTeams)
	if err != nil {
		return out, err
	}

	// AXM (LST-47): a follow-up that cannot be made is refused before the count, so the answer
	// to a second request is the existing follow-up even when nobody is left to send to.
	if o.Followup != nil {
		if err := c.followupRefusal(ctx, tx, out.Campaign); err != nil {
			return out, err
		}
	}
	args := reactionArgs(id, s, o.Scope)
	if err := tx.Stmtx(c.q.AxmReactionCounts).GetContext(ctx, &out.Counts, args...); err != nil {
		return out, err
	}
	if out.Counts.Eligible == 0 {
		return out, c.reactionBadRequest("empty")
	}
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = c.reactionDefaultName(out.Campaign.Name, s, out.CheckedAt, out.Counts.Eligible)
	}
	name, err = c.uniqueDerivedListName(ctx, tx, name)
	if err != nil {
		return out, err
	}
	uu, err := uuid.NewV4()
	if err != nil {
		return out, err
	}
	out.List = models.List{
		Name: name, UUID: uu.String(), Type: models.ListTypePrivate, Optin: models.ListOptinSingle,
		Status: models.ListStatusActive, AxmPreset: preset, Tags: append(pq.StringArray{models.ReactionListTag}, teams...),
		Description: c.describeReaction(out, s),
	}
	if err := tx.Stmtx(c.q.AxmDrawCreateList).GetContext(ctx, &out.List.Base, out.List.UUID, name, out.List.Tags, out.List.Description, preset); err != nil {
		return out, err
	}
	result, err := tx.Stmtx(c.q.AxmReactionAddMembers).ExecContext(ctx, append(args, out.List.ID)...)
	if err != nil {
		return out, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if inserted == 0 {
		return out, c.reactionBadRequest("empty")
	}
	if inserted != int64(out.Counts.Eligible) {
		return out, errors.New("reaction list membership differs from the transaction count")
	}
	out.Counts.Eligible = int(inserted)
	out.List.SubscriberCount = int(inserted)
	out.List.SubscriberCounts = models.StringIntMap{"confirmed": int(inserted)}
	if o.Followup != nil {
		if err := c.makeFollowupTx(ctx, tx, &out, s, o.Followup); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	// AXM (LST-47): a reaction recorded while the transaction ran did not see the new follow-up
	// (its trigger looked before the commit). Keep what is on record now as well.
	if out.Followup != nil {
		if _, err := c.q.AxmFollowupReactionsNow.ExecContext(ctx, out.Followup.ID, out.Campaign.ID); err != nil {
			c.log.Printf("error keeping the reactions for follow-up campaign %d: %v", out.Followup.ID, err)
		}
	}
	_ = c.refreshCache(matListSubStats, false)
	return out, nil
}

func maskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 1 || at+1 == len(email) {
		return "***"
	}
	local := []rune(email[:at])
	domain := email[at+1:]
	first, _ := utf8.DecodeRuneInString(domain)
	var suffix string
	if dot := strings.LastIndexByte(domain, '.'); dot > 0 && dot+1 < len(domain) {
		suffix = domain[dot:]
	}
	return string(local[:min(2, len(local))]) + "***@" + string(first) + "***" + suffix
}

func maskName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(name)
	return string(first) + "*"
}
