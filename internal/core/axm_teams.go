package core

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

// TeamWidePerms are the permissions a team member must not have (LST-68): each lets its owner
// change other teams' lists or campaigns (lists:manage_all edits any list and unlocks the
// subscribers of other teams, campaigns:manage_all edits any campaign). Reading other teams'
// data stays allowed: subscribers:get_all and the bounce screen are how members work across teams
// (decision of 2026-11-25). lists:get_all and campaigns:get_all only widen reading (see FORK.md).
// The frontend keeps the same list in frontend/src/teams.js.
var TeamWidePerms = []string{"lists:manage_all", "campaigns:manage_all"}

// WideOf returns the team-wide permissions among perms, in the order of TeamWidePerms.
func WideOf(perms []string) []string {
	have := map[string]bool{}
	for _, p := range perms {
		have[p] = true
	}
	out := []string{}
	for _, p := range TeamWidePerms {
		if have[p] {
			out = append(out, p)
		}
	}
	return out
}

// teamRoleFact is what a user role is worth for a team (axm-user-role-facts).
type teamRoleFact struct {
	ID         int            `db:"id"`
	SuperAdmin bool           `db:"super_admin"`
	Wide       bool           `db:"wide"`
	WidePerms  pq.StringArray `db:"wide_perms"`
}

// teamRoleFacts reads the facts of the user roles with the given IDs.
func (c *Core) teamRoleFacts(tx *sqlx.Tx, ids []int) ([]teamRoleFact, error) {
	var facts []teamRoleFact
	if err := tx.Stmtx(c.q.AxmUserRoleFacts).Select(&facts, pq.Array(ids), pq.Array(TeamWidePerms)); err != nil {
		return nil, c.teamFail(err)
	}
	return facts, nil
}

// teamRoleUse is a team that uses a user role (LST-68): as the default user role of the
// team, or by members.
type teamRoleUse struct {
	Team      string `db:"team"`
	IsDefault bool   `db:"is_default"`
	Members   int    `db:"members"`
}

// teamRoleLockKey serializes changes to team defaults, members and user role permissions.
const teamRoleLockKey = 741968

// beginTeamRoleTx keeps the lock, validation and writes on one connection. Read committed
// lets each validation see the previous lock holder's commit after waiting for the lock.
func (c *Core) beginTeamRoleTx() (*sqlx.Tx, error) {
	tx, err := c.db.BeginTxx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, c.teamFail(err)
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, teamRoleLockKey); err != nil {
		tx.Rollback()
		return nil, c.teamFail(err)
	}
	return tx, nil
}

// guardTeamUserRole rejects a write-wide role used as a team default or by team members.
func (c *Core) guardTeamUserRole(tx *sqlx.Tx, id int, role auth.Role) error {
	wide := WideOf(role.Permissions)
	if len(wide) == 0 {
		return nil
	}
	var uses []teamRoleUse
	if err := tx.Stmtx(c.q.AxmUserRoleTeamUse).Select(&uses, id); err != nil {
		return c.teamFail(err)
	}
	if len(uses) == 0 {
		return nil
	}
	teams := make([]string, len(uses))
	for i, u := range uses {
		teams[i] = u.Team
	}
	return echo.NewHTTPError(http.StatusBadRequest,
		c.i18n.Ts("teams.roleInUseByTeam", "team", strings.Join(teams, ", "), "perms", strings.Join(wide, ", ")))
}

