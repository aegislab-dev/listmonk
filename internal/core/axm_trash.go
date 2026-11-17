package core

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// TrashDays is how long a deleted list stays in the trash before the daily job deletes it
// for good.
const TrashDays = 14

// trashExpiry is when a list deleted at the given time leaves the trash.
func trashExpiry(deletedAt time.Time) time.Time {
	return deletedAt.AddDate(0, 0, TrashDays)
}

// TrashActor is who deletes a list.
type TrashActor struct {
	ID       int
	Username string
	Name     string
}

// ResolveListsToDelete returns the IDs and names of the lists a delete by IDs or by query
// reaches (the lists the caller has already been allowed to delete).
func (c *Core) ResolveListsToDelete(ids []int, query string, getAll bool, permittedIDs []int) ([]models.TrashedList, error) {
	queryStr := ""
	if len(ids) == 0 {
		queryStr = makeSearchString(query)
	}
	out := []models.TrashedList{}
	if err := c.q.AxmTrashResolveLists.Select(&out, pq.Array(ids), queryStr, getAll, pq.Array(permittedIDs)); err != nil {
		c.log.Printf("error resolving lists to delete: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.lists}", "error", pqErrMsg(err)))
	}
	return out, nil
}

// TrashLists deletes the lists and keeps them in the trash: in one transaction the lists
// (with their subscriptions and what pointed at them) are copied to the trash tables and
// then deleted with the unchanged delete-lists query. It returns each list with the number
// of subscriptions it had. A list that a running, paused or scheduled campaign sends to is
// refused (409), and so is the whole call.
func (c *Core) TrashLists(ids []int, actor TrashActor) ([]models.TrashedList, error) {
	if len(ids) == 0 {
		return []models.TrashedList{}, nil
	}

	var busy []struct {
		ListID       int    `db:"list_id"`
		CampaignID   int    `db:"campaign_id"`
		CampaignName string `db:"campaign_name"`
	}
	if err := c.q.AxmTrashBusyLists.Select(&busy, pq.Array(ids)); err != nil {
		c.log.Printf("error checking campaigns of lists: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.lists}", "error", pqErrMsg(err)))
	}
	if len(busy) > 0 {
		return nil, echo.NewHTTPError(http.StatusConflict, c.i18n.Ts("lists.trash.busy", "name", busy[0].CampaignName))
	}

	tx, err := c.db.BeginTxx(context.Background(), nil)
	if err != nil {
		c.log.Printf("error starting a list delete: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.lists}", "error", pqErrMsg(err)))
	}
	defer tx.Rollback()

	var by any
	if actor.ID > 0 {
		by = actor.ID
	}
	out := []models.TrashedList{}
	if err := tx.Stmtx(c.q.AxmTrashSaveLists).Select(&out, pq.Array(ids), by, actor.Username, actor.Name); err != nil {
		c.log.Printf("error copying lists to the trash: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.lists}", "error", pqErrMsg(err)))
	}
	saved := make([]int, 0, len(out))
	for _, l := range out {
		saved = append(saved, l.ID)
	}
	// Never run the delete with an empty list of IDs: the query reads that as "all lists".
	if len(saved) == 0 {
		return out, nil
	}
	if _, err := tx.Stmtx(c.q.AxmTrashClearSubs).Exec(pq.Array(saved)); err != nil {
		return nil, c.trashErr("clearing the trash of lists", err)
	}
	if _, err := tx.Stmtx(c.q.AxmTrashSaveSubs).Exec(pq.Array(saved)); err != nil {
		return nil, c.trashErr("copying subscriptions to the trash", err)
	}

	// The unchanged delete query, by the IDs that were copied.
	if _, err := tx.Stmtx(c.q.DeleteLists).Exec(pq.Array(saved), "", true, pq.Array([]int{})); err != nil {
		// A list that a dynamic list excludes people of cannot go (migration axm.7).
		if pe, ok := err.(*pq.Error); ok && pe.Code == axmExcludeListInUse {
			return nil, echo.NewHTTPError(http.StatusBadRequest, pe.Message)
		}
		return nil, c.trashErr("deleting lists", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, c.trashErr("committing a list delete", err)
	}
	return out, nil
}

func (c *Core) trashErr(what string, err error) error {
	c.log.Printf("error %s: %v", what, err)
	return echo.NewHTTPError(http.StatusInternalServerError,
		c.i18n.Ts("globals.messages.errorDeleting", "name", "{globals.terms.lists}", "error", pqErrMsg(err)))
}

