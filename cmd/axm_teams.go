package main

import (
	"net/http"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// A team is a list role with a team tag; the database keeps the role's lists equal to the
// lists that carry the tag (migration v6.2.1-axm.8). A team member is a user whose list role
// is a team role. The rules below keep the tag honest: members always make lists of their
// own team, and only people who may manage all lists change a list's team.

// teamTagOf returns the team tag of the user's list role, "" when it is not a team role.
func (a *App) teamTagOf(user auth.User) string {
	if user.ListRoleID == nil {
		return ""
	}
	return a.core.TeamTagOfRole(*user.ListRoleID)
}

// canMakeLists tells whether the user may make lists: lists:manage_all, or being a team member.
func (a *App) canMakeLists(user auth.User) bool {
	return user.HasPerm(auth.PermListManageAll) || a.teamTagOf(user) != ""
}

// listMaker guards a route that makes lists (a list, a drawn list, a preview of one).
func (a *App) listMaker(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, ok := c.Get(auth.UserHTTPCtxKey).(auth.User)
		if !ok {
			return echo.NewHTTPError(http.StatusForbidden, "invalid session")
		}
		if !a.canMakeLists(user) {
			return echo.NewHTTPError(http.StatusForbidden, "permission denied: "+auth.PermListManageAll)
		}
		return next(c)
	}
}

// refreshTeamUsers reloads the cached API users after a change that moves lists in or out of
// a team role (API users keep their permissions in memory).
func (a *App) refreshTeamUsers() {
	if a.auth == nil || !a.core.HasTeamRoles() {
		return
	}
	if _, err := cacheUsers(a.core, a.auth); err != nil {
		a.log.Printf("error caching users after a team change: %v", err)
	}
}

// cleanTags turns whitespace into dashes the way list tags are stored.
func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.Join(strings.Fields(t), "-"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// knownTeamTags returns the tags of the teams that exist.
func (a *App) knownTeamTags() (map[string]bool, error) {
	teams, err := a.core.GetTeams()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, t := range teams {
		out[t.Tag] = true
	}
	return out, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

// checkAdminTeams checks the team tags an admin gives a list: they must name existing teams
// (a tag without a team makes a list nobody but admins can see), and a list gets at least
// one team unless the admin says noTeam. It does nothing while no team exists yet.
func (a *App) checkAdminTeams(teams []string, noTeam bool) error {
	if !a.core.HasTeamRoles() {
		return nil
	}
	if len(teams) == 0 {
		if noTeam {
			return nil
		}
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("lists.team.required"))
	}
	known, err := a.knownTeamTags()
	if err != nil {
		return err
	}
	for _, t := range teams {
		if !known[t] {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("lists.team.unknown", "name", models.TeamName(t)))
		}
	}
	return nil
}

// tagsForNewList decides the tags of a list a user makes. A team member's list belongs to
// the member's team (whatever team tags were sent are dropped); an admin must name a team.
func (a *App) tagsForNewList(user auth.User, tags []string, noTeam bool) ([]string, error) {
	tags = cleanTags(tags)
	if user.HasPerm(auth.PermListManageAll) {
		return tags, a.checkAdminTeams(models.TeamTags(tags), noTeam)
	}
	own := a.teamTagOf(user)
	if own == "" {
		return nil, auth.ErrPermDenied
	}
	return append(models.WithoutTeamTags(tags), own), nil
}

// tagsForUpdatedList decides the tags of a list a user saves. Only an admin changes its
// teams; for everybody else the list keeps the teams it has.
func (a *App) tagsForUpdatedList(user auth.User, tags, old []string, noTeam bool) ([]string, error) {
	tags = cleanTags(tags)
	if !user.HasPerm(auth.PermListManageAll) {
		return append(models.WithoutTeamTags(tags), models.TeamTags(old)...), nil
	}
	teams, oldTeams := models.TeamTags(tags), models.TeamTags(old)
	if sameStrings(teams, oldTeams) {
		return tags, nil
	}
	return tags, a.checkAdminTeams(teams, noTeam)
}

// GetTeams returns the teams with their members (admin screen).
func (a *App) GetTeams(c echo.Context) error {
	out, err := a.core.GetTeams()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{out})
}

type teamReq struct {
	Name           string `json:"name"`
	UserRoleID     int    `json:"user_role_id"`
	ApplyToMembers bool   `json:"apply_to_members"`
}

// CreateTeam makes a team.
func (a *App) CreateTeam(c echo.Context) error {
	var req teamReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	out, err := a.core.CreateTeam(req.Name, req.UserRoleID)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.audit(c, auditTeamCreate, auditTarget{auditTargetTeam, out.ID, out.Name}, auditNoCount, nil)
	return c.JSON(http.StatusOK, okResp{out})
}

// UpdateTeam renames a team and sets its default user role.
func (a *App) UpdateTeam(c echo.Context) error {
	var req teamReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	before, _ := a.core.GetTeam(getID(c))
	out, err := a.core.UpdateTeam(getID(c), req.Name, req.UserRoleID, req.ApplyToMembers)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.auditTeamUpdated(c, before, out)
	return c.JSON(http.StatusOK, okResp{out})
}

// DeleteTeam deletes an empty team.
func (a *App) DeleteTeam(c echo.Context) error {
	before, _ := a.core.GetTeam(getID(c))
	if err := a.core.DeleteTeam(getID(c)); err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.audit(c, auditTeamDelete, auditTarget{auditTargetTeam, getID(c), before.Name}, auditNoCount, nil)
	return c.JSON(http.StatusOK, okResp{true})
}

