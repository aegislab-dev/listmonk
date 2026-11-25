package core

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// The team boundary on bounce records (LST-68). A bounce row carries the address of a
// subscriber, so a user who may not read all lists sees and changes only the bounces of
// people who have a subscription row (any status) on a list the user may read, and the
// writes follow the LST-42 rule: nobody who is on a list outside the ones the user may manage
// is touched (see boundedTx). The upstream statements stay as they are, for the user who may
// read all lists.

func (c *Core) bounceFail(what string, err error) error {
	c.log.Printf("error in bounce query (%s): %v", what, pqErrMsg(err))
	return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("globals.messages.internalError"))
}

func nonNil(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}

// QueryBouncesScoped is QueryBounces for a user who may read only the lists readListIDs: only
// the bounces of people on those lists, and the total counts only those.
func (c *Core) QueryBouncesScoped(id, campID, subID int, source, orderBy, order string, offset, limit int, readListIDs []int) ([]models.Bounce, int, error) {
	if !strSliceContains(orderBy, bounceQuerySortFields) {
		orderBy = "created_at"
	}
	if order != SortAsc && order != SortDesc {
		order = SortDesc
	}

	out := []models.Bounce{}
	if err := c.q.AxmQueryBounces.Select(&out, id, campID, subID, source, pq.Array(nonNil(readListIDs)), orderBy, order, offset, limit); err != nil {
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

// GetBounceScoped is GetBounce for such a user. A bounce out of their reach is answered like
// one that does not exist.
func (c *Core) GetBounceScoped(id int, readListIDs []int) (models.Bounce, error) {
	out, _, err := c.QueryBouncesScoped(id, 0, 0, "", "", "", 0, 1, readListIDs)
	if err != nil {
		return models.Bounce{}, err
	}
	if len(out) == 0 {
		return models.Bounce{}, echo.NewHTTPError(http.StatusBadRequest,
			c.i18n.Ts("globals.messages.notFound", "name", "{globals.terms.bounce}"))
	}
	return out[0], nil
}

// MaskBounceCampaigns takes the campaign out of the bounces whose campaign does not send to
// a list in readListIDs: the name of another team's campaign is not shown.
func (c *Core) MaskBounceCampaigns(bs []models.Bounce, readListIDs []int) error {
	ids := []int{}
	seen := map[int]bool{}
	for _, b := range bs {
		if b.Campaign == nil {
			continue
		}
		var camp struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(*b.Campaign, &camp); err != nil || seen[camp.ID] {
			continue
		}
		seen[camp.ID] = true
		ids = append(ids, camp.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	ok := []int{}
	if err := c.q.AxmReadableCampaigns.Select(&ok, pq.Array(ids), pq.Array(nonNil(readListIDs))); err != nil {
		return c.bounceFail("campaigns", err)
	}
	readable := map[int]bool{}
	for _, id := range ok {
		readable[id] = true
	}
	for i := range bs {
		if bs[i].Campaign == nil {
			continue
		}
		var camp struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(*bs[i].Campaign, &camp); err != nil || !readable[camp.ID] {
			bs[i].Campaign = nil
		}
	}
	return nil
}

type bounceScopeRow struct {
	ID           int  `db:"id"`
	SubscriberID int  `db:"subscriber_id"`
	Readable     bool `db:"readable"`
}

func subscriberIDsOf(rows []bounceScopeRow) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, r := range rows {
		if !seen[r.SubscriberID] {
			seen[r.SubscriberID] = true
			out = append(out, r.SubscriberID)
		}
	}
	sort.Ints(out)
	return out
}

// DeleteBouncesScoped deletes bounces by ID for a user who may read readListIDs and manage
// manageListIDs. Nothing is deleted when one of the bounces is out of the user's reach (it
// returns how many are) or when one of their subscribers is locked (LST-42; it returns their
// IDs). The check and the delete are one transaction that holds the subscribers' row locks.
// IDs that are no bounce are ignored.
func (c *Core) DeleteBouncesScoped(ids, readListIDs, manageListIDs []int) (int, []int, error) {
	var rows []bounceScopeRow
	if err := c.q.AxmBounceScope.Select(&rows, pq.Array(ids), pq.Array(nonNil(readListIDs))); err != nil {
		return 0, nil, c.bounceFail("scope", err)
	}
	outside := 0
	locked, err := c.boundedTx(subscriberIDsOf(rows), manageListIDs, func(tx *sqlx.Tx, locked []int) (bool, error) {
		// Looked at again under the locks: nobody adds a subscription between this and the delete.
		var again []bounceScopeRow
		if err := tx.Stmtx(c.q.AxmBounceScope).Select(&again, pq.Array(ids), pq.Array(nonNil(readListIDs))); err != nil {
			return false, c.bounceFail("scope", err)
		}
		outside = 0
		for _, r := range again {
			if !r.Readable {
				outside++
			}
		}
		if outside > 0 || len(locked) > 0 {
			return false, nil
		}
		if _, err := tx.Stmtx(c.q.DeleteBounces).Exec(pq.Array(ids), false); err != nil {
			c.log.Printf("error deleting bounces: %v", err)
			return false, echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.bounces}", "error", pqErrMsg(err)))
		}
		return true, nil
	})
	if err != nil {
		return 0, nil, err
	}
	if outside > 0 {
		return outside, nil, nil
	}
	return 0, locked, nil
}

// DeleteAllBouncesScoped deletes the bounces in the reach of the user (people on readListIDs)
// except those of locked subscribers (LST-42). It returns how many it deleted and how many it
// left because the subscriber is locked.
func (c *Core) DeleteAllBouncesScoped(readListIDs, manageListIDs []int) (int, int, error) {
	var rows []bounceScopeRow
	if err := c.q.AxmBouncesInScope.Select(&rows, pq.Array(nonNil(readListIDs))); err != nil {
		return 0, 0, c.bounceFail("scope", err)
	}
	deleted, skipped := 0, 0
	_, err := c.boundedTx(subscriberIDsOf(rows), manageListIDs, func(tx *sqlx.Tx, locked []int) (bool, error) {
		isLocked := map[int]bool{}
		for _, id := range locked {
			isLocked[id] = true
		}
		skipped = 0
		for _, r := range rows {
			if isLocked[r.SubscriberID] {
				skipped++
			}
		}
		open := []int{}
		for _, id := range subscriberIDsOf(rows) {
			if !isLocked[id] {
				open = append(open, id)
			}
		}
		res, err := tx.Stmtx(c.q.AxmDeleteBouncesScoped).Exec(pq.Array(open), pq.Array(nonNil(readListIDs)))
		if err != nil {
			c.log.Printf("error deleting bounces: %v", err)
			return false, echo.NewHTTPError(http.StatusInternalServerError,
				c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.bounces}", "error", pqErrMsg(err)))
		}
		n, _ := res.RowsAffected()
		deleted = int(n)
		return true, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return deleted, skipped, nil
}

// BlocklistBouncedScoped blocklists the subscribers that have a bounce record and are on a
// list in manageListIDs, except the locked ones (LST-42). It returns how many it blocklisted
// and how many it left because they are locked.
func (c *Core) BlocklistBouncedScoped(manageListIDs []int) (int, int, error) {
	var ids []int
	if err := c.q.AxmBouncedSubscribersInLists.Select(&ids, pq.Array(nonNil(manageListIDs))); err != nil {
		return 0, 0, c.bounceFail("subscribers", err)
	}
	done, skipped := 0, 0
	_, err := c.boundedTxPresent(ids, manageListIDs, func(tx *sqlx.Tx, locked, open []int) (bool, error) {
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
	if err != nil {
		return 0, 0, err
	}
	return done, skipped, nil
}