// GetTrash returns the deleted lists, newest first. all=false limits them to the ones
// deleted by userID.
func (c *Core) GetTrash(all bool, userID int) ([]models.DeletedList, error) {
	out := []models.DeletedList{}
	if err := c.q.AxmGetListTrash.Select(&out, all, userID); err != nil {
		c.log.Printf("error fetching the list trash: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{lists.trash.title}", "error", pqErrMsg(err)))
	}
	now := time.Now()
	for i := range out {
		out[i].ExpiresAt = trashExpiry(out[i].DeletedAt)
		left := out[i].ExpiresAt.Sub(now)
		out[i].DaysLeft = max(0, int((left+24*time.Hour-1)/(24*time.Hour)))
		if out[i].Tags == nil {
			out[i].Tags = types.JSONText("[]")
		}
	}
	return out, nil
}

// TrashOwner returns who deleted a list in the trash (userID 0 = nobody known). ok is false
// when the list is not in the trash.
func (c *Core) TrashOwner(id int) (userID int, name string, ok bool, err error) {
	var row struct {
		By   sql.NullInt64 `db:"deleted_by"`
		Name string        `db:"name"`
	}
	if err := c.db.Get(&row, `SELECT deleted_by, name FROM axm_deleted_lists WHERE list_id = $1`, id); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", false, nil
		}
		return 0, "", false, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{lists.trash.title}", "error", pqErrMsg(err)))
	}
	return int(row.By.Int64), row.Name, true, nil
}

// RestoreList puts a deleted list back with the same ID and UUID: its row, the subscriptions
// of the subscribers who still exist, its campaign links, list role rows and draw records.
// It does not bring back subscribers that were deleted meanwhile.
func (c *Core) RestoreList(id int) (models.RestoredList, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "{lists.trash.title}"))

	tx, err := c.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	defer tx.Rollback()

	var row struct {
		ListID        int            `db:"list_id"`
		UUID          string         `db:"uuid"`
		Name          string         `db:"name"`
		List          types.JSONText `db:"list"`
		SubCount      int            `db:"sub_count"`
		DeletedBy     sql.NullInt64  `db:"deleted_by"`
		CampaignLinks pq.Int64Array  `db:"campaign_links"`
		Roles         types.JSONText `db:"roles"`
		Draws         pq.Int64Array  `db:"draws"`
		SourceDraws   pq.Int64Array  `db:"source_draws"`
		DrawCampaigns pq.Int64Array  `db:"draw_campaigns"`
	}
	if err := tx.Stmtx(c.q.AxmTrashLock).Get(&row, id); err != nil {
		if err == sql.ErrNoRows {
			return models.RestoredList{}, notFound
		}
		return models.RestoredList{}, c.restoreErr(err)
	}

	if _, err := tx.Stmtx(c.q.AxmTrashRestoreList).Exec(string(row.List)); err != nil {
		if pe, ok := err.(*pq.Error); ok && pe.Code == "23505" {
			return models.RestoredList{}, echo.NewHTTPError(http.StatusConflict, c.i18n.T("lists.trash.exists"))
		}
		return models.RestoredList{}, c.restoreErr(err)
	}

	// The subscriptions go in before the draw records come back, so that an unsubscribed row
	// is not passed on to the source list again (it was when it first happened).
	res, err := tx.Stmtx(c.q.AxmTrashRestoreSubs).Exec(id)
	if err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	subs, _ := res.RowsAffected()

	res, err = tx.Stmtx(c.q.AxmTrashRestoreCamps).Exec(id, row.CampaignLinks)
	if err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	camps, _ := res.RowsAffected()

	if _, err := tx.Stmtx(c.q.AxmTrashRestoreRoles).Exec(id, string(row.Roles)); err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	if _, err := tx.Stmtx(c.q.AxmTrashRestoreDraws).Exec(id, row.Draws, row.SourceDraws, row.DrawCampaigns); err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	if _, err := tx.Stmtx(c.q.AxmTrashDrop).Exec(id); err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	if err := tx.Commit(); err != nil {
		return models.RestoredList{}, c.restoreErr(err)
	}
	return models.RestoredList{ID: id, Subscriptions: int(subs), Campaigns: int(camps)}, nil
}

func (c *Core) restoreErr(err error) error {
	c.log.Printf("error restoring a list: %v", err)
	return echo.NewHTTPError(http.StatusInternalServerError,
		c.i18n.Ts("globals.messages.errorUpdating", "name", "{globals.terms.list}", "error", pqErrMsg(err)))
}

// PurgeTrash deletes the lists that have been in the trash longer than TrashDays and
// returns them.
func (c *Core) PurgeTrash() ([]models.TrashedList, error) {
	out := []models.TrashedList{}
	if err := c.q.AxmTrashPurge.Select(&out, time.Now().AddDate(0, 0, -TrashDays)); err != nil {
		return nil, fmt.Errorf("purging the list trash: %w", err)
	}
	return out, nil
}
