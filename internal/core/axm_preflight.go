package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"gopkg.in/volatiletech/null.v6"
)

// GetPreflightAudience counts, for the pre-send confirmation (LST-48), who campaign id
// would reach: each list alone and all lists together with every person counted once.
// The audience and ledger counts use one SQL snapshot. afterID is the sending checkpoint
// when resume is true.
func (c *Core) GetPreflightAudience(ctx context.Context, id, afterID int, resume bool) ([]models.PreflightList, models.PreflightAudience, error) {
	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, models.PreflightAudience{}, err
	}
	defer tx.Rollback()
	return c.preflightAudience(ctx, tx, id, afterID, resume)
}

// CheckPreflightVersion rejects a preflight assembled across a campaign edit. The
// separate reads may see newer lists or samples, so verify the original version
// after all of them have finished, without locking the campaign while counting.
func (c *Core) CheckPreflightVersion(ctx context.Context, camp models.Campaign) error {
	var current bool
	if err := c.db.GetContext(ctx, &current, `SELECT EXISTS (SELECT 1 FROM campaigns
		WHERE id = $1 AND status = $2 AND updated_at IS NOT DISTINCT FROM $3)`, camp.ID, camp.Status, camp.UpdatedAt); err != nil {
		c.log.Printf("error checking the preflight version of campaign %d: %v", camp.ID, err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
	}
	if !current {
		return echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
	}
	return nil
}

// stmtOn is where a prepared statement runs: on the pool (onPool) or on a transaction's own
// connection (tx.Stmtx). A caller that holds a row lock must not read on another connection:
// with a small pool and the sender's lock connection in it, the second one would wait for a
// free connection while this one is held (LST-48 review).
type stmtOn func(*sqlx.Stmt) *sqlx.Stmt

func onPool(s *sqlx.Stmt) *sqlx.Stmt { return s }

// preflightAudience keeps the larger working memory for the aggregate scoped to
// the transaction. At the default 4 MB PostgreSQL spills both the join and the
// grouping sets to temporary files on the 400k-participant fixture.
func (c *Core) preflightAudience(ctx context.Context, tx *sqlx.Tx, id, afterID int, resume bool) ([]models.PreflightList, models.PreflightAudience, error) {
	if _, err := tx.ExecContext(ctx, `SELECT set_config('work_mem', '24MB', true)
		WHERE pg_size_bytes(current_setting('work_mem')) < pg_size_bytes('24MB')`); err != nil {
		return nil, models.PreflightAudience{}, err
	}
	var rows []models.PreflightRow
	if err := tx.Stmtx(c.q.AxmPreflightAudience).SelectContext(ctx, &rows, id, afterID, resume); err != nil {
		c.log.Printf("error counting the recipients of campaign %d: %v", id, err)
		return nil, models.PreflightAudience{}, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
	}

	// A campaign without lists has no rows and reaches nobody.
	aud := models.PreflightAudience{AsOf: time.Now()}
	lists := make([]models.PreflightList, 0, len(rows))
	for _, r := range rows {
		lists = append(lists, r.PreflightList)
		aud = r.PreflightAudience
	}

	// AXM (LST-47): a follow-up's check before sending holds back the people who reacted to the
	// original since (or opted out of it). Those it would hold now are not counted, so a
	// follow-up that would reach nobody is stopped as any empty campaign is. They are only on
	// the follow-up list among the campaign's lists (else they are not held).
	if len(rows) > 0 {
		held, listID, err := c.followupHeldNow(ctx, func(st *sqlx.Stmt) *sqlx.Stmt { return tx.Stmtx(st) }, id)
		if err != nil {
			c.log.Printf("error counting who the check would hold back from campaign %d: %v", id, err)
			return nil, models.PreflightAudience{}, echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
		}
		if held != nil && held.Reachable > 0 {
			for i := range lists {
				if lists[i].ID.Valid && int(lists[i].ID.Int) == listID {
					lists[i].Count = max(0, lists[i].Count-held.Reachable)
				}
			}
			aud.Total = max(0, aud.Total-held.Reachable)
			aud.Excluded += held.Reachable
			if resume {
				aud.Remaining = max(0, aud.Remaining-held.ReachableAfter)
			} else {
				aud.Remaining = aud.Total
			}
		}
	}
	return lists, aud, nil
}

// checkCampaignSeen refuses (409) to go on when campaign id was changed after seenAt, the
// updated_at that the pre-send confirmation showed. The row is locked until the end of tx,
// so nothing can change it between this check and the status change. An empty seenAt does
// not check: callers that do not use the confirmation (the API) behave as before.
func (c *Core) checkCampaignSeen(tx *sqlx.Tx, id int, seenAt string) error {
	if seenAt == "" {
		return nil
	}
	var same bool
	if err := tx.Get(&same, `SELECT updated_at = $2::TIMESTAMPTZ FROM campaigns WHERE id = $1 FOR UPDATE`, id, seenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest,
				c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
		}
		// An unreadable timestamp is the caller's mistake.
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code.Class() == "22" {
			return echo.NewHTTPError(http.StatusBadRequest, c.i18n.Ts("globals.messages.invalidFields", "name", "updated_at"))
		}
		return err
	}
	if !same {
		return echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
	}
	return nil
}

