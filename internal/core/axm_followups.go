package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// pgUniqueViolation is the Postgres error code for a duplicate key.
const pgUniqueViolation = "23505"

// FollowupDraft is the campaign the reaction list is made together with (LST-47). The
// caller has already validated it as a campaign copy; the list it sends to does not exist
// yet, so the list is filled in by MakeReactionList.
type FollowupDraft struct {
	Campaign models.Campaign
	MediaIDs []int
	UserID   int
}

// FollowupName is the default name of the follow-up draft of a campaign.
func (c *Core) FollowupName(parent string) string {
	name := c.i18n.Ts("campaigns.followup.defaultName", "name", parent)
	if utf8.RuneCountInString(name) <= 200 {
		return name
	}
	suffix := c.i18n.Ts("campaigns.followup.defaultName", "name", "")
	return truncateListName(parent, 200-utf8.RuneCountInString(suffix)) + suffix
}

// followupOffer says whether the follow-up draft can be made from this campaign and why not.
func (c *Core) followupOffer(ctx context.Context, tx *sqlx.Tx, camp models.ReactionCampaign, t models.ReactionTracking, canMake bool) (*models.ReactionFollowup, error) {
	out := &models.ReactionFollowup{WarnDays: models.FollowupWarnDays, DefaultName: c.FollowupName(camp.Name)}
	if t.LastSentAt != nil {
		days := int(time.Since(*t.LastSentAt).Hours() / 24)
		out.DaysSince = &days
	}
	var isFollowup bool
	if err := tx.Stmtx(c.q.AxmFollowupIsFollowup).GetContext(ctx, &isFollowup, camp.ID); err != nil {
		return nil, err
	}
	var existing models.FollowupRef
	err := tx.Stmtx(c.q.AxmFollowupExisting).GetContext(ctx, &existing, camp.ID)
	switch {
	case err == nil:
		out.Existing = &existing
		out.Reason = "exists"
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	case !canMake:
		out.Reason = "forbidden"
	case camp.Type != models.CampaignTypeRegular:
		out.Reason = "not_regular"
	case camp.Status != models.CampaignStatusFinished:
		out.Reason = "not_finished"
	case isFollowup:
		out.Reason = "is_followup"
	default:
		out.Available = true
	}
	return out, nil
}

func (c *Core) followupExistsError(ctx context.Context, parent int) error {
	var existing models.FollowupRef
	// A fresh connection: the transaction that hit the duplicate is already aborted.
	if err := c.q.AxmFollowupExisting.GetContext(ctx, &existing, parent); err != nil {
		return c.followupBadRequest(http.StatusConflict, "exists", "id", "0")
	}
	return c.followupBadRequest(http.StatusConflict, "exists", "id", strconv.Itoa(existing.ID))
}

func (c *Core) followupBadRequest(code int, key string, params ...string) error {
	if len(params) > 0 {
		return echo.NewHTTPError(code, c.i18n.Ts("campaigns.followup."+key, params...))
	}
	return echo.NewHTTPError(code, c.i18n.T("campaigns.followup."+key))
}

// followupRefusal says why no follow-up can be made from the campaign: it is not finished, not a
// regular campaign, a follow-up itself, or already has one (409 with its id). It is asked before
// the list is counted, so a retry after everyone reacted still points at the existing follow-up.
func (c *Core) followupRefusal(ctx context.Context, tx *sqlx.Tx, camp models.ReactionCampaign) error {
	if camp.Status != models.CampaignStatusFinished {
		return c.followupBadRequest(http.StatusBadRequest, "notFinished")
	}
	if camp.Type != models.CampaignTypeRegular {
		return c.followupBadRequest(http.StatusBadRequest, "notRegular")
	}
	var isFollowup bool
	if err := tx.Stmtx(c.q.AxmFollowupIsFollowup).GetContext(ctx, &isFollowup, camp.ID); err != nil {
		return err
	}
	if isFollowup {
		return c.followupBadRequest(http.StatusBadRequest, "isFollowup")
	}
	var existing models.FollowupRef
	if err := tx.Stmtx(c.q.AxmFollowupExisting).GetContext(ctx, &existing, camp.ID); err == nil {
		return c.followupBadRequest(http.StatusConflict, "exists", "id", strconv.Itoa(existing.ID))
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	return nil
}

// makeFollowupTx creates the draft campaign that sends to the new list and links it to the
// original, in the transaction that made the list. A second follow-up of the same campaign
// fails on the unique key and rolls the whole transaction back.
func (c *Core) makeFollowupTx(ctx context.Context, tx *sqlx.Tx, out *models.ReactionResult, s models.ReactionSettings, d *FollowupDraft) error {
	if err := c.followupRefusal(ctx, tx, out.Campaign); err != nil {
		return err
	}

	uu, err := uuid.NewV4()
	if err != nil {
		return err
	}
	o := d.Campaign
	var id int
	if err := tx.Stmtx(c.q.CreateCampaign).GetContext(ctx, &id,
		uu, o.Type, o.Name, o.Subject, o.FromEmail, o.Body, o.AltBody, o.ContentType, o.SendAt, o.Headers,
		o.Attribs, pq.StringArray(normalizeTags(o.Tags)), o.Messenger, o.TemplateID, pq.Array([]int{out.List.ID}),
		o.Archive, o.ArchiveSlug, o.ArchiveTemplateID, o.ArchiveMeta, pq.Array(d.MediaIDs), o.BodySource, o.ReplyTo,
	); err != nil {
		return err
	}

	cond, err := json.Marshal(s)
	if err != nil {
		return err
	}
	counts, err := json.Marshal(struct {
		Received int `json:"received"`
		models.ReactionCounts
	}{out.Tracking.Sends, out.Counts})
	if err != nil {
		return err
	}
	var by any
	if d.UserID > 0 {
		by = d.UserID
	}
	if _, err := tx.Stmtx(c.q.AxmFollowupInsert).ExecContext(ctx, id, out.Campaign.ID, out.List.UUID, cond, counts, by); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return c.followupExistsError(ctx, out.Campaign.ID)
		}
		return err
	}
	out.Followup = &models.FollowupRef{ID: id, Name: o.Name, Status: models.CampaignStatusDraft}
	return nil
}

