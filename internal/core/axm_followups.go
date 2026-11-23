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
	if _, err := tx.Stmtx(c.q.AxmFollowupMembers).ExecContext(ctx, id, out.List.ID, out.Campaign.ID); err != nil {
		return err
	}
	if _, err := tx.Stmtx(c.q.AxmFollowupReactionsNow).ExecContext(ctx, id, out.Campaign.ID); err != nil {
		return err
	}
	// Last before the commit: wait for the reactions to the original being recorded now, so the
	// copy made right after the commit keeps them (see axm_followup_reaction).
	if _, err := tx.Stmtx(c.q.AxmFollowupLockOriginal).ExecContext(ctx, out.Campaign.ID); err != nil {
		return err
	}
	// Made while opens and clicks are not recorded per subscriber: that time is noted (it is
	// otherwise noted when the app starts with tracking off).
	if !c.tracking() {
		if _, err := tx.Stmtx(c.q.AxmTrackingOffStart).ExecContext(ctx); err != nil {
			return err
		}
	}
	out.Followup = &models.FollowupRef{ID: id, Name: o.Name, Status: models.CampaignStatusDraft}
	return nil
}

type followupDue struct {
	CampaignID int             `db:"campaign_id"`
	ParentID   int             `db:"parent_campaign_id"`
	Name       string          `db:"name"`
	ParentName string          `db:"parent_name"`
	Condition  json.RawMessage `db:"condition"`
	ListIDs    pq.Int64Array   `db:"list_ids"`
}