// lockCampaignState reads, on the transaction and locked for it, what a status change of
// campaign id is judged by (LST-48): its ID, status and send time, and whether the send time
// has come by the database's clock, the one the sender goes by (next-campaigns).
func (c *Core) lockCampaignState(tx *sqlx.Tx, id int) (models.Campaign, bool, error) {
	var (
		cm  models.Campaign
		due bool
	)
	err := tx.Get(&cm, `SELECT id, status, send_at, type, started_at, last_subscriber_id, sent, axm_legacy_send_history
		FROM campaigns WHERE id = $1 FOR UPDATE`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return cm, false, echo.NewHTTPError(http.StatusBadRequest,
			c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
	}
	if err != nil {
		return cm, false, err
	}
	// Read the clock now that the row is held, with clock_timestamp(): NOW() is when the
	// transaction began, before any wait for the lock (LST-48 review: a request that waited
	// 12 s for the lock was judged by the time before the wait and the campaign went out).
	if cm.SendAt.Valid {
		err = tx.QueryRowx(`SELECT clock_timestamp() >= $1::TIMESTAMPTZ`, cm.SendAt.Time).Scan(&due)
	}
	return cm, due, err
}

// checkStatusAudience runs after the confirmation version check, under the same campaign lock
// used by edits to the campaign or its exclusion rule. A paused resume may finish with no mail.
func (c *Core) checkStatusAudience(tx *sqlx.Tx, cm models.Campaign, status string) error {
	if cm.Type != models.CampaignTypeRegular ||
		!(status == models.CampaignStatusScheduled || status == models.CampaignStatusRunning && cm.Status == models.CampaignStatusDraft) {
		return nil
	}
	hasHistory := cm.StartedAt.Valid || cm.LastSubscriberID > 0 || cm.Sent > 0 || cm.LegacySendHistory
	if !hasHistory {
		// An ordinary draft may still start when the operator cannot load the confirmation.
		// Identify exclusions without reading the send ledger, which may be unavailable.
		var usesExclusions bool
		if err := tx.Get(&usesExclusions, `SELECT axm_recent_rule_on($1) OR EXISTS (
			SELECT 1 FROM axm_campaign_recent_holds WHERE campaign_id = $1)`, cm.ID); err != nil {
			return c.recentError(err)
		}
		if !usesExclusions {
			// AXM (LST-71): nothing that depends on the ledger applies, but a regular campaign
			// with no list or nobody on its lists is still not started or scheduled, through the
			// API as through the confirmation window (LST-66 table 3).
			return c.checkListedAudience(tx, cm.ID)
		}
	}

	lists, aud, err := c.preflightAudience(context.Background(), tx, cm.ID, cm.LastSubscriberID, hasHistory)
	if err != nil {
		return err
	}
	if hasHistory && aud.Remaining > 0 || !hasHistory && (aud.Recent == 0 || aud.Total > aud.Recent) {
		return nil
	}
	for _, list := range lists {
		if !list.Deleted {
			return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recheck.zero"))
		}
	}
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recheck.noLists"))
}

// checkListedAudience refuses (400) a regular campaign that has no list left or nobody on its
// lists, with the messages of the scheduled save. It reads no send ledger (LST-71).
func (c *Core) checkListedAudience(tx *sqlx.Tx, id int) error {
	var aud struct {
		HasLists      bool `db:"has_lists"`
		HasRecipients bool `db:"has_recipients"`
	}
	if err := tx.Stmtx(c.q.AxmPreflightListedRecipient).Get(&aud, id); err != nil {
		c.log.Printf("error checking the recipients of campaign %d: %v", id, err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
	}
	switch {
	case !aud.HasLists:
		return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.start.noLists"))
	case !aud.HasRecipients:
		return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.start.zero"))
	}
	return nil
}

// checkSendTimeShown refuses a status change from the pre-send confirmation (seenAt given)
// that schedules a campaign whose send time has come, when the confirmation did not say so
// (pastShown): the campaign would go out at once while the person read "later" (LST-48
// review: a browser clock behind the server's). The answer is 412; the window reads again
// and shows the warning. A change without seenAt (the API) is not judged.
func (c *Core) checkSendTimeShown(cm models.Campaign, due bool, status, seenAt string, pastShown bool) error {
	if seenAt == "" || pastShown || !due {
		return nil
	}
	// As update-campaign-status: a start with a send time schedules the campaign.
	if status == models.CampaignStatusScheduled || (status == models.CampaignStatusRunning && cm.SendAt.Valid) {
		return echo.NewHTTPError(http.StatusPreconditionFailed, c.i18n.T("campaigns.sendConfirm.timePassed"))
	}
	return nil
}

// GetPreflightSample is up to n of the people a campaign would reach, the lowest IDs first,
// by the sender's rule (axm-preflight-sample, as axm-preflight-audience). The pre-send check
// renders the mail for them too, not only for an example subscriber (LST-48 review).
//
// resume: only the people still to be sent after checkpoint afterID (LST-48 review: on resume
// the sample was the people already sent).
func (c *Core) GetPreflightSample(ctx context.Context, campID, n, afterID int, resume bool) []models.Subscriber {
	var out []models.Subscriber
	if err := c.q.AxmPreflightSample.SelectContext(ctx, &out, campID, n, afterID, resume); err != nil {
		c.log.Printf("error reading the pre-send sample of campaign %d: %v", campID, err)
		return nil
	}
	return out
}

// GetCampaignPausedAt is when campaign id was last paused by a person (the audit log), else
// fallback, the campaign's last change. The audit row only counts while nothing started the
// campaign again after it: a campaign that was resumed and then stopped by the sender (too many
// errors) has no audit row for that stop, and its last change is the better answer.
func (c *Core) GetCampaignPausedAt(id int, fallback null.Time) null.Time {
	var at null.Time
	err := c.db.Get(&at, `SELECT p.at FROM (
		SELECT MAX(created_at) AS at FROM axm_audit_log
		WHERE action = 'campaign.pause' AND target_type = 'campaign' AND target_id = $1
	) p WHERE NOT EXISTS (
		SELECT 1 FROM axm_audit_log
		WHERE action IN ('campaign.start', 'campaign.schedule') AND target_type = 'campaign' AND target_id = $1 AND created_at > p.at
	)`, id)
	if err != nil || !at.Valid {
		return fallback
	}
	return at
}

// CampaignSendProgress reconciles the saved counter with known deliveries. For campaigns
// that began before the deletion tally, the result may be only a minimum.
func (c *Core) CampaignSendProgress(ctx context.Context, camp models.Campaign) (int, error) {
	var history struct {
		Recorded int `db:"recorded"`
		Removed  int `db:"removed"`
	}
	err := c.db.GetContext(ctx, &history, `SELECT COUNT(*) AS recorded,
		COALESCE((SELECT sent FROM axm_campaign_expired_sends WHERE campaign_id = $1), 0) AS removed
		FROM (
			SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1
			UNION ALL
			SELECT k.subscriber_id FROM axm_campaign_followup_claims k WHERE k.campaign_id = $1 AND k.sent
				AND NOT EXISTS (SELECT 1 FROM axm_campaign_sends s
					WHERE s.campaign_id = k.campaign_id AND s.subscriber_id = k.subscriber_id)
		) deliveries`, camp.ID)
	if err != nil {
		return 0, err
	}
	return max(camp.Sent, history.Recorded+history.Removed), nil
}

// ScheduledCheck judges whether the edited campaign has lists and can still reach anyone.
// An error refuses the save.
type ScheduledCheck func(hasLists, hasRecipients bool) error

// UpdateScheduledCampaign saves and checks a scheduled campaign in one transaction. Lock the
// campaign before touching its lists so each editor checks its own replacement, and a sender
// that has already picked up the campaign wins the race. The sender skips locked rows and
// discards counts from an older row version if a save commits while it is counting.
func (c *Core) UpdateScheduledCampaign(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, check ScheduledCheck) (models.Campaign, error) {
	return c.updateCampaignSave(ctx, id, o, listIDs, mediaIDs, nil, 0, check, true, "")
}

// UpdateScheduledCampaignWithRecentRule atomically saves a scheduled campaign, its replacement
// recent-send rule, and the recipient check that decides whether the new schedule is viable.
func (c *Core) UpdateScheduledCampaignWithRecentRule(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, rule models.RecentRule, userID int, check ScheduledCheck) (models.Campaign, error) {
	return c.updateCampaignSave(ctx, id, o, listIDs, mediaIDs, &rule, userID, check, true, "")
}

// UpdateCampaignWithRecentRule saves a campaign edit and its dirty rule after locking the live
// campaign status. The status read in the HTTP handler is only a permission check: another editor
// may schedule the draft while this request is in flight.
func (c *Core) UpdateCampaignWithRecentRule(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, rule models.RecentRule, userID int, check ScheduledCheck) (models.Campaign, error) {
	return c.updateCampaignSave(ctx, id, o, listIDs, mediaIDs, &rule, userID, check, false, "")
}

// UpdateDraftCampaign saves an edit of a draft or paused campaign under the campaign row lock
// (LST-71). The handler read the status (expect) before it validated the request, and that
// read is no lock: another request may have scheduled the campaign, or the sender started it,
// meanwhile. An unlocked write then put the draft's edit into a scheduled campaign without the
// scheduled save's check, or rewrote the body and lists of one that was already sending. Under
// the lock a status that is not the one the edit was judged for is refused: 409 when the
// campaign can still be edited (the editor reads it again), 400 when it can not.
func (c *Core) UpdateDraftCampaign(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, expect string) (models.Campaign, error) {
	return c.updateCampaignSave(ctx, id, o, listIDs, mediaIDs, nil, 0, nil, false, expect)
}

func (c *Core) updateCampaignSave(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, rule *models.RecentRule, userID int, check ScheduledCheck, requireScheduled bool, expect string) (models.Campaign, error) {
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return models.Campaign{}, err
	}
	defer tx.Rollback()

	run := func(st *sqlx.Stmt) *sqlx.Stmt { return tx.Stmtx(st) }
	var before struct {
		Status           string `db:"status"`
		Type             string `db:"type"`
		LastSubscriberID int    `db:"last_subscriber_id"`
		Started          bool   `db:"started"`
	}
	if err := tx.GetContext(ctx, &before, `SELECT status, type, last_subscriber_id, started_at IS NOT NULL AS started FROM campaigns WHERE id = $1 FOR NO KEY UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest,
				c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
		}
		return models.Campaign{}, err
	}
	switch before.Status {
	case models.CampaignStatusScheduled:
	case models.CampaignStatusDraft, models.CampaignStatusPaused:
		if requireScheduled {
			return models.Campaign{}, echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
		}
	default:
		return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	}
	// AXM (LST-71): the status the caller judged the edit for is the one under the lock.
	if expect != "" && before.Status != expect {
		return models.Campaign{}, echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
	}
	if rule != nil && !models.CanEditRecentRule(before.Status, before.Started) {
		return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	}

	if _, err := run(c.q.UpdateCampaign).ExecContext(ctx, updateCampaignArgs(id, o, listIDs, mediaIDs)...); err != nil {
		return models.Campaign{}, c.updateCampaignErr(err)
	}
	if rule != nil {
		if before.Type != models.CampaignTypeRegular {
			return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.recent.regularOnly"))
		}
		if _, err := c.saveRecentRuleTx(ctx, tx, id, *rule, userID); err != nil {
			return models.Campaign{}, err
		}
	}

	// Clearing the send time makes the campaign a draft. Opt-in campaigns only warn.
	if before.Status == models.CampaignStatusScheduled && o.SendAt.Valid && before.Type == models.CampaignTypeRegular && check != nil {
		followupListID, err := c.preflightFollowupList(ctx, run, id)
		if err != nil {
			c.log.Printf("error finding the follow-up list for campaign %d: %v", id, err)
			return models.Campaign{}, echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
		}
		var audience struct {
			HasLists      bool `db:"has_lists"`
			HasRecipients bool `db:"has_recipients"`
		}
		if err := run(c.q.AxmPreflightHasRecipient).GetContext(ctx, &audience, id, before.LastSubscriberID, followupListID, c.tracking()); err != nil {
			c.log.Printf("error checking the recipients of campaign %d: %v", id, err)
			return models.Campaign{}, echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
		}
		if err := check(audience.HasLists, audience.HasRecipients); err != nil {
			return models.Campaign{}, err
		}
	}

	// Read the response while the row is still locked. The editor uses this version
	// to tell whether the preflight belongs to its save or to a later edit.
	saved, err := c.getCampaign(run, id, "", "", campaignTplDefault)
	if err != nil {
		return models.Campaign{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Campaign{}, err
	}
	return saved, nil
}

// ReturnedCampaign is a scheduled campaign that RevertEmptyScheduled took out of the schedule.
type ReturnedCampaign struct {
	ID     int    `db:"id"`
	Name   string `db:"name"`
	Status string `db:"status"`
}

// RevertEmptyScheduled takes the campaigns among ids (the ones the sender found due and did not
// start, because nobody is on their lists) out of the schedule (LST-71): a campaign that never
// started becomes a draft, one that started before becomes paused. The sender leaves such a
// campaign scheduled, so it was read again at every scan for as long as nobody subscribed, and
// went out to whoever did, much later, without a new schedule.
func (c *Core) RevertEmptyScheduled(ids []int) ([]ReturnedCampaign, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []ReturnedCampaign
	if err := c.q.AxmRevertEmptyScheduled.Select(&out, pq.Array(ids)); err != nil {
		return nil, err
	}
	for _, r := range out {
		c.log.Printf("campaign %d (%s): nobody is on its lists at its send time, not sent; back to %s", r.ID, r.Name, r.Status)
	}
	return out, nil
}

// ScheduledCampaignRef names a scheduled campaign for a refusal that lists the ones it concerns.
type ScheduledCampaignRef struct {
	ID        int    `db:"id"`
	Name      string `db:"name"`
	FromEmail string `db:"from_email"`
}

// GetScheduledByTemplate is the scheduled regular campaigns that go out with template id.
func (c *Core) GetScheduledByTemplate(id int) ([]ScheduledCampaignRef, error) {
	var out []ScheduledCampaignRef
	if err := c.q.AxmScheduledByTemplate.Select(&out, id); err != nil {
		c.log.Printf("error reading the scheduled campaigns of template %d: %v", id, err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaigns}", "error", pqErrMsg(err)))
	}
	return out, nil
}

// GetScheduledSenders is the scheduled campaigns with their From.
func (c *Core) GetScheduledSenders() ([]ScheduledCampaignRef, error) {
	var out []ScheduledCampaignRef
	if err := c.q.AxmScheduledSenders.Select(&out); err != nil {
		c.log.Printf("error reading the senders of scheduled campaigns: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaigns}", "error", pqErrMsg(err)))
	}
	return out, nil
}