// guardTeamMemberRole checks the pair of roles that the write will store. A zero user role
// or a zero list role keeps the previous role in an update. A nil list role clears it.
// Keeping both roles allows profile edits by members of existing wide roles.
func (c *Core) guardTeamMemberRole(tx *sqlx.Tx, u, previous auth.User) error {
	userRole, listRole := u.UserRoleID, u.ListRoleID
	if previous.ID != 0 {
		if userRole == 0 {
			userRole = previous.UserRole.ID
		}
		if listRole != nil && *listRole == 0 {
			listRole = previous.ListRoleID
		}
	}
	if listRole == nil || *listRole <= 0 {
		return nil
	}
	if userRole == previous.UserRole.ID && previous.ListRoleID != nil && *listRole == *previous.ListRoleID {
		return nil
	}
	team, found, err := c.getTeamRole(tx.Stmtx(c.q.AxmGetTeamRole), *listRole)
	if err != nil {
		return err
	}
	if !found || team.Tag == nil {
		return nil
	}
	facts, err := c.teamRoleFacts(tx, []int{userRole})
	if err != nil {
		return err
	}
	if len(facts) == 0 || (!facts[0].SuperAdmin && !facts[0].Wide) {
		return nil
	}
	perms := facts[0].WidePerms
	if facts[0].SuperAdmin {
		perms = []string{"Super Admin"}
	}
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.Ts("teams.memberWideRole", "perms", strings.Join(perms, ", ")))
}

// CampaignListsWithin tells whether the campaign sends to at least one of the lists and to
// none outside them (LST-68).
func (c *Core) CampaignListsWithin(id int, listIDs []int) (bool, error) {
	var ok bool
	if listIDs == nil {
		listIDs = []int{}
	}
	if err := c.q.AxmCampaignListsWithin.Get(&ok, id, pq.Array(listIDs)); err != nil {
		c.log.Printf("error checking campaign lists: %v", err)
		return false, echo.NewHTTPError(http.StatusInternalServerError,
			c.i18n.Ts("globals.messages.errorFetching", "name", "{globals.terms.campaign}", "error", pqErrMsg(err)))
	}
	return ok, nil
}

// teamNameMax keeps the tag ("팀:" + name) within the 100 characters of roles.axm_team_tag.
const teamNameMax = 90

func (c *Core) teamBad(key string) error {
	return echo.NewHTTPError(http.StatusBadRequest, c.i18n.T(key))
}

func (c *Core) teamFail(err error) error {
	c.log.Printf("error in team query: %v", pqErrMsg(err))
	return echo.NewHTTPError(http.StatusInternalServerError, c.i18n.T("globals.messages.internalError"))
}

// HasTeamRoles tells whether any team role exists.
func (c *Core) HasTeamRoles() bool {
	var n int
	if err := c.q.AxmCountTeamRoles.Get(&n); err != nil {
		return false
	}
	return n > 0
}

// teamRoleRow is one list role with its team tag (empty for an ordinary list role).
type teamRoleRow struct {
	ID         int     `db:"id"`
	Name       string  `db:"name"`
	Tag        *string `db:"tag"`
	UserRoleID *int    `db:"user_role_id"`
}

func (c *Core) getTeamRole(stmt *sqlx.Stmt, id int) (teamRoleRow, bool, error) {
	var r teamRoleRow
	if err := stmt.Get(&r, id); err != nil {
		if err == sql.ErrNoRows {
			return r, false, nil
		}
		return r, false, c.teamFail(err)
	}
	return r, true, nil
}

// TeamTagOfRole returns the team tag of a list role, or "" when it is not a team role.
func (c *Core) TeamTagOfRole(id int) string {
	r, ok, err := c.getTeamRole(c.q.AxmGetTeamRole, id)
	if err != nil || !ok || r.Tag == nil {
		return ""
	}
	return *r.Tag
}

// GetTeams returns the teams with their members.
func (c *Core) GetTeams() ([]models.Team, error) {
	teams := []models.Team{}
	if err := c.q.AxmGetTeams.Select(&teams, pq.Array(TeamWidePerms)); err != nil {
		return nil, c.teamFail(err)
	}
	members := []models.TeamMember{}
	if err := c.q.AxmGetTeamMembers.Select(&members); err != nil {
		return nil, c.teamFail(err)
	}
	byTeam := map[int][]models.TeamMember{}
	for _, m := range members {
		byTeam[m.TeamID] = append(byTeam[m.TeamID], m)
	}
	for i := range teams {
		teams[i].Members = byTeam[teams[i].ID]
		if teams[i].Members == nil {
			teams[i].Members = []models.TeamMember{}
		}
	}
	return teams, nil
}

