package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/labstack/echo/v4"
)

// GetUserRoles retrieves roles.
func (a *App) GetUserRoles(c echo.Context) error {
	// Get all roles.
	out, err := a.core.GetRoles()
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// GeListRoles retrieves roles.
func (a *App) GeListRoles(c echo.Context) error {
	// Get all roles.
	out, err := a.core.GetListRoles()
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// CreateUserRole handles role creation.
func (a *App) CreateUserRole(c echo.Context) error {
	var r auth.Role
	if err := c.Bind(&r); err != nil {
		return err
	}
	if err := a.validateUserRole(r); err != nil {
		return err
	}

	// Create the role in the DB.
	out, err := a.core.CreateRole(r)
	if err != nil {
		return err
	}
	a.auditRoleCreated(c, out.ID, roleInfo{name: out.Name.String, kind: auth.RoleTypeUser, perms: out.Permissions})

	return c.JSON(http.StatusOK, okResp{out})
}

// CreateListRole handles role creation.
func (a *App) CreateListRole(c echo.Context) error {
	var r auth.ListRole
	if err := c.Bind(&r); err != nil {
		return err
	}
	if err := a.validateListRole(r); err != nil {
		return err
	}

	// Create the role in the DB.
	out, err := a.core.CreateListRole(r)
	if err != nil {
		return err
	}
	a.auditRoleCreated(c, out.ID, listRoleInfo(out))

	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateUserRole handles role modification.
func (a *App) UpdateUserRole(c echo.Context) error {
	id := getID(c)

	// ID 1 is reserved for the Super Admin user role.
	if id == auth.SuperAdminRoleID {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
	}

	// Incoming params.
	var r auth.Role
	if err := c.Bind(&r); err != nil {
		return err
	}
	if err := a.validateUserRole(r); err != nil {
		return err
	}
	// AXM (LST-68): a team's user role keeps no team-wide permission. The check and the save
	// are one turn of the team-role lock.
	unlock, err := a.core.LockTeamRoles()
	if err != nil {
		return err
	}
	defer unlock()
	if err := a.guardTeamUserRole(id, r); err != nil {
		return err
	}

	// Validate.
	r.Name.String = strings.TrimSpace(r.Name.String)

	// Update the role in the DB.
	before := a.roleInfoOf(id)
	out, err := a.core.UpdateUserRole(id, r)
	if err != nil {
		return err
	}
	a.auditRoleUpdated(c, id, before, roleInfo{found: true, name: out.Name.String, kind: auth.RoleTypeUser, perms: out.Permissions})

	// Cache API tokens for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(a.core, a.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateListRole handles role modification.
func (a *App) UpdateListRole(c echo.Context) error {
	// Get the role ID.
	id := getID(c)

	// ID 1 is reserved for the Super Admin user role.
	if id == auth.SuperAdminRoleID {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
	}

	// Incoming params.
	var r auth.ListRole
	if err := c.Bind(&r); err != nil {
		return err
	}

	if err := a.validateListRole(r); err != nil {
		return err
	}

	// A team role is changed on the teams screen.
	if a.core.TeamTagOfRole(id) != "" {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("teams.managedByTeams"))
	}

	// Validate.
	r.Name.String = strings.TrimSpace(r.Name.String)

	// Update the role in the DB.
	before := a.roleInfoOf(id)
	out, err := a.core.UpdateListRole(id, r)
	if err != nil {
		return err
	}
	a.auditRoleUpdated(c, id, before, listRoleInfo(out))

	// Cache API tokens for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(a.core, a.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// DeleteRole handles (user|list) role deletion.
func (a *App) DeleteRole(c echo.Context) error {
	// Get the role ID.
	id := getID(c)

	// ID 1 is reserved for the Super Admin user role.
	if id == auth.SuperAdminRoleID {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
	}

	// A team role is deleted on the teams screen. Deleting a list role would also delete the
	// accounts that use it (users.list_role_id cascades), so a role in use is refused.
	if a.core.TeamTagOfRole(int(id)) != "" {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("teams.managedByTeams"))
	}
	if n, err := a.core.CountListRoleUsers(int(id)); err != nil {
		return err
	} else if n > 0 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("users.cantDeleteRole"))
	}

	// Delete the role from the DB.
	before := a.roleInfoOf(int(id))
	if err := a.core.DeleteRole(int(id)); err != nil {
		return err
	}
	a.audit(c, auditRoleDelete, auditTarget{auditTargetRole, int(id), before.name}, auditNoCount, map[string]any{"kind": before.kind})

	// Cache API tokens for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(a.core, a.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{true})
}

func (a *App) validateUserRole(r auth.Role) error {
	if !strHasLen(r.Name.String, 1, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "name"))
	}

	for _, p := range r.Permissions {
		if _, ok := a.cfg.Permissions[p]; !ok {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", fmt.Sprintf("permission: %s", p)))
		}
	}

	return nil
}

func (a *App) validateListRole(r auth.ListRole) error {
	if !strHasLen(r.Name.String, 1, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", "name"))
	}

	for _, l := range r.Lists {
		for _, p := range l.Permissions {
			if p != auth.PermListGet && p != auth.PermListManage {
				return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("globals.messages.invalidFields", "name", fmt.Sprintf("list permission: %s", p)))
			}
		}
	}

	return nil
}
