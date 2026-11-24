package core

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// The team boundary on subscribers (LST-42): writes by a user who may not manage all lists.
// The "is this subscriber on another list too?" check and the change run in one transaction
// that holds the subscribers' row locks, so a subscription added by somebody else at the
// same moment is either seen by the check or waits until the change is done.

// boundedTx locks the subscribers, finds the locked ones among them (LockedSubscribers) and
// runs fn. fn returns false to give up without changing anything.
func (c *Core) boundedTx(subIDs, manageListIDs []int, fn func(tx *sqlx.Tx, locked []int) (bool, error)) ([]int, error) {
	return c.boundedTxPresent(subIDs, manageListIDs, func(tx *sqlx.Tx, locked, _ []int) (bool, error) { return fn(tx, locked) })
}

// boundedTxPresent is boundedTx that also tells fn which of the subscribers exist (and are
// not locked): somebody else may have deleted one in the meantime.
func (c *Core) boundedTxPresent(subIDs, manageListIDs []int, fn func(tx *sqlx.Tx, locked, open []int) (bool, error)) ([]int, error) {
	if manageListIDs == nil {
		manageListIDs = []int{}
	}
	tx, err := c.db.Beginx()
	if err != nil {
		return nil, c.teamFail(err)
	}
	defer tx.Rollback()

	if _, err := tx.Stmtx(c.q.AxmLockSubscribers).Exec(pq.Array(subIDs)); err != nil {
		return nil, c.teamFail(err)
	}
	rows := []struct {
		SubID  int  `db:"subscriber_id"`
		Locked bool `db:"locked"`
	}{}
	if err := tx.Stmtx(c.q.AxmSubscriberLocked).Select(&rows, pq.Array(subIDs), pq.Array(manageListIDs)); err != nil {
		return nil, c.teamFail(err)
	}
	locked, open := []int{}, []int{}
	for _, r := range rows {
		if r.Locked {
			locked = append(locked, r.SubID)
		} else {
			open = append(open, r.SubID)
		}
	}
	sort.Ints(locked)
	sort.Ints(open)

	ok, err := fn(tx, locked, open)
	if err != nil {
		return nil, err
	}
	if !ok {
		return locked, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, c.teamFail(err)
	}
	return nil, nil
}

// allOrNothing runs exec when none of the subscribers is locked.
func (c *Core) allOrNothing(subIDs, manageListIDs []int, exec func(tx *sqlx.Tx) error) ([]int, error) {
	return c.boundedTx(subIDs, manageListIDs, func(tx *sqlx.Tx, locked []int) (bool, error) {
		if len(locked) > 0 {
			return false, nil
		}
		return true, exec(tx)
	})
}

// DeleteSubscribersBounded deletes the subscribers unless one of them has a subscription
// outside manageListIDs. It returns the IDs of those; then nobody was deleted.
func (c *Core) DeleteSubscribersBounded(subIDs, manageListIDs []int) ([]int, error) {
	return c.allOrNothing(subIDs, manageListIDs, func(tx *sqlx.Tx) error {
		if _, err := tx.Stmtx(c.q.DeleteSubscribers).Exec(pq.Array(subIDs), pq.Array([]string{})); err != nil {
			c.log.Printf("error deleting subscribers: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.subscribers}", "error", pqErrMsg(err)))
		}
		return nil
	})
}

