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
	var rows []models.PreflightRow
	if err := c.q.AxmPreflightAudience.SelectContext(ctx, &rows, id, afterID, resume); err != nil {
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
	err := tx.QueryRowx(`SELECT id, status, send_at, COALESCE(NOW() >= send_at, false) FROM campaigns WHERE id = $1 FOR UPDATE`, id).
		Scan(&cm.ID, &cm.Status, &cm.SendAt, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return cm, false, echo.NewHTTPError(http.StatusBadRequest,
			c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.campaign}"))
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
