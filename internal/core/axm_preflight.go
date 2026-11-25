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
// afterID is the campaign's sending checkpoint, used when resume is true. Everything comes
// from one statement, so it is one snapshot.
func (c *Core) GetPreflightAudience(ctx context.Context, id, afterID int, resume bool) ([]models.PreflightList, models.PreflightAudience, error) {
	return c.preflightAudience(ctx, onPool, id, afterID, resume)
}

// stmtOn is where a prepared statement runs: on the pool (onPool) or on a transaction's own
// connection (tx.Stmtx). A caller that holds a row lock must not read on another connection:
// with a small pool and the sender's lock connection in it, the second one would wait for a
// free connection while this one is held (LST-48 review).
type stmtOn func(*sqlx.Stmt) *sqlx.Stmt

func onPool(s *sqlx.Stmt) *sqlx.Stmt { return s }

// preflightAudience is GetPreflightAudience on the connection that run says.
func (c *Core) preflightAudience(ctx context.Context, run stmtOn, id, afterID int, resume bool) ([]models.PreflightList, models.PreflightAudience, error) {
	var rows []models.PreflightRow
	if err := run(c.q.AxmPreflightAudience).SelectContext(ctx, &rows, id, afterID, resume); err != nil {
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
		held, listID, err := c.followupHeldNow(ctx, run, id)
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

// ScheduledCheck judges the recipients of a scheduled campaign that is being saved (LST-67): its
// lists as the send reads them and the people it reaches. resumed: the campaign was started and
// paused before (it has a checkpoint or sent mails), so the people it still reaches are
// aud.Remaining, as when it is resumed. An error refuses the save.
type ScheduledCheck func(lists []models.PreflightList, aud models.PreflightAudience, resumed bool) error

// Progressed reports whether a campaign has been sent from before (a checkpoint or sent mails):
// then the send goes on after the checkpoint and skips who has a record, and the people it still
// reaches are the remaining ones, not all that are eligible.
func Progressed(lastSubscriberID, sent int) bool { return lastSubscriberID > 0 || sent > 0 }

// UpdateScheduledCampaign saves campaign id, which is scheduled, and checks who it would reach
// now (LST-67): when the saved campaign is a regular one that is still scheduled, check judges
// its lists and recipients, and an error from it undoes the save, so the schedule stays as it
// was. Before this the edit of a scheduled campaign was saved without a look at the lists, and
// the send time came with no check at all.
//
// The check reads the new lists inside the transaction (nobody else sees them before the commit)
// but the campaign's row is locked only afterwards, for the one statement that saves it. A lock
// held while the people are counted (seconds on a big list) would make the sender, which picks a
// campaign by the committed row, wait with a decision made on the old send time: when the
// time passed meanwhile it would send the campaign as it was after the save moved the time
// (review: the old subject went out to 40 people after a save that moved the time a day on).
// With the lock taken last, a sender that took the campaign up meanwhile is seen then (the
// campaign is no longer scheduled) and the save is refused, as before. Everything runs on one
// connection (the sender's lock connection is in a small pool; LST-48 review).
func (c *Core) UpdateScheduledCampaign(ctx context.Context, id int, o models.Campaign, listIDs, mediaIDs []int, check ScheduledCheck) (models.Campaign, error) {
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return models.Campaign{}, err
	}
	defer tx.Rollback()

	var before struct {
		Status           string `db:"status"`
		Type             string `db:"type"`
		Sent             int    `db:"sent"`
		LastSubscriberID int    `db:"last_subscriber_id"`
	}
	if err := tx.Get(&before, `SELECT status, type, sent, last_subscriber_id FROM campaigns WHERE id = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest,
				c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
		}
		return models.Campaign{}, err
	}
	if before.Status != models.CampaignStatusScheduled && before.Status != models.CampaignStatusDraft && before.Status != models.CampaignStatusPaused {
		return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	}

	// A save that takes the send time off makes a scheduled campaign a draft: not judged; an
	// opt-in campaign only warns.
	if before.Status == models.CampaignStatusScheduled && o.SendAt.Valid && before.Type == models.CampaignTypeRegular && check != nil {
		// The lists as update-campaign leaves them (the same two statements), so the count reads
		// them; the row of the campaign itself is not touched yet.
		if _, err := tx.Exec(`DELETE FROM campaign_lists WHERE campaign_id = $1 AND NOT(list_id = ANY($2))`, id, pq.Array(listIDs)); err != nil {
			return models.Campaign{}, c.updateCampaignErr(err)
		}
		if _, err := tx.Exec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name)
			(SELECT $1, id, name FROM lists WHERE id = ANY($2::INT[]))
			ON CONFLICT (campaign_id, list_id) DO UPDATE SET list_name = EXCLUDED.list_name`, id, pq.Array(listIDs)); err != nil {
			return models.Campaign{}, c.updateCampaignErr(err)
		}
		resumed := Progressed(before.LastSubscriberID, before.Sent)
		afterID := 0
		if resumed {
			afterID = before.LastSubscriberID
		}
		lists, aud, err := c.preflightAudience(ctx, func(st *sqlx.Stmt) *sqlx.Stmt { return tx.Stmtx(st) }, id, afterID, resumed)
		if err != nil {
			return models.Campaign{}, err
		}
		if err := check(lists, aud, resumed); err != nil {
			return models.Campaign{}, err
		}
	}

	// Now the row, for the one statement. Taken up by the sender (or ended) since it was read:
	// refused, its body does not change under a send.
	var status string
	if err := tx.Get(&status, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, id); err != nil {
		return models.Campaign{}, err
	}
	switch {
	case status != models.CampaignStatusScheduled && status != models.CampaignStatusDraft && status != models.CampaignStatusPaused:
		return models.Campaign{}, echo.NewHTTPError(http.StatusBadRequest, c.i18n.T("campaigns.cantUpdate"))
	case status != before.Status:
		// Unscheduled (or paused) by someone else meanwhile: what was counted was for another state.
		return models.Campaign{}, echo.NewHTTPError(http.StatusConflict, c.i18n.T("campaigns.sendConfirm.changed"))
	}
	if _, err := tx.Stmtx(c.q.UpdateCampaign).Exec(updateCampaignArgs(id, o, listIDs, mediaIDs)...); err != nil {
		return models.Campaign{}, c.updateCampaignErr(err)
	}
	if err := tx.Commit(); err != nil {
		return models.Campaign{}, err
	}
	return c.GetCampaign(id, "", "")
}