// BlocklistSubscribersBounded blocklists the subscribers unless one of them has a
// subscription outside manageListIDs. It returns the IDs of those; then nobody changed.
func (c *Core) BlocklistSubscribersBounded(subIDs, manageListIDs []int) ([]int, error) {
	return c.allOrNothing(subIDs, manageListIDs, func(tx *sqlx.Tx) error {
		if _, err := tx.Stmtx(c.q.BlocklistSubscribers).Exec(pq.Array(subIDs)); err != nil {
			c.log.Printf("error blocklisting subscribers: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("subscribers.errorBlocklisting", "error", err.Error()))
		}
		return nil
	})
}

// DeleteSubscriberBouncesBounded deletes the bounce records of a subscriber unless they have
// a subscription outside manageListIDs (then it returns the ID).
func (c *Core) DeleteSubscriberBouncesBounded(id int, manageListIDs []int) ([]int, error) {
	return c.allOrNothing([]int{id}, manageListIDs, func(tx *sqlx.Tx) error {
		return c.deleteSubscriberBouncesTx(tx, id, nil)
	})
}

// UpdateSubscriberBounded saves a subscriber for a user who may not manage all lists.
//
// A subscriber with every subscription on manageListIDs is saved like
// UpdateSubscriberWithLists. Any other subscriber is locked: changed tells whether the
// request changes the profile as it is now. If it does, nothing is saved and the ID comes
// back. If it does not, only the subscriptions on ownListIDs change; the subscriber row is
// not written at all, so nothing in it (a number JSON cannot carry exactly, say) can shift.
// ownListIDs are the lists whose subscriptions may be removed (see ownListsOnly in cmd).
func (c *Core) UpdateSubscriberBounded(id int, sub models.Subscriber, listIDs []int, preconfirm, deleteLists bool,
	ownListIDs, manageListIDs []int, changed func(cur models.Subscriber) bool) (models.Subscriber, []int, error) {
	subStatus := models.SubscriptionStatusUnconfirmed
	if preconfirm {
		subStatus = models.SubscriptionStatusConfirmed
	}
	if listIDs == nil {
		listIDs = []int{}
	}
	fail := func(err error) error {
		c.log.Printf("error updating subscriber: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorUpdating", "name", "{globals.terms.subscriber}", "error", pqErrMsg(err)))
	}

	refused, err := c.boundedTx([]int{id}, manageListIDs, func(tx *sqlx.Tx, locked []int) (bool, error) {
		if len(locked) > 0 {
			var cur models.Subscribers
			if err := tx.Stmtx(c.q.GetSubscriber).Select(&cur, id, nil, ""); err != nil {
				return false, fail(err)
			}
			if len(cur) == 0 || changed(cur[0]) {
				return false, nil
			}
			if _, err := tx.Stmtx(c.q.AxmUpdateSubscriberLists).Exec(id, pq.Array(listIDs), subStatus, deleteLists, pq.Array(ownListIDs)); err != nil {
				return false, fail(err)
			}
			return true, nil
		}

		attribs := []byte("{}")
		if len(sub.Attribs) > 0 {
			b, err := json.Marshal(sub.Attribs)
			if err != nil {
				return false, fail(err)
			}
			attribs = b
		}
		if _, err := tx.Stmtx(c.q.UpdateSubscriberWithLists).Exec(id, sub.Email, strings.TrimSpace(sub.Name), sub.Status,
			json.RawMessage(attribs), pq.Array(listIDs), pq.Array([]string{}), subStatus, deleteLists, pq.Array(ownListIDs), false); err != nil {
			return false, fail(err)
		}
		return true, nil
	})
	if err != nil || len(refused) > 0 {
		return models.Subscriber{}, refused, err
	}

	out, err := c.GetSubscriber(id, "", "")
	if err != nil {
		return models.Subscriber{}, nil, err
	}
	if !preconfirm && c.consts.SendOptinConfirmation {
		// Send a confirmation e-mail (if there are any double opt-in lists).
		_, _ = c.h.SendOptinConfirmation(out, listIDs)
	}
	return out, nil, nil
}

// BulkByQueryBounded deletes (or, with blocklist, blocklists) the subscribers a bulk action by
// query reaches, for a user who may not manage all lists. The single statement upstream uses
// picks its people as they were when it started, so a subscription another team adds while
// it runs would go with them. Here the people are picked first (with the export query, which
// reads the same conditions). Then, in one transaction, their rows are locked, who is on a
// list outside manageListIDs is read again, and only the others are changed. An error rolls
// everything back: nobody is changed. It returns how many were changed (people who still
// existed when they were locked) and how many were left out at that second look.
func (c *Core) BulkByQueryBounded(searchStr, queryExp string, listIDs []int, subStatus string, manageListIDs []int, blocklist bool) (int, int, error) {
	next, err := c.ExportSubscribers(searchStr, sanitizeSQLExp(queryExp), nil, listIDs, subStatus, 5000)
	if err != nil {
		return 0, 0, err
	}
	ids := []int{}
	for {
		rows, err := next()
		if err != nil {
			return 0, 0, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}

	done, late := 0, 0
	_, err = c.boundedTxPresent(ids, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
		if len(open) > 0 {
			var err error
			if blocklist {
				_, err = tx.Stmtx(c.q.BlocklistSubscribers).Exec(pq.Array(open))
			} else {
				_, err = tx.Stmtx(c.q.DeleteSubscribers).Exec(pq.Array(open), pq.Array([]string{}))
			}
			if err != nil {
				c.log.Printf("error in a bulk action on subscribers: %v", err)
				if blocklist {
					return false, echo.NewHTTPError(http.StatusInternalServerError,
						c.i18n.Ts("subscribers.errorBlocklisting", "error", pqErrMsg(err)))
				}
				return false, echo.NewHTTPError(http.StatusInternalServerError,
					c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.subscribers}", "error", pqErrMsg(err)))
			}
		}
		done, late = len(open), len(locked)
		return true, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return done, late, nil
}
