package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// AddressCheckAudience returns how many subscribers a campaign to the lists would reach
// and, unless there are more than max, their IDs and addresses. Both are read at one
// snapshot. A row is marked direct when the subscriber is subscribed to one of targets.
func (c *Core) AddressCheckAudience(ctx context.Context, lists, targets []int, max int) (int, []models.AddressCheckRow, error) {
	fail := func(err error) (int, []models.AddressCheckRow, error) {
		c.log.Printf("error reading the audience for an address check: %v", err)
		return 0, nil, echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("addressCheck.error")).SetInternal(err)
	}
	tx, err := c.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()

	listIDs := make(pq.Int64Array, len(lists))
	for i, id := range lists {
		listIDs[i] = int64(id)
	}
	var total int
	if err := tx.Stmtx(c.q.AxmAddressCheckCount).GetContext(ctx, &total, listIDs); err != nil {
		return fail(err)
	}
	if total == 0 || total > max {
		return total, nil, nil
	}
	rows := make([]models.AddressCheckRow, 0, total)
	if err := tx.Stmtx(c.q.AxmAddressCheckAudience).SelectContext(ctx, &rows, listIDs, pq.Array(targets)); err != nil {
		return fail(err)
	}
	return len(rows), rows, nil
}

// AddressCheckDynamicFilters returns the rules of the dynamic lists among the given lists.
func (c *Core) AddressCheckDynamicFilters(ctx context.Context, lists []int) (map[int]DynamicFilter, error) {
	out := map[int]DynamicFilter{}
	if len(lists) == 0 {
		return out, nil
	}
	var rows []models.AddressCheckDynamic
	if err := c.q.AxmAddressCheckDynamic.SelectContext(ctx, &rows, pq.Array(lists)); err != nil {
		return nil, c.addressCheckError(err)
	}
	for _, r := range rows {
		var f DynamicFilter
		if err := json.Unmarshal(r.Filter, &f); err != nil {
			return nil, c.addressCheckError(err)
		}
		out[r.ID] = f
	}
	return out, nil
}

// AddressCheckLinked returns the other ordinary lists that an unsubscribe from the given
// ordinary lists can also reach, because the lists are tied by a draw.
func (c *Core) AddressCheckLinked(ctx context.Context, lists []int) ([]models.AddressCheckList, error) {
	out := []models.AddressCheckList{}
	if len(lists) == 0 {
		return out, nil
	}
	if err := c.q.AxmAddressCheckLinked.SelectContext(ctx, &out, pq.Array(lists)); err != nil {
		return nil, c.addressCheckError(err)
	}
	return out, nil
}

func (c *Core) addressCheckError(err error) error {
	c.log.Printf("error reading lists for an address check: %v", err)
	return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("addressCheck.error")).SetInternal(err)
}
