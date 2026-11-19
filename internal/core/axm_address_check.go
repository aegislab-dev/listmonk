package core

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// AddressCheckAudience returns how many subscribers a campaign to the lists would reach
// and, unless there are more than max, their IDs and addresses. Both are read at one
// snapshot.
func (c *Core) AddressCheckAudience(ctx context.Context, lists []int, max int) (int, []models.AddressCheckRow, error) {
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
	if err := tx.Stmtx(c.q.AxmAddressCheckAudience).SelectContext(ctx, &rows, listIDs); err != nil {
		return fail(err)
	}
	return len(rows), rows, nil
}
