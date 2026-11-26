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
	err := tx.QueryRowx(`SELECT id, status, send_at FROM campaigns WHERE id = $1 FOR UPDATE`, id).
		Scan(&cm.ID, &cm.Status, &cm.SendAt)
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
	return c.updateScheduledCampaign(ctx, id, o, listIDs, mediaIDs, nil, 0, check)
}

// UpdateScheduledCampaignWithRecentRule atomically saves a scheduled campaign, its replacement
// recent-send rule, and the recipient check that decides whether the new schedule is viable.
func (c *Core) UpdateScheduledCampaignWithRecentRule(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, rule models.RecentRule, userID int, check ScheduledCheck) (models.Campaign, error) {
	return c.updateScheduledCampaign(ctx, id, o, listIDs, mediaIDs, &rule, userID, check)
}

func (c *Core) updateScheduledCampaign(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, rule *models.RecentRule, userID int, check ScheduledCheck) (models.Campaign, error) {
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return models.Campaign{}, err
	}
	defer tx.Rollback()

	run := func(st *sqlx.Stmt) *sqlx.Stmt { return tx.Stmtx(st) }
	var before struct {
		Status, Type     string
		LastSubscriberID int
		Started          bool
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
		return models.Campaign{}, echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
	default:
		return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
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
	if o.SendAt.Valid && before.Type == models.CampaignTypeRegular && check != nil {
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
