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

// DeleteBouncesBounded deletes bounce records for a user who may not manage all lists.
//
// By ID (all = false) the delete is all or nothing: if one of the bounces is of a locked
// subscriber, nothing is deleted and the IDs of the locked subscribers come back. With all = true
// (the bounces that match campID and source) the bounces of locked subscribers are left out.
// The owners are read, locked and looked at again in one transaction (boundedTx). It returns
// how many bounces were deleted and how many were left out.
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

	done, skipped := 0, 0
	refused, err := c.boundedTxPresent(subIDs, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
		if !all && len(locked) > 0 {
			return false, nil
		}
		if len(open) > 0 {
			if ids == nil {
				ids = []int{}
			}
			res, err := tx.Stmtx(c.q.AxmDeleteBouncesOf).Exec(pq.Array(open), all, pq.Array(ids), campID, source)
			if err != nil {
				return false, c.bounceDeleteFail(err)
			}
			n, _ := res.RowsAffected()
			done = int(n)
		}
		for _, id := range locked {
			skipped += owners[id]
		}
		return true, nil
	})
	if err != nil || len(refused) > 0 {
		return 0, 0, refused, err
	}
	return done, skipped, nil, nil
}

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
	done, skipped := 0, 0
	_, err := c.boundedTxPresent(subIDs, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
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