// SetTeamMembers sets who is in the team.
func (a *App) SetTeamMembers(c echo.Context) error {
	var req struct {
		UserIDs []int `json:"user_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return err
	}
	before, _ := a.core.GetTeam(getID(c))
	out, err := a.core.SetTeamMembers(getID(c), req.UserIDs)
	if err != nil {
		return err
	}
	a.refreshTeamUsers()
	a.auditTeamMembers(c, before, out)
	return c.JSON(http.StatusOK, okResp{out})
}

// GetListTeams returns the teams on the lists the user may read. Whoever reads all lists
// also gets the number of lists without a team.
func (a *App) GetListTeams(c echo.Context) error {
	user := auth.GetUser(c)
	all, ids := user.GetPermittedLists(auth.PermTypeGet)
	teams, err := a.core.VisibleTeams(all, ids)
	if err != nil {
		return err
	}
	if !user.HasPerm(auth.PermRolesGet) {
		for i := range teams {
			teams[i].RoleID, teams[i].RoleName = nil, nil
		}
	}
	out := struct {
		Teams    []models.TeamCount `json:"teams"`
		Teamless *int               `json:"teamless"`
	}{Teams: teams}
	if all {
		none, err := a.core.TeamlessListIDs()
		if err != nil {
			return err
		}
		n := len(none)
		out.Teamless = &n
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// restrictToTeamless narrows the lists a user reads to those without a team tag
// (GET /api/lists?team=none).
func (a *App) restrictToTeamless(all bool, ids []int) (bool, []int, error) {
	none, err := a.core.TeamlessListIDs()
	if err != nil {
		return false, nil, err
	}
	if all {
		return false, none, nil
	}
	allowed := map[int]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	out := []int{}
	for _, id := range none {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return false, out, nil
}

// attachOtherTeams marks the subscribers that are also on lists of other teams, for users who
// do not read all lists.
func (a *App) attachOtherTeams(user auth.User, subs []models.Subscriber) {
	if len(subs) == 0 || user.HasPerm(auth.PermListGetAll) || user.HasPerm(auth.PermListManageAll) {
		return
	}
	_, readable := user.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage)
	ids := make([]int, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	m, err := a.core.OtherTeams(ids, readable)
	if err != nil {
		a.log.Printf("error reading other teams of subscribers: %v", err)
		return
	}
	for i := range subs {
		subs[i].OtherTeams = m[subs[i].ID]
	}
}

// guardTeamUserRole refuses a save of the user role id (roles screen) that would give a team
// member a team-wide permission (core.TeamWidePerms), LST-68: the role is the default user
// role of a team or a member uses it, and the permissions to save include such a permission.
// A save without them passes, so a wide role is mended by this very screen.
func (a *App) guardTeamUserRole(id int, r auth.Role) error {
	wide := core.WideOf(r.Permissions)
	if len(wide) == 0 {
		return nil
	}
	uses, err := a.core.TeamRoleUses(id)
	if err != nil {
		return err
	}
	if len(uses) == 0 {
		return nil
	}
	teams := make([]string, len(uses))
	for i, u := range uses {
		teams[i] = u.Team
	}
	return echo.NewHTTPError(http.StatusBadRequest,
		a.i18n.Ts("teams.roleInUseByTeam", "team", strings.Join(teams, ", "), "perms", strings.Join(wide, ", ")))
}

// guardTeamMemberRole refuses a save of a user (users screen) that makes a team member of
// somebody whose user role has a team-wide permission or is the super admin role (LST-68).
// id is 0 for a new user. A save that changes neither the user role nor the list role
// passes, so that a member of a team that is already wide can still change their name or
// password.
func (a *App) guardTeamMemberRole(id int, u auth.User) error {
	if u.ListRoleID == nil || a.core.TeamTagOfRole(*u.ListRoleID) == "" {
		return nil
	}
	if id > 0 {
		cur, err := a.core.GetUser(id, "", "")
		if err == nil && cur.UserRole.ID == u.UserRoleID && cur.ListRoleID != nil && *cur.ListRoleID == *u.ListRoleID {
			return nil
		}
	}
	perms, super, err := a.core.WidePermsOfRole(u.UserRoleID)
	if err != nil {
		return err
	}
	if !super && len(perms) == 0 {
		return nil
	}
	if super {
		perms = []string{"Super Admin"}
	}
	return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("teams.memberWideRole", "perms", strings.Join(perms, ", ")))
}

// The campaign write rule (LST-68). Upstream reads "get or manage" access to a list as one:
// GetPermittedLists(Get|Manage) answers "all" for a user with lists:get_all, and so a member who
// may only read every list (lists:get_all, no lists:manage_all) could edit, delete, send and
// aim campaigns at the lists of other teams. Writing to campaigns follows the lists the user
// may really use: everything for lists:manage_all and the super admin, otherwise the lists of
// their own list role (a team's lists), whatever lists:get_all lets them read.

// campaignWriteLists is GetPermittedLists(Get|Manage) for a campaign write.
func campaignWriteLists(user auth.User) (bool, []int) {
	all, ids := user.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage)
	if !all || user.UserRole.ID == auth.SuperAdminRoleID || user.HasPerm(auth.PermListManageAll) {
		return all, ids
	}
	// lists:get_all only: the lists of the list role.
	seen := map[int]bool{}
	own := []int{}
	for _, id := range append(append([]int{}, user.GetListIDs...), user.ManageListIDs...) {
		if !seen[id] {
			seen[id] = true
			own = append(own, id)
		}
	}
	return false, own
}

// campaignWriteListIDs is FilterListsByPerm(Get|Manage) for the lists a campaign is saved with.
func campaignWriteListIDs(user auth.User, ids []int) []int {
	all, own := campaignWriteLists(user)
	if all || !user.HasPerm(auth.PermListGetAll) {
		return user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, ids)
	}
	allowed := map[int]bool{}
	for _, id := range own {
		allowed[id] = true
	}
	out := []int{}
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}
