package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// GetLists retrieves lists with additional metadata like subscriber counts.
func (a *App) GetLists(c echo.Context) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	// Get the list IDs (or blanket permission) the user has access to.
	hasAllPerm, permittedIDs := user.GetPermittedLists(auth.PermTypeGet)

	// team=none shows the lists without a team (AXM).
	if c.FormValue("team") == "none" {
		var err error
		if hasAllPerm, permittedIDs, err = a.restrictToTeamless(hasAllPerm, permittedIDs); err != nil {
			return err
		}
	}

	// Minimal query simply returns the list of all lists without JOIN subscriber counts. This is fast.
	minimal, _ := strconv.ParseBool(c.FormValue("minimal"))
	if minimal {
		status := c.FormValue("status")
		res, err := a.core.GetLists("", status, hasAllPerm, permittedIDs)
		if err != nil {
			return err
		}
		if len(res) == 0 {
			return c.JSON(http.StatusOK, okResp{[]struct{}{}})
		}

		// Meta.
		total := len(res)
		out := models.PageResults{
			Results: res,
			Total:   total,
			Page:    1,
			PerPage: total,
		}

		return c.JSON(http.StatusOK, okResp{out})
	}

	// Full list query.
	var (
		query   = strings.TrimSpace(c.FormValue("query"))
		tags    = c.QueryParams()["tag"]
		orderBy = c.FormValue("order_by")
		typ     = c.FormValue("type")
		optin   = c.FormValue("optin")
		status  = c.FormValue("status")
		order   = c.FormValue("order")

		pg = a.pg.NewFromURL(c.Request().URL.Query())
	)
	res, total, err := a.core.QueryLists(query, typ, optin, status, tags, orderBy, order, hasAllPerm, permittedIDs, pg.Offset, pg.Limit)
	if err != nil {
		return err
	}

	out := models.PageResults{
		Query:   query,
		Results: res,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// GetList retrieves a single list by id.
// It's permission checked by the listPerm middleware.
func (a *App) GetList(c echo.Context) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	// Check if the user has access to the list.
	id := getID(c)
	if err := user.HasListPerm(auth.PermTypeGet, id); err != nil {
		return err
	}

	// Get the list from the DB.
	out, err := a.core.GetList(id, "")
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// CreateList handles list creation.
func (a *App) CreateList(c echo.Context) error {
	l := models.List{}
	if err := c.Bind(&l); err != nil {
		return err
	}

	// Validate.
	if !strHasLen(l.Name, 1, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.invalidName"))
	}

	tags, err := a.tagsForNewList(auth.GetUser(c), l.Tags, l.NoTeam)
	if err != nil {
		return err
	}
	l.Tags = tags

	if err := a.validateDynamicList(c, &l); err != nil {
		return err
	}
	out, err := a.core.CreateList(l)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.audit(c, auditListCreate, auditTarget{auditTargetList, out.ID, out.Name}, auditNoCount, map[string]any{
		"type": out.Type, "optin": out.Optin, "status": out.Status, "dynamic": out.AxmFilter != nil,
		"teams": teamNames(out.Tags),
	})

	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateList handles list modification.
// It's permission checked by the listPerm middleware.
func (a *App) UpdateList(c echo.Context) error {
	// Get the authenticated user.
	user := auth.GetUser(c)

	// Check if the user has access to the list.
	id := getID(c)
	if err := user.HasListPerm(auth.PermTypeManage, id); err != nil {
		return err
	}

	// Incoming params.
	var l models.List
	if err := c.Bind(&l); err != nil {
		return err
	}

	// Validate.
	if !strHasLen(l.Name, 1, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.invalidName"))
	}

	old, err := a.core.GetList(id, "")
	if err != nil {
		return err
	}
	if old.AxmFilter != nil && l.AxmFilter == nil {
		l.AxmFilter = old.AxmFilter
	}
	if l.Tags, err = a.tagsForUpdatedList(user, l.Tags, old.Tags, l.NoTeam); err != nil {
		return err
	}
	if err := a.validateDynamicList(c, &l); err != nil {
		return err
	}
	// Update the list in the DB.
	out, err := a.core.UpdateList(id, l)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.auditListUpdate(c, old, out)

	return c.JSON(http.StatusOK, okResp{out})
}

// DeleteList deletes a single list by ID.
func (a *App) DeleteList(c echo.Context) error {
	id := getID(c)

	// Check if the user has manage permission for the list.
	user := auth.GetUser(c)
	if err := user.HasListPerm(auth.PermTypeManage, id); err != nil {
		return err
	}

	// Move the list to the trash (AXM): it is deleted as before, and kept for core.TrashDays
	// days. Permissions were verified above.
	if err := a.trashLists(c, []int{id}); err != nil {
		return err
	}
	a.refreshTeamUsers()

	return c.JSON(http.StatusOK, okResp{true})
}

// DeleteLists deletes multiple lists by IDs or by query.
func (a *App) DeleteLists(c echo.Context) error {
	user := auth.GetUser(c)

	var (
		ids   []int
		query string
		all   bool
	)

	// Check for IDs in query params.
	if len(c.Request().URL.Query()["id"]) > 0 {
		var err error
		ids, err = parseStringIDs(c.Request().URL.Query()["id"])
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest,
				a.i18n.Ts("globals.messages.errorInvalidIDs", "error", err.Error()))
		}
	} else {
		// Check for query param.
		query = strings.TrimSpace(c.FormValue("query"))
		all = c.FormValue("all") == "true"
	}

	// Validate that either IDs or query is provided.
	if len(ids) == 0 && (query == "" && !all) {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("globals.messages.errorInvalidIDs", "error", "id or query required"))
	}

	// For ID deletion, check if the user has manage permission for the specific lists.
	if len(ids) > 0 {
		if err := user.HasListPerm(auth.PermTypeManage, ids...); err != nil {
			return err
		}

		// Move the lists to the trash (AXM). Permissions were verified above.
		if err := a.trashLists(c, ids); err != nil {
			return err
		}
	} else {
		// For query deletion, get the list IDs the user has manage permission for.
		hasAllPerm, permittedIDs := user.GetPermittedLists(auth.PermTypeManage)

		// Find the lists with permission filtering, then move them to the trash.
		found, err := a.core.ResolveListsToDelete(nil, query, hasAllPerm, permittedIDs)
		if err != nil {
			return err
		}
		resolved := make([]int, 0, len(found))
		for _, l := range found {
			resolved = append(resolved, l.ID)
		}
		if err := a.trashLists(c, resolved); err != nil {
			return err
		}
	}

	a.refreshTeamUsers()

	return c.JSON(http.StatusOK, okResp{true})
}

