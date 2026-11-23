package core

import (
	"context"
	"net/http"
	"strings"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

var bounceQuerySortFields = []string{"email", "campaign_name", "source", "created_at", "type"}

// QueryBounces retrieves paginated bounce entries based on the given params.
// It also returns the total number of bounce records in the DB.
func (c *Core) QueryBounces(campID, subID int, source, orderBy, order string, offset, limit int) ([]models.Bounce, int, error) {
	if !strSliceContains(orderBy, bounceQuerySortFields) {
		orderBy = "created_at"
	}
	if order != SortAsc && order != SortDesc {
		order = SortDesc
	}

	out := []models.Bounce{}
	stmt := strings.ReplaceAll(c.q.QueryBounces, "%order%", orderBy+" "+order)
	if err := c.db.Select(&out, stmt, 0, campID, subID, source, offset, limit); err != nil {
		c.log.Printf("error fetching bounces: %v", err)
		return nil, 0, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.bounce}", "error", pqErrMsg(err)))
	}

	total := 0
	if len(out) > 0 {
		total = out[0].Total
	}

	return out, total, nil
}

// GetBounce retrieves bounce entries based on the given params.
func (c *Core) GetBounce(id int) (models.Bounce, error) {
	var out []models.Bounce
	stmt := strings.ReplaceAll(c.q.QueryBounces, "%order%", "id "+SortAsc)
	if err := c.db.Select(&out, stmt, id, 0, 0, "", 0, 1); err != nil {
		c.log.Printf("error fetching bounces: %v", err)
		return models.Bounce{}, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.bounce}", "error", pqErrMsg(err)))
	}

	if len(out) == 0 {
		return models.Bounce{}, echo.NewHTTPError(http.StatusBadRequest,
			c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.bounce}"))

	}

	return out[0], nil
}

// RecordBounce records a new bounce.
func (c *Core) RecordBounce(b models.Bounce) error {
	action, ok := c.consts.BounceActions[b.Type]
	if !ok {
		return echo.NewHTTPError(http.StatusBadRequest, c.i18n.Ts("globals.messages.invalidData")+": "+b.Type)
	}

	// One transaction: the address of a hard bounce or a complaint is kept out of future imports
	// and additions (LST-56) only when the bounce itself is recorded. The address is written
	// first: the delete action removes the subscriber, and with them the bounce rows, in the
	// statement below.
	tx, err := c.db.BeginTxx(context.Background(), nil)
	if err != nil {
		c.log.Printf("error recording bounce: %v", err)
		return err
	}
	defer tx.Rollback()
	permanent := b.Type == models.BounceTypeHard || b.Type == models.BounceTypeComplaint
	if permanent {
		// The trigger on bounces keeps the address of the record it sees. A failure to name the
		// address must not lose the bounce: a savepoint takes it back.
		if _, err := tx.Exec("SAVEPOINT axm_bounced"); err == nil {
			var set string
			if err := tx.Stmtx(c.q.AxmBounceAddress).Get(&set, b.SubscriberUUID, b.Email); err != nil {
				c.log.Printf("error naming the address of a bounce: %v", err)
				_, _ = tx.Exec("ROLLBACK TO SAVEPOINT axm_bounced")
			}
		}
	}

	_, err = tx.Stmtx(c.q.RecordBounce).Exec(b.SubscriberUUID,
		b.Email,
		b.CampaignUUID,
		b.Type,
		b.Source,
		b.Meta,
		b.CreatedAt,
		action.Count,
		action.Action)
	if err == nil {
		err = tx.Commit()
	}

	if err != nil {
		// Ignore the error if it complained of no subscriber.
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Column == "subscriber_id" {
			c.log.Printf("bounced subscriber (%s / %s) not found", b.SubscriberUUID, b.Email)
			// A bounce for an address that was changed since is still a bounce for that address.
			if em := strings.TrimSpace(b.Email); permanent && em != "" {
				if _, err := c.q.AxmBouncedOrphan.Exec(em, b.Type); err != nil {
					c.log.Printf("error remembering a bounced address: %v", err)
				}
			}
			return nil
		}

		c.log.Printf("error recording bounce: %v", err)
	}

	return err
}

// BlocklistBouncedSubscribers blocklists all bounced subscribers.
func (c *Core) BlocklistBouncedSubscribers() error {
	if _, err := c.q.BlocklistBouncedSubscribers.Exec(); err != nil {
		c.log.Printf("error blocklisting bounced subscribers: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.Ts("subscribers.errorBlocklisting", "error", err.Error()))
	}

	return nil
}

// DeleteBounce deletes a list.
func (c *Core) DeleteBounce(id int) error {
	return c.DeleteBounces([]int{id}, false)
}

// DeleteBounces deletes multiple lists.
func (c *Core) DeleteBounces(ids []int, all bool) error {
	if _, err := c.q.DeleteBounces.Exec(pq.Array(ids), all); err != nil {
		c.log.Printf("error deleting lists: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.list}", "error", pqErrMsg(err)))
	}
	return nil
}
