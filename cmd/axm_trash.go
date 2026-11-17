package main

import (
	"net/http"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// A deleted list stays in the trash for core.TrashDays days (migration axm.11). Whoever may
// manage all lists sees and restores every deleted list; everybody else sees and restores the
// lists they deleted themselves.

func trashActor(u auth.User) core.TrashActor {
	return core.TrashActor{ID: u.ID, Username: u.Username, Name: u.Name}
}

// trashLists deletes lists into the trash and writes an audit row for each of them.
func (a *App) trashLists(c echo.Context, ids []int) error {
	user := auth.GetUser(c)
	out, err := a.core.TrashLists(ids, trashActor(user))
	if err != nil {
		return err
	}
	for _, l := range out {
		a.audit(c, auditListDelete, auditTarget{auditTargetList, l.ID, l.Name}, l.SubCount, map[string]any{
			"trash_days": core.TrashDays,
		})
	}
	return nil
}

// GetListTrash returns the lists in the trash that the user may see.
func (a *App) GetListTrash(c echo.Context) error {
	user := auth.GetUser(c)
	out, err := a.core.GetTrash(user.HasPerm(auth.PermListManageAll), user.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{struct {
		Results   []models.DeletedList `json:"results"`
		TrashDays int                  `json:"trash_days"`
	}{out, core.TrashDays}})
}

// RestoreList puts a list from the trash back.
func (a *App) RestoreList(c echo.Context) error {
	id := getID(c)
	user := auth.GetUser(c)
	by, name, ok, err := a.core.TrashOwner(id)
	if err != nil {
		return err
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, a.i18n.Ts("globals.messages.notFound", "name", "{lists.trash.title}"))
	}
	if !user.HasPerm(auth.PermListManageAll) && by != user.ID {
		return echo.NewHTTPError(http.StatusForbidden, a.i18n.Ts("globals.messages.permissionDenied", "name", auth.PermListManageAll))
	}

	out, err := a.core.RestoreList(id)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.audit(c, auditListRestore, auditTarget{auditTargetList, id, name}, out.Subscriptions, map[string]any{
		"campaigns": out.Campaigns,
	})
	return c.JSON(http.StatusOK, okResp{out})
}