type followupDue struct {
	CampaignID int             `db:"campaign_id"`
	ParentID   int             `db:"parent_campaign_id"`
	Condition  json.RawMessage `db:"condition"`
	ListIDs    pq.Int64Array   `db:"list_ids"`
	Version    int             `db:"version"`
}

// RecheckFollowups runs the check before follow-up campaigns start: the people on a follow-up
// list who reacted to the original campaign since the list was made, opted out of it, or got
// blocked are held back, and the sender skips them. Subscriptions are not changed, and who is held
// is decided again at every check. This covers the follow-ups the sender is about to pick up (a
// start, a due schedule, a resume, a restart); it does nothing when there are none. It returns
// the campaigns it checked. An error means the check did not happen, and the caller must not
// start those campaigns.
func (c *Core) RecheckFollowups(ctx context.Context, currentIDs []int64) (checked, skipped []int, err error) {
	var due []followupDue
	if err := c.q.AxmFollowupDue.SelectContext(ctx, &due, pq.Array(currentIDs)); err != nil {
		return nil, nil, err
	}
	return c.recheck(ctx, due)
}

// FollowupsToCheck returns the follow-ups among the given campaigns whose last check was not made
// for the campaign as it is now. The sender gives none of them back before they are checked.
func (c *Core) FollowupsToCheck(ctx context.Context, ids []int) ([]int, error) {
	out := []int{}
	if err := c.q.AxmFollowupUnchecked.SelectContext(ctx, &out, pq.Array(ids)); err != nil {
		return nil, err
	}
	return out, nil
}

// FollowupStale says whether the campaign is a follow-up whose last check is not for the campaign
// as it is now; the sender must read no recipients for it then.
func (c *Core) FollowupStale(id int) (bool, error) {
	var stale bool
	err := c.q.AxmFollowupStale.Get(&stale, id)
	return stale, err
}

// RecheckFollowupsByID checks the follow-ups among the given campaigns, whatever their state.
// The sender uses it for a campaign it found after the first check (a schedule that fell due
// while another check ran). It returns the follow-ups it checked and the follow-ups it skipped
// because they were not due when it came to them: those are not checked and must not start.
func (c *Core) RecheckFollowupsByID(ctx context.Context, ids []int) (checked, skipped []int, err error) {
	var rows []followupDue
	if err := c.q.AxmFollowupByIDs.SelectContext(ctx, &rows, pq.Array(ids)); err != nil {
		return nil, nil, err
	}
	return c.recheck(ctx, rows)
}

// recheck checks each follow-up. A follow-up that was not due any more (a later time was saved
// meanwhile) is skipped, not checked: it can fall due again before the sender's query runs, and
// then it must be checked again first.
func (c *Core) recheck(ctx context.Context, rows []followupDue) (done, skipped []int, err error) {
	done = []int{}
	for _, f := range rows {
		var s models.ReactionSettings
		if err := json.Unmarshal(f.Condition, &s); err != nil || s.Validate() != nil {
			return done, skipped, errors.New("follow-up campaign " + strconv.Itoa(f.CampaignID) + " has no valid condition")
		}
		held, checked, err := c.recheckOne(ctx, f, s)
		if err != nil {
			return done, skipped, err
		}
		if !checked {
			skipped = append(skipped, f.CampaignID)
			continue
		}
		if len(f.ListIDs) > 0 {
			c.log.Printf("follow-up campaign %d: checked against campaign %d before sending, %d people held back",
				f.CampaignID, f.ParentID, held)
		}
		done = append(done, f.CampaignID)
	}
	return done, skipped, nil
}

