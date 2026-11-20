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

// GetPreflightSample is up to n of the people a campaign's (static) lists would reach, the
// lowest IDs first: subscribers who are not blocked, on a list of the campaign they have not
// left. The pre-send check renders the mail for them too, not only for an example subscriber
// whose attributes real people may not have (LST-48 review: an unsubscribe link shown only
// with a city attribute passed for the example and was missing for every real recipient).
func (c *Core) GetPreflightSample(ctx context.Context, campID, n int) []models.Subscriber {
	var out []models.Subscriber
	err := c.db.SelectContext(ctx, &out, `SELECT DISTINCT ON (s.id) s.* FROM subscribers s
		JOIN subscriber_lists sl ON sl.subscriber_id = s.id
		JOIN campaign_lists cl ON cl.list_id = sl.list_id
		WHERE cl.campaign_id = $1 AND s.status != 'blocklisted' AND sl.status != 'unsubscribed'
		ORDER BY s.id LIMIT $2`, campID, n)
	if err != nil {
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
