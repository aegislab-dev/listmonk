package core

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

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

func (c *Core) getTeamRole(id int) (teamRoleRow, bool, error) {
	var r teamRoleRow
	if err := c.q.AxmGetTeamRole.Get(&r, id); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return r, false, nil
		}
		return r, false, c.teamFail(err)
	}
	return r, true, nil
}

// TeamTagOfRole returns the team tag of a list role, or "" when it is not a team role.
func (c *Core) TeamTagOfRole(id int) string {
	r, ok, err := c.getTeamRole(id)
	if err != nil || !ok || r.Tag == nil {
		return ""
	}
	return *r.Tag
}

// GetTeams returns the teams with their members.
func (c *Core) GetTeams() ([]models.Team, error) {
	teams := []models.Team{}
	if err := c.q.AxmGetTeams.Select(&teams); err != nil {
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

// validTeamUserRole checks the default user role of a team: it must exist and must not be
// the super admin role or carry lists:get_all / lists:manage_all, which would show every list.
func (c *Core) validTeamUserRole(id int) error {
	if id == 0 {
		return nil
	}
	var facts []struct {
		ID         int  `db:"id"`
		SuperAdmin bool `db:"super_admin"`
		AllLists   bool `db:"all_lists"`
	}
	if err := c.q.AxmUserRoleFacts.Select(&facts, pq.Array([]int{id})); err != nil {
		return c.teamFail(err)
	}
	if len(facts) != 1 || facts[0].SuperAdmin || facts[0].AllLists {
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
	if err := c.validTeamUserRole(userRoleID); err != nil {
		return models.Team{}, err
	}
	var role any
	if userRoleID > 0 {
		role = userRoleID
	}
	var id int
	if err := c.q.AxmCreateTeamRole.Get(&id, name, models.TeamTagFromName(name), role); err != nil {
		if isUnique(err) {
			return models.Team{}, c.teamBad("teams.exists")
		}
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
	if err := c.validTeamUserRole(userRoleID); err != nil {
		return models.Team{}, err
	}
	old, ok, err := c.getTeamRole(id)
	if err != nil {
		return models.Team{}, err
	}
	if !ok || old.Tag == nil {
		return models.Team{}, echo.NewHTTPError(http.StatusNotFound, c.i18n.Ts("globals.messages.notFound", "name", "team"))
	}

	tag := models.TeamTagFromName(name)
	tx, err := c.db.Beginx()
	if err != nil {
		return models.Team{}, c.teamFail(err)
	}
	defer tx.Rollback()

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
// team's default user role. Super admins cannot join, and nobody who would still read every
// list (lists:*_all) can join a team that gives no default user role.
func (c *Core) SetTeamMembers(id int, userIDs []int) (models.Team, error) {
	t, err := c.GetTeam(id)
	if err != nil {
		return t, err
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
		AllLists   bool `db:"all_lists"`
	}
	if err := c.q.AxmTeamCandidateUsers.Select(&cands, pq.Array(ids)); err != nil {
		return t, c.teamFail(err)
	}
	if len(cands) != len(ids) {
		return t, c.teamBad("teams.invalidUsers")
	}
	role := 0
	if t.UserRoleID != nil {
		role = *t.UserRoleID
	}
	for _, u := range cands {
		if u.SuperAdmin {
			return t, c.teamBad("teams.superAdmin")
		}
		if u.AllLists && role == 0 {
			return t, c.teamBad("teams.needsUserRole")
		}
	}

	tx, err := c.db.Beginx()
	if err != nil {
		return t, c.teamFail(err)
	}
	defer tx.Rollback()
	if _, err := tx.Stmtx(c.q.AxmReleaseTeamMembers).Exec(id, pq.Array(ids)); err != nil {
		return t, c.teamFail(err)
	}
	// Only people who join get the default user role; members who stay keep theirs.
	var joining []int
	if err := tx.Select(&joining, `SELECT id FROM users WHERE id = ANY($1::INT[]) AND list_role_id IS DISTINCT FROM $2`, pq.Array(ids), id); err != nil {
		return t, c.teamFail(err)
	}
	if len(joining) > 0 {
		if _, err := tx.Stmtx(c.q.AxmAssignTeamMembers).Exec(id, pq.Array(joining), role); err != nil {
			return t, c.teamFail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return t, c.teamFail(err)
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