// recheckOne decides who is held back from one follow-up campaign, in one transaction. checked is
// false when the campaign was no longer due (its schedule was moved): nothing changes then.
func (c *Core) recheckOne(ctx context.Context, f followupDue, s models.ReactionSettings) (held int, checked bool, err error) {
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	if _, err := tx.Stmtx(c.q.AxmFollowupUnhold).ExecContext(ctx, f.CampaignID); err != nil {
		return 0, false, err
	}
	if len(f.ListIDs) > 0 {
		var reasons []int
		args := []any{f.ParentID, s.Open, s.Click, pq.Array([]int(nil)), true, f.ListIDs, f.CampaignID}
		if err := tx.Stmtx(c.q.AxmFollowupRecheck).SelectContext(ctx, &reasons, args...); err != nil {
			return 0, false, err
		}
		held = len(reasons)
	}
	// Then, with the campaign locked: if it changed since its row was read (a later time saved, its
	// lists changed, a pause), the check was made for a campaign that no longer is and is dropped;
	// the sender checks it again (FollowupsToCheck). A save that comes after this lock waits for the
	// check; core.UpdateCampaign then reads the status again and refuses the save if the campaign
	// has started meanwhile. The campaign row is locked before the link row is written, the order a
	// save takes them in (its trigger axm_followup_touch writes the link row), so the two never
	// wait for each other.
	var locked, version int
	if err := tx.Stmtx(c.q.AxmFollowupLockDue).GetContext(ctx, &locked, f.CampaignID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if err := tx.Stmtx(c.q.AxmFollowupVersion).GetContext(ctx, &version, f.CampaignID); err != nil {
		return 0, false, err
	}
	if version != f.Version {
		return 0, false, nil
	}
	if _, err := tx.Stmtx(c.q.AxmFollowupRecord).ExecContext(ctx, f.CampaignID, f.Version); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return held, true, nil
}

// GetFollowupInfo describes the follow-up link of a campaign, if it has one. withPending adds
// how many people a check made now would take off a follow-up list that has not started.
func (c *Core) GetFollowupInfo(ctx context.Context, id int, withPending bool) (models.FollowupInfo, error) {
	out := models.FollowupInfo{Role: "none"}
	var row models.FollowupRow
	if err := c.q.AxmFollowupGet.GetContext(ctx, &row, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	out.Role = row.Role
	out.Parent = &models.FollowupRef{ID: row.ParentCampaignID, Name: row.ParentName, Status: row.ParentStatus}
	out.Followup = &models.FollowupRef{ID: row.CampaignID, Name: row.FollowupName, Status: row.FollowupStatus}
	var s models.ReactionSettings
	if json.Unmarshal(row.Condition, &s) == nil {
		out.Condition = &s
	}
	out.Counts = row.Counts
	var rc models.FollowupRecheck
	if json.Unmarshal(row.Recheck, &rc) == nil && rc.Runs > 0 {
		out.Recheck = &rc
		if row.RecheckedAt.Valid {
			out.RecheckedAt = &row.RecheckedAt.Time
		}
	}
	out.CreatedAt = &row.CreatedAt
	if row.ParentSendAt.Valid {
		out.ParentSendAt = &row.ParentSendAt.Time
	}
	if row.FollowupSendAt.Valid {
		out.FollowupSendAt = &row.FollowupSendAt.Time
	}
	if row.ParentLastSentAt.Valid {
		out.ParentLastSentAt = &row.ParentLastSentAt.Time
		days := int(time.Since(row.ParentLastSentAt.Time).Hours() / 24)
		out.DaysSince = &days
	}
	if row.ListID.Valid && row.ListName.Valid {
		out.List = &models.FollowupList{ID: int(row.ListID.Int), Name: row.ListName.String, Count: row.ListCount}
	}
	// The list was the only target when the draft was made. Any other set of lists means
	// someone changed it, and the check before sending only covers the follow-up list.
	out.ListsChanged = !row.HasList || row.CampaignLists != 1
	if row.Role == "followup" && withPending && out.Condition != nil && len(row.ListIDs) > 0 &&
		(row.FollowupStatus == models.CampaignStatusDraft || row.FollowupStatus == models.CampaignStatusScheduled) {
		var p models.FollowupPending
		args := []any{row.ParentCampaignID, out.Condition.Open, out.Condition.Click, pq.Array([]int(nil)), true, row.ListIDs, row.CampaignID}
		if err := c.q.AxmFollowupPending.GetContext(ctx, &p, args...); err != nil {
			return out, err
		}
		out.Pending = &p
	}
	return out, nil
}
