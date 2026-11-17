package main

import (
	"net/http"
	"strconv"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/labstack/echo/v4"
)

// Column presets (LST-17): the attribute columns of the subscribers table. Two are built in
// (creator, b2b) and read-only; the rest people with lists:manage_all make, edit and delete
// here. Everybody who is logged in reads them (the list form and the subscribers page need
// the columns); only the one who may manage all lists also sees the names of the lists that
// use a preset, as list names of other teams are not theirs to read.

type columnPresetReq struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// GetColumnPresets returns the presets with the number of lists that use each.
func (a *App) GetColumnPresets(c echo.Context) error {
	user := auth.GetUser(c)
	out, err := a.core.GetColumnPresetUsage(user.HasPerm(auth.PermListManageAll))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// CreateColumnPreset makes a preset.
func (a *App) CreateColumnPreset(c echo.Context) error {
	var req columnPresetReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	out, err := a.core.CreateColumnPreset(req.Name, req.Columns)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// presetRowID reads the :id of a preset people made. Built-in presets have no row, so they
// cannot be edited or deleted: their IDs are not numbers.
func presetRowID(c echo.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "invalid ID")
	}
	return id, nil
}

// UpdateColumnPreset changes a preset's name and columns.
func (a *App) UpdateColumnPreset(c echo.Context) error {
	id, err := presetRowID(c)
	if err != nil {
		return err
	}
	var req columnPresetReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	out, err := a.core.UpdateColumnPreset(id, req.Name, req.Columns)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// DeleteColumnPreset deletes a preset; the lists that used it go to the default preset.
func (a *App) DeleteColumnPreset(c echo.Context) error {
	id, err := presetRowID(c)
	if err != nil {
		return err
	}
	n, err := a.core.DeleteColumnPreset(id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{struct {
		Lists   int    `json:"lists"`
		Default string `json:"default"`
	}{n, core.DefaultColumnPreset}})
}