// GetTeam returns one team.
func (c *Core) GetTeam(id int) (models.Team, error) {
	teams, err := c.GetTeams()
	if err != nil {
		return models.Team{}, err
	}
	for _, t := range teams {
		if t.ID == id {
			return t, nil
		}
	}
	return models.Team{}, echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "team"))
}

// validTeamUserRole allows no default or an existing role without team-wide write permissions.
func (c *Core) validTeamUserRole(tx *sqlx.Tx, id int) error {
	if id == 0 {
		return nil
	}
	facts, err := c.teamRoleFacts(tx, []int{id})
	if err != nil {
		return err
	}
	if len(facts) != 1 || facts[0].SuperAdmin || facts[0].Wide {
		return c.teamBad("teams.invalidUserRole")
	}
	return nil
}

func (c *Core) teamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > teamNameMax || strings.ContainsAny(name, ",\n\r") {
		return "", c.teamBad("teams.invalidName")
	}
	return name, nil
}

func isUnique(err error) bool {
	pqErr, ok := err.(*pq.Error)
	return ok && pqErr.Code == "23505"
}

// CreateTeam makes a team: a list role with the team tag. Lists that already carry the tag
// join it at once (the migration's trigger).
func (c *Core) CreateTeam(name string, userRoleID int) (models.Team, error) {
	name, err := c.teamName(name)
	if err != nil {
		return models.Team{}, err
	}
	tx, err := c.beginTeamRoleTx()
	if err != nil {
		return models.Team{}, err
	}
	defer tx.Rollback()
	if err := c.validTeamUserRole(tx, userRoleID); err != nil {
		return models.Team{}, err
	}
	var role any
	if userRoleID > 0 {
		role = userRoleID
	}
	var id int
	if err := tx.Stmtx(c.q.AxmCreateTeamRole).Get(&id, name, models.TeamTagFromName(name), role); err != nil {
		if isUnique(err) {
			return models.Team{}, c.teamBad("teams.exists")
		}
		return models.Team{}, c.teamFail(err)
	}
	if err := tx.Commit(); err != nil {
		return models.Team{}, c.teamFail(err)
	}
	return c.GetTeam(id)
}

// UpdateTeam renames a team (its tag changes with the name, on the lists too) and sets the
// user role members get by default. applyToMembers gives that role to the current members.
func (c *Core) UpdateTeam(id int, name string, userRoleID int, applyToMembers bool) (models.Team, error) {
	name, err := c.teamName(name)
	if err != nil {
		return models.Team{}, err
	}
	tx, err := c.beginTeamRoleTx()
	if err != nil {
		return models.Team{}, err
	}
	defer tx.Rollback()
	if err := c.validTeamUserRole(tx, userRoleID); err != nil {
		return models.Team{}, err
	}
	old, ok, err := c.getTeamRole(tx.Stmtx(c.q.AxmGetTeamRole), id)
	if err != nil {
		return models.Team{}, err
	}
	if !ok || old.Tag == nil {
		return models.Team{}, echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "team"))
	}

	tag := models.TeamTagFromName(name)
	if tag != *old.Tag {
		// A tag that lists already carry belongs to nobody yet; taking it over would pull
		// those lists into this team without anyone asking.
		var n int
		if err := tx.Stmtx(c.q.AxmCountTeamLists).Get(&n, tag); err != nil {
			return models.Team{}, c.teamFail(err)
		}
		if n > 0 {
			return models.Team{}, c.teamBad("teams.exists")
		}
	}
	var role any
	if userRoleID > 0 {
		role = userRoleID
	}
	if _, err := tx.Stmtx(c.q.AxmUpdateTeamRole).Exec(id, name, tag, role); err != nil {
		if isUnique(err) {
			return models.Team{}, c.teamBad("teams.exists")
		}
		return models.Team{}, c.teamFail(err)
	}
	if tag != *old.Tag {
		if _, err := tx.Stmtx(c.q.AxmRetagLists).Exec(*old.Tag, tag); err != nil {
			return models.Team{}, c.teamFail(err)
		}
	}
	if applyToMembers && userRoleID > 0 {
		if _, err := tx.Stmtx(c.q.AxmApplyTeamUserRole).Exec(id, userRoleID); err != nil {
			return models.Team{}, c.teamFail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Team{}, c.teamFail(err)
	}
	return c.GetTeam(id)
}