// teamNames returns the names of the teams among the list tags.
func teamNames(tags []string) []string {
	out := []string{}
	for _, t := range models.TeamTags(tags) {
		out = append(out, models.TeamName(t))
	}
	return out
}

// auditListUpdate writes what a list save changed: list.update for its name, type, opt-in,
// status, description or tags, and list.team_change for its teams.
func (a *App) auditListUpdate(c echo.Context, old, now models.List) {
	target := auditTarget{auditTargetList, now.ID, now.Name}
	changed := map[string]any{}
	if old.Name != now.Name {
		changed["name"] = map[string]any{"from": old.Name, "to": now.Name}
	}
	if old.Type != now.Type {
		changed["type"] = map[string]any{"from": old.Type, "to": now.Type}
	}
	if old.Optin != now.Optin {
		changed["optin"] = map[string]any{"from": old.Optin, "to": now.Optin}
	}
	if old.Status != now.Status {
		changed["status"] = map[string]any{"from": old.Status, "to": now.Status}
	}
	if old.Description != now.Description {
		changed["description"] = true
	}
	if !sameStrings(models.WithoutTeamTags(old.Tags), models.WithoutTeamTags(now.Tags)) {
		changed["tags"] = true
	}
	if (old.AxmFilter == nil) != (now.AxmFilter == nil) ||
		(old.AxmFilter != nil && now.AxmFilter != nil && string(*old.AxmFilter) != string(*now.AxmFilter)) {
		changed["rule"] = true
	}
	if len(changed) > 0 {
		a.audit(c, auditListUpdate, target, auditNoCount, map[string]any{"changed": changed})
	}
	if !sameStrings(models.TeamTags(old.Tags), models.TeamTags(now.Tags)) {
		a.audit(c, auditListTeamChange, target, auditNoCount, map[string]any{
			"from": teamNames(old.Tags), "to": teamNames(now.Tags),
		})
	}
}
