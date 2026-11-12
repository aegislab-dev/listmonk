package main

import (
	"encoding/json"
	"net/http"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

func (a *App) validateDynamicList(c echo.Context, l *models.List) error {
	if l.AxmFilter == nil {
		return nil
	}
	f, err := core.ValidateDynamicFilter(*l.AxmFilter)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("lists.dynamic.invalidFilter", "error", err.Error()))
	}
	if err := a.checkDynamicBasePerm(c, f); err != nil {
		return err
	}
	return a.core.ValidateDynamicBase(f)
}

func (a *App) checkDynamicBasePerm(c echo.Context, f core.DynamicFilter) error {
	user := auth.GetUser(c)
	if f.Base.Type == "all" {
		if !user.HasPerm(auth.PermListGetAll) {
			return auth.ErrPermDenied
		}
		return nil
	}
	return user.HasListPerm(auth.PermTypeGet, f.Base.Lists...)
}

// PreviewDynamicList returns only a count and requires manage access to its destination.
func (a *App) PreviewDynamicList(c echo.Context) error {
	var req struct {
		Filter json.RawMessage `json:"filter"`
		ListID int             `json:"list_id"`
	}
	if err := c.Bind(&req); err != nil {
		return err
	}
	user := auth.GetUser(c)
	if req.ListID < 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid list ID")
	}
	if req.ListID > 0 {
		if err := user.HasListPerm(auth.PermTypeManage, req.ListID); err != nil {
			return err
		}
		l, err := a.core.GetList(req.ListID, "")
		if err != nil {
			return err
		}
		if l.AxmFilter == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "List is not dynamic")
		}
	} else if !user.HasPerm(auth.PermListManageAll) {
		return auth.ErrPermDenied
	}
	f, err := core.ValidateDynamicFilter(req.Filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("lists.dynamic.invalidFilter", "error", err.Error()))
	}
	if err := a.checkDynamicBasePerm(c, f); err != nil {
		return err
	}
	if err := a.core.ValidateDynamicBase(f); err != nil {
		return err
	}
	n, notNumber, err := a.core.PreviewDynamicFilter(f, req.ListID)
	if err != nil {
		a.log.Printf("dynamic list preview: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, a.i18n.T("lists.dynamic.previewError"))
	}
	return c.JSON(http.StatusOK, okResp{map[string]int{"count": n, "not_number": notNumber}})
}

func (a *App) rejectDynamicTargets(ids []int) error {
	dynamicIDs, err := a.core.DynamicListIDs(ids)
	if err != nil {
		return err
	}
	if len(dynamicIDs) > 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.dynamic.invalidTarget"))
	}
	return nil
}