// ClaimFollowupBatch decides, just before the sender uses a batch it has read for follow-up
// campaign id, who of the people subs is left out, and returns them (isFollowup is false for any
// other campaign: nothing is decided). The decision is one transaction: it first takes the
// original's lock exclusively, so the reactions and send records of the original being written at
// this moment are committed and seen; then it keeps the batch's late recipients of the original,
// judges the batch (axm_followup_judge) and records who is left out and why (and that the others
// are not). Whatever is committed before it counts; what comes after is after the send was decided.
func (c *Core) ClaimFollowupBatch(ctx context.Context, id int, subs []int) (left map[int]bool, isFollowup bool, err error) {
	if len(subs) == 0 {
		return nil, false, nil
	}
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var g struct {
		Parent int    `db:"parent_campaign_id"`
		Locked string `db:"locked"`
	}
	if err := tx.Stmtx(c.q.AxmFollowupClaimLock).GetContext(ctx, &g, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if _, err := tx.Stmtx(c.q.AxmFollowupClaimRecipients).ExecContext(ctx, id, g.Parent, pq.Array(subs)); err != nil {
		return nil, true, err
	}
	var rows []struct {
		SubscriberID int  `db:"subscriber_id"`
		Reason       int  `db:"reason"`
		Inserted     bool `db:"inserted"`
	}
	if err := tx.Stmtx(c.q.AxmFollowupClaim).SelectContext(ctx, &rows, id, pq.Array(subs), c.tracking()); err != nil {
		return nil, true, err
	}
	// Who may be sent to is who has a verdict of 0: a person of the batch with no verdict (deleted
	// meanwhile) is left out too.
	send := make(map[int]bool, len(rows))
	reasons := map[int]int{}
	newly := 0
	for _, r := range rows {
		if r.Reason == 0 {
			send[r.SubscriberID] = true
			continue
		}
		if r.Inserted {
			newly++
			reasons[r.Reason]++
		}
	}
	left = make(map[int]bool, len(subs))
	for _, sid := range subs {
		if !send[sid] {
			left[sid] = true
		}
	}
	if _, err := tx.Stmtx(c.q.AxmFollowupToSend).ExecContext(ctx, id, newly); err != nil {
		return nil, true, err
	}
	var recorded json.RawMessage
	if err := tx.Stmtx(c.q.AxmFollowupRecord).GetContext(ctx, &recorded, id); err != nil {
		return nil, true, err
	}
	// The audit has each person left out once (a batch read again does not count them again).
	if newly > 0 {
		if err := c.auditFollowupLeftOut(ctx, tx, id, g.Parent, reasons, newly, recorded); err != nil {
			return nil, true, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, true, err
	}
	if newly > 0 {
		c.log.Printf("follow-up campaign %d: %d people of a batch of %d newly left out (reacted to the original, opted out, blocked, reactions unseen or no longer subscribed)", id, newly, len(subs))
	}
	return left, true, nil
}

// auditFollowupLeftOut writes the audit row of a batch in which the sender left people out of a
// follow-up, in the claim's transaction: counts only, no person.
func (c *Core) auditFollowupLeftOut(ctx context.Context, tx *sqlx.Tx, id, parent int, reasons map[int]int, left int, recorded json.RawMessage) error {
	var rc models.FollowupRecheck
	_ = json.Unmarshal(recorded, &rc)
	var names struct {
		Name       string `db:"name"`
		ParentName string `db:"parent_name"`
	}
	if err := tx.GetContext(ctx, &names, `SELECT c.name, p.name AS parent_name FROM campaigns c, campaigns p WHERE c.id = $1 AND p.id = $2`, id, parent); err != nil {
		return err
	}
	// Campaign names can hold anything, an address too: scrubbed as every audit text is.
	detail, err := json.Marshal(ScrubAuditDetail(map[string]any{
		"parent":       map[string]any{"id": parent, "name": names.ParentName},
		"reacted":      reasons[-1],
		"opted_out":    reasons[2] + reasons[3],
		"blocked":      reasons[1],
		"unknown":      reasons[4],
		"unsubscribed": reasons[5],
		"check":        rc.Runs,
	}))
	if err != nil {
		return err
	}
	name := ScrubAuditText(names.Name)
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	_, err = tx.Stmtx(c.q.AxmInsertAudit).ExecContext(ctx, nil, "system", "system", models.AuditUserTypeSystem, models.AuditActionFollowupCheck,
		"campaign", id, name, left, string(detail))
	return err
}

// FinishEmptyFollowup ends a follow-up campaign that is due with nobody at all on its lists (the
// sender does not start it). It reports whether it did.
func (c *Core) FinishEmptyFollowup(id int) (bool, error) {
	var ended int
	if err := c.q.AxmFollowupFinishEmpty.Get(&ended, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	c.log.Printf("follow-up campaign %d: nobody is on its lists at its send time, finished without sending", id)
	return true, nil
}

// FollowupHeldNow is who a check made now would hold back from campaign id, if it is a follow-up
// with its list (nil otherwise), and the id of that list. The pre-send confirmation leaves them
// out of its count.
func (c *Core) FollowupHeldNow(ctx context.Context, id int) (*models.FollowupPending, int, error) {
	var rows []followupDue
	if err := c.q.AxmFollowupByIDs.SelectContext(ctx, &rows, pq.Array([]int{id})); err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 || len(rows[0].ListIDs) == 0 {
		return nil, 0, nil
	}
	f := rows[0]
	var s models.ReactionSettings
	if err := json.Unmarshal(f.Condition, &s); err != nil || s.Validate() != nil {
		return nil, 0, errors.New("follow-up campaign " + strconv.Itoa(id) + " has no valid condition")
	}
	var p models.FollowupPending
	if err := c.q.AxmFollowupPending.GetContext(ctx, &p, f.CampaignID, c.tracking()); err != nil {
		return nil, 0, err
	}
	return &p, int(f.ListIDs[0]), nil
}

// SetIndividualTracking gives the core the live setting of individual tracking.
func (c *Core) SetIndividualTracking(f func() bool) { c.individualTracking = f }

func (c *Core) tracking() bool { return c.individualTracking != nil && c.individualTracking() }

// NoteTracking records, when the app starts, whether opens and clicks are recorded per subscriber
// from now on: a time without it begins (off) or ends (on). Follow-ups of campaigns that went out
// before such a time ended cannot judge "did not open/click" (LST-47).
func (c *Core) NoteTracking(on bool) error {
	q := c.q.AxmTrackingOffStart
	if on {
		q = c.q.AxmTrackingOffEnd
	}
	_, err := q.Exec()
	return err
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
		args := []any{row.CampaignID, c.tracking()}
		if err := c.q.AxmFollowupPending.GetContext(ctx, &p, args...); err != nil {
			return out, err
		}
		out.Pending = &p
	}
	return out, nil
}