// DeleteTeam deletes a team that has no members and no lists. Deleting a list role deletes
// the accounts that use it, so people are taken out first.
func (c *Core) DeleteTeam(id int) error {
	t, err := c.GetTeam(id)
	if err != nil {
		return err
	}
	if t.NumMembers > 0 || t.Lists > 0 {
		return c.teamBad("teams.notEmpty")
	}
	var n int
	if err := c.q.AxmCountTeamLists.Get(&n, t.Tag); err != nil {
		return c.teamFail(err)
	}
	if n > 0 {
		return c.teamBad("teams.notEmpty")
	}
	if _, err := c.q.AxmDeleteTeamRole.Exec(id); err != nil {
		return c.teamFail(err)
	}
	return nil
}

// SetTeamMembers makes the users the members of the team: everybody else leaves it, and a
// user who was in another team (or had another list role) moves. A new member gets the
// team's default user role. Super admins cannot join, and nobody who has a team-wide
// permission (TeamWidePerms) can join a team that gives no default user role. A team whose own
// default user role has such a permission takes nobody new (LST-68); saving the members it
// already has, or fewer, still works.
func (c *Core) SetTeamMembers(id int, userIDs []int) (models.Team, error) {
	tx, err := c.beginTeamRoleTx()
	if err != nil {
		return models.Team{}, err
	}
	defer tx.Rollback()
	team, found, err := c.getTeamRole(tx.Stmtx(c.q.AxmGetTeamRole), id)
	if err != nil {
		return models.Team{}, err
	}
	if !found || team.Tag == nil {
		return models.Team{}, echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "team"))
	}
	seen := map[int]bool{}
	ids := []int{}
	for _, u := range userIDs {
		if u > 0 && !seen[u] {
			seen[u] = true
			ids = append(ids, u)
		}
	}
	var cands []struct {
		ID         int  `db:"id"`
		SuperAdmin bool `db:"super_admin"`
		ListRoleID *int `db:"list_role_id"`
		Wide       bool `db:"wide"`
	}
	if err := tx.Stmtx(c.q.AxmTeamCandidateUsers).Select(&cands, pq.Array(ids), pq.Array(TeamWidePerms)); err != nil {
		return models.Team{}, c.teamFail(err)
	}
	if len(cands) != len(ids) {
		return models.Team{}, c.teamBad("teams.invalidUsers")
	}
	role := 0
	if team.UserRoleID != nil {
		role = *team.UserRoleID
	}
	var joining []int
	for _, u := range cands {
		if u.SuperAdmin {
			return models.Team{}, c.teamBad("teams.superAdmin")
		}
		if u.ListRoleID != nil && *u.ListRoleID == id {
			// A member who stays is not checked again: a team that is wide already is mended
			// by the roles, not by taking its members out.
			continue
		}
		if u.Wide && role == 0 {
			return models.Team{}, c.teamBad("teams.needsUserRole")
		}
		joining = append(joining, u.ID)
	}

	if len(joining) > 0 {
		if err := c.validTeamUserRole(tx, role); err != nil {
			return models.Team{}, err
		}
	}
	if _, err := tx.Stmtx(c.q.AxmReleaseTeamMembers).Exec(id, pq.Array(ids)); err != nil {
		return models.Team{}, c.teamFail(err)
	}
	// Current members keep their roles. Only the new members get the default.
	if len(joining) > 0 {
		if _, err := tx.Stmtx(c.q.AxmAssignTeamMembers).Exec(id, pq.Array(joining), role); err != nil {
			return models.Team{}, c.teamFail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Team{}, c.teamFail(err)
	}
	return c.GetTeam(id)
}

