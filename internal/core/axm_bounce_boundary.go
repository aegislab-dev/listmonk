package core

import (
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// The team boundary on bounce records (LST-73). A bounce belongs to a subscriber, and the
// subscriber is one row for the whole company (see axm_sub_boundary.go). Deleting a bounce
// record also frees the address a hard bounce had kept out of imports (LST-56), and "block
// everyone who bounced" blocklists people, so a user who may not manage all lists reaches only
// the bounces of subscribers that are not locked for them: every subscription of the subscriber
// is on a list the user may manage. Reading the bounces stays company-wide.

// bounceOwners reads whose bounces a delete reaches: subscriber ID -> number of bounces.
func (c *Core) bounceOwners(ids []int, all bool, campID int, source string) (map[int]int, error) {
	if ids == nil {
		ids = []int{}
	}
	rows := []struct {
		SubID int `db:"subscriber_id"`
		Num   int `db:"num"`
	}{}
	if err := c.q.AxmBounceSubscribers.Select(&rows, pq.Array(ids), all, campID, source); err != nil {
		return nil, c.teamFail(err)
	}
	out := make(map[int]int, len(rows))
	for _, r := range rows {
		out[r.SubID] = r.Num
	}
	return out, nil
}

func (c *Core) bounceDeleteFail(err error) error {
	c.log.Printf("error deleting bounces: %v", err)
	return echo.NewHTTPError(http.StatusInternalServerError,
		c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.bounces}", "error", pqErrMsg(err)))
}

// AfterBounceOwnersRead is called by DeleteBouncesBounded after it has read whose bounces a
// delete reaches and before it takes the locks. Tests use it to change the data in that gap.
var AfterBounceOwnersRead func()

// AfterBounceRecount is called inside the transaction after the bounce rows were read again and
// locked, before anything is deleted. Tests use it to try a delete in that gap.
var AfterBounceRecount func()

// DeleteBouncesBounded deletes bounce records for a user who may not manage all lists.
//
// By ID (all = false) the delete is all or nothing: if one of the bounces is of a locked
// subscriber, nothing is deleted and the IDs of the locked subscribers come back. With all = true
// (the bounces that match campID and source) the bounces of locked subscribers are left out.
// The owners are read, locked and looked at again in one transaction (boundedTx); the left-out
// count is read again under the locks, because somebody else may have cleared those bounces
// in the meantime (an administrator's delete does not wait for the locks of the people). It
// returns how many bounces were deleted and how many were left out.
func (c *Core) DeleteBouncesBounded(ids []int, all bool, campID int, source string, manageListIDs []int) (int, int, []int, error) {
	owners, err := c.bounceOwners(ids, all, campID, source)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(owners) == 0 {
		return 0, 0, nil, nil
	}
	subIDs := make([]int, 0, len(owners))
	for id := range owners {
		subIDs = append(subIDs, id)
	}
	if AfterBounceOwnersRead != nil {
		AfterBounceOwnersRead()
	}

	done, skipped := 0, 0
	refused, err := c.boundedTxPresent(subIDs, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
		// Look again under the locks: only the bounces that are still there count, so a locked
		// person whose bounces somebody else already cleared neither refuses a delete by ID nor
		// shows up in the left-out count. The bounce rows are locked too (FOR UPDATE, waits for
		// a delete that is not committed yet): deleting a bounce does not lock its subscriber, so
		// otherwise a delete could commit between this count and ours.
		var now []int
		if ids == nil {
			ids = []int{}
		}
		if err := tx.Stmtx(c.q.AxmBounceRowsLock).Select(&now, pq.Array(ids), all, campID, source); err != nil {
			return false, c.teamFail(err)
		}
		if AfterBounceRecount != nil {
			AfterBounceRecount()
		}
		left := make(map[int]int, len(now))
		for _, id := range now {
			left[id]++
		}
		stillLocked := []int{}
		for _, id := range locked {
			if left[id] > 0 {
				stillLocked = append(stillLocked, id)
			}
		}
		if !all && len(stillLocked) > 0 {
			return false, nil
		}
		if len(open) > 0 {
			res, err := tx.Stmtx(c.q.AxmDeleteBouncesOf).Exec(pq.Array(open), all, pq.Array(ids), campID, source)
			if err != nil {
				return false, c.bounceDeleteFail(err)
			}
			n, _ := res.RowsAffected()
			done = int(n)
		}
		for _, id := range stillLocked {
			skipped += left[id]
		}
		return true, nil
	})
	if err != nil || len(refused) > 0 {
		return 0, 0, refused, err
	}
	return done, skipped, nil, nil
}

// AfterBouncedRead is called by BlocklistBouncedSubscribersBounded after it has read who has
// bounces and before it takes the locks. Tests use it to change the data in that gap.
var AfterBouncedRead func()

// AfterBouncedRecheck is called inside the transaction after the bounce rows were re-read and
// locked, before anybody is blocklisted. Tests use it to try a delete in that gap.
var AfterBouncedRecheck func()

// BlocklistBouncedSubscribersBounded blocklists everybody who has a bounce record and is not
// locked for a user who may not manage all lists. It returns how many people were blocklisted
// and how many were left out because they are on other lists too.
func (c *Core) BlocklistBouncedSubscribersBounded(manageListIDs []int) (int, int, error) {
	var subIDs []int
	if err := c.q.AxmBouncedSubscribers.Select(&subIDs); err != nil {
		return 0, 0, c.teamFail(err)
	}
	if len(subIDs) == 0 {
		return 0, 0, nil
	}
	if AfterBouncedRead != nil {
		AfterBouncedRead()
	}
	done, skipped := 0, 0
	_, err := c.boundedTxPresent(subIDs, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
		// The candidates were read before the locks: somebody whose last bounce was cleared in
		// the meantime is not "everybody who bounced" any more. Look again, under the locks, and
		// lock the bounce rows too: deleting a bounce does not lock its subscriber.
		var still []int
		if err := tx.Stmtx(c.q.AxmBouncedAmong).Select(&still, pq.Array(subIDs)); err != nil {
			return false, c.teamFail(err)
		}
		bounced := make(map[int]bool, len(still))
		for _, id := range still {
			bounced[id] = true
		}
		keep := func(ids []int) []int {
			out := []int{}
			for _, id := range ids {
				if bounced[id] {
					out = append(out, id)
				}
			}
			return out
		}
		locked, open = keep(locked), keep(open)
		if AfterBouncedRecheck != nil {
			AfterBouncedRecheck()
		}
		if len(open) > 0 {
			if _, err := tx.Stmtx(c.q.BlocklistSubscribers).Exec(pq.Array(open)); err != nil {
				c.log.Printf("error blocklisting bounced subscribers: %v", err)
				return false, echo.NewHTTPError(http.StatusInternalServerError,
					c.i18n.Ts("subscribers.errorBlocklisting", "error", pqErrMsg(err)))
			}
		}
		done, skipped = len(open), len(locked)
		return true, nil
	})
	return done, skipped, err
}