// CountListRoleUsers returns how many users have the list role.
func (c *Core) CountListRoleUsers(id int) (int, error) {
	var n int
	if err := c.q.AxmCountListRoleUsers.Get(&n, id); err != nil {
		return 0, c.teamFail(err)
	}
	return n, nil
}

// VisibleTeams lists the teams seen on the lists a user may read (all lists when all is
// true). Teams with a role but no list show only to someone who may read all lists.
func (c *Core) VisibleTeams(all bool, listIDs []int) ([]models.TeamCount, error) {
	out := []models.TeamCount{}
	if err := c.q.AxmVisibleTeams.Select(&out, all, pq.Array(listIDs), models.TeamTagPrefix); err != nil {
		return nil, c.teamFail(err)
	}
	for i := range out {
		out[i].Name = models.TeamName(out[i].Tag)
		out[i].HasRole = out[i].RoleID != nil
	}
	return out, nil
}

// TeamlessListIDs returns the IDs of the lists without a team tag.
func (c *Core) TeamlessListIDs() ([]int, error) {
	ids := []int{}
	if err := c.q.AxmTeamlessListIDs.Select(&ids, models.TeamTagPrefix); err != nil {
		return nil, c.teamFail(err)
	}
	return ids, nil
}

// OtherTeams returns, per subscriber, the teams of the lists they are active on that the
// viewer cannot read ("" = lists without a team) and how many lists that is for each.
func (c *Core) OtherTeams(subIDs, readableListIDs []int) (map[int][]models.OtherTeam, error) {
	out := map[int][]models.OtherTeam{}
	if len(subIDs) == 0 {
		return out, nil
	}
	rows := []models.OtherTeam{}
	if err := c.q.AxmSubscriberOtherTeams.Select(&rows, pq.Array(subIDs), pq.Array(readableListIDs), models.TeamTagPrefix); err != nil {
		return nil, c.teamFail(err)
	}
	// The viewer's own teams are not "other": a list the viewer cannot read but that carries
	// the viewer's team tag cannot exist (the team role holds every list with the tag).
	for _, r := range rows {
		if r.Team != "" {
			r.Team = models.TeamName(r.Team)
		}
		out[r.SubscriberID] = append(out[r.SubscriberID], r)
	}
	return out, nil
}

// LockedSubscribers tells, per subscriber, whether they have a subscription row (any status)
// on a list outside manageListIDs (LST-42). A subscriber that does not exist is left out.
func (c *Core) LockedSubscribers(subIDs, manageListIDs []int) (map[int]bool, error) {
	out := map[int]bool{}
	if len(subIDs) == 0 {
		return out, nil
	}
	if manageListIDs == nil {
		manageListIDs = []int{}
	}
	rows := []struct {
		SubID  int  `db:"subscriber_id"`
		Locked bool `db:"locked"`
	}{}
	if err := c.q.AxmSubscriberLocked.Select(&rows, pq.Array(subIDs), pq.Array(manageListIDs)); err != nil {
		return nil, c.teamFail(err)
	}
	for _, r := range rows {
		out[r.SubID] = r.Locked
	}
	return out, nil
}

// SharedSubscriberExclusion is the SQL condition that leaves out the locked subscribers of
// LockedSubscribers in a bulk action by query: the subscribers with a subscription row on a
// list outside manageListIDs. Only integer IDs enter the text. With no list at all, everybody
// who has a subscription row is left out.
func SharedSubscriberExclusion(manageListIDs []int) string {
	ids := make([]string, len(manageListIDs))
	for i, id := range manageListIDs {
		ids[i] = strconv.Itoa(id)
	}
	return "NOT EXISTS (SELECT 1 FROM subscriber_lists axm_lk WHERE axm_lk.subscriber_id = subscribers.id" +
		" AND NOT (axm_lk.list_id = ANY('{" + strings.Join(ids, ",") + "}'::INT[])))"
}
