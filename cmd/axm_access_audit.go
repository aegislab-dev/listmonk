package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// Audit rows for roles, teams, users and settings. They name what changed; a changed
// setting is a key name only (never the value: it may hold a password), and a user's e-mail
// and password are never written.

// roleInfo is a role as the audit log needs it.
type roleInfo struct {
	found bool
	name  string
	kind  string
	perms []string
	lists map[int]string // list role: list ID -> permissions joined
	names map[int]string // list role: list ID -> list name
}

// roleInfoOf reads a role (a user role or a list role) before it changes.
func (a *App) roleInfoOf(id int) roleInfo {
	roles, err := a.core.GetRoles()
	if err == nil {
		for _, r := range roles {
			if r.ID == id {
				return roleInfo{found: true, name: r.Name.String, kind: auth.RoleTypeUser, perms: r.Permissions}
			}
		}
	}
	lroles, err := a.core.GetListRoles()
	if err == nil {
		for _, r := range lroles {
			if r.ID == id {
				return listRoleInfo(r)
			}
		}
	}
	return roleInfo{}
}

func listRoleInfo(r auth.ListRole) roleInfo {
	ri := roleInfo{found: true, name: r.Name.String, kind: auth.RoleTypeList,
		lists: map[int]string{}, names: map[int]string{}}
	for _, l := range r.Lists {
		p := append([]string{}, l.Permissions...)
		sort.Strings(p)
		b, _ := json.Marshal(p)
		ri.lists[l.ID] = string(b)
		ri.names[l.ID] = l.Name
	}
	return ri
}

// roleChanges describes what differs between two states of a role.
func roleChanges(before, after roleInfo) map[string]any {
	d := map[string]any{}
	if before.name != after.name {
		d["name"] = map[string]any{"from": before.name, "to": after.name}
	}
	if after.kind == auth.RoleTypeUser {
		added, removed := diffStrings(before.perms, after.perms)
		if len(added) > 0 {
			d["permissions_added"] = added
		}
		if len(removed) > 0 {
			d["permissions_removed"] = removed
		}
		return d
	}
	var added, removed, changed []string
	for id, p := range after.lists {
		old, ok := before.lists[id]
		switch {
		case !ok:
			added = append(added, after.names[id])
		case old != p:
			changed = append(changed, after.names[id])
		}
	}
	for id := range before.lists {
		if _, ok := after.lists[id]; !ok {
			removed = append(removed, before.names[id])
		}
	}
	for k, v := range map[string][]string{"lists_added": added, "lists_removed": removed, "lists_changed": changed} {
		if len(v) > 0 {
			sort.Strings(v)
			d[k] = v
		}
	}
	return d
}

func (a *App) auditRoleCreated(c echo.Context, id int, after roleInfo) {
	d := map[string]any{"kind": after.kind}
	if after.kind == auth.RoleTypeUser {
		d["permissions"] = after.perms
	} else {
		d["lists"] = len(after.lists)
	}
	a.audit(c, auditRoleCreate, auditTarget{auditTargetRole, id, after.name}, auditNoCount, d)
}

func (a *App) auditRoleUpdated(c echo.Context, id int, before, after roleInfo) {
	d := roleChanges(before, after)
	d["kind"] = after.kind
	a.audit(c, auditRoleUpdate, auditTarget{auditTargetRole, id, after.name}, auditNoCount, d)
}

// userInfo is the part of a user the audit log compares.
type userInfo struct {
	found         bool
	username      string
	name          string
	typ           string
	status        string
	email         string
	userRoleID    int
	listRoleID    int
	userRole      string // role names, for the detail (the ID when the name is not known)
	listRole      string
	passwordLogin bool
}

func (a *App) userInfoOf(id int) userInfo {
	u, err := a.core.GetUser(id, "", "")
	if err != nil {
		return userInfo{}
	}
	return userFrom(u)
}

func userFrom(u auth.User) userInfo {
	// core.setupUserFields moves the user role into u.UserRole and zeroes u.UserRoleID.
	ui := userInfo{found: true, username: u.Username, name: u.Name, typ: u.Type, status: u.Status,
		email: u.Email.String, userRoleID: u.UserRole.ID, passwordLogin: u.PasswordLogin}
	ui.userRole = roleLabel(u.UserRole.ID, u.UserRole.Name)
	if u.ListRoleID != nil {
		ui.listRoleID = *u.ListRoleID
		ui.listRole = roleLabel(*u.ListRoleID, u.ListRoleName.String)
	}
	return ui
}

// roleLabel is a role's name, or its ID when the name is not known.
func roleLabel(id int, name string) string {
	if name != "" {
		return name
	}
	if id == 0 {
		return ""
	}
	return "#" + strconv.Itoa(id)
}

func (a *App) auditUserCreated(c echo.Context, u auth.User) {
	now := userFrom(u)
	d := map[string]any{"type": u.Type, "status": u.Status, "user_role": now.userRole, "password_login": u.PasswordLogin}
	if now.listRole != "" {
		d["list_role"] = now.listRole
	}
	a.audit(c, auditUserCreate, auditTarget{auditTargetUser, u.ID, u.Username}, auditNoCount, d)
}

// auditUserUpdated writes what a user save changed. The e-mail and the password are only
// said to have changed.
func (a *App) auditUserUpdated(c echo.Context, id int, before userInfo, after auth.User, passwordSent bool) {
	now := userFrom(after)
	d := map[string]any{}
	set := func(k string, from, to any) {
		if !reflect.DeepEqual(from, to) {
			d[k] = map[string]any{"from": from, "to": to}
		}
	}
	if before.found {
		set("username", before.username, now.username)
		set("name", before.name, now.name)
		set("status", before.status, now.status)
		if before.userRoleID != now.userRoleID {
			d["user_role"] = map[string]any{"from": before.userRole, "to": now.userRole}
		}
		if before.listRoleID != now.listRoleID {
			d["list_role"] = map[string]any{"from": before.listRole, "to": now.listRole}
		}
		set("password_login", before.passwordLogin, now.passwordLogin)
		if before.email != now.email {
			d["email_changed"] = true
		}
	}
	if passwordSent {
		d["password_changed"] = true
	}
	a.audit(c, auditUserUpdate, auditTarget{auditTargetUser, id, after.Username}, auditNoCount, d)
}

func (a *App) auditUsersDeleted(c echo.Context, ids []int, before []userInfo) {
	for i, id := range ids {
		name := ""
		if i < len(before) && before[i].found {
			name = before[i].username
		}
		a.audit(c, auditUserDelete, auditTarget{auditTargetUser, id, name}, auditNoCount, nil)
	}
}

func (a *App) userInfos(ids []int) []userInfo {
	out := make([]userInfo, len(ids))
	for i, id := range ids {
		out[i] = a.userInfoOf(id)
	}
	return out
}

// teamMemberIDs returns the IDs of the people in a team.
func teamMemberIDs(t models.Team) []int {
	out := make([]int, 0, len(t.Members))
	for _, m := range t.Members {
		out = append(out, m.ID)
	}
	return out
}

func (a *App) auditTeamUpdated(c echo.Context, before, after models.Team) {
	d := map[string]any{}
	if before.Name != after.Name {
		d["name"] = map[string]any{"from": before.Name, "to": after.Name}
	}
	bu, au := 0, 0
	if before.UserRoleID != nil {
		bu = *before.UserRoleID
	}
	if after.UserRoleID != nil {
		au = *after.UserRoleID
	}
	if bu != au {
		d["user_role_id"] = map[string]any{"from": bu, "to": au}
	}
	a.audit(c, auditTeamUpdate, auditTarget{auditTargetTeam, after.ID, after.Name}, auditNoCount, d)
}

func (a *App) auditTeamMembers(c echo.Context, before, after models.Team) {
	idStr := func(ids []int) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = strconv.Itoa(id)
		}
		return out
	}
	added, removed := diffStrings(idStr(teamMemberIDs(before)), idStr(teamMemberIDs(after)))
	d := map[string]any{}
	if len(added) > 0 {
		d["added_user_ids"] = added
	}
	if len(removed) > 0 {
		d["removed_user_ids"] = removed
	}
	a.audit(c, auditTeamMembers, auditTarget{auditTargetTeam, after.ID, after.Name}, len(after.Members), d)
}

// changedSettingKeys returns the keys of the settings that differ between two states.
func changedSettingKeys(before, after models.Settings) []string {
	toMap := func(s models.Settings) map[string]json.RawMessage {
		b, _ := json.Marshal(s)
		m := map[string]json.RawMessage{}
		_ = json.Unmarshal(b, &m)
		return m
	}
	bm, am := toMap(before), toMap(after)
	var keys []string
	for k, v := range am {
		if o, ok := bm[k]; !ok || string(o) != string(v) {
			keys = append(keys, k)
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (a *App) auditSettings(c echo.Context, keys []string) {
	if keys == nil {
		keys = []string{}
	}
	a.audit(c, auditSettingsUpdate, auditTarget{Type: auditTargetSettings, Name: "settings"}, len(keys), map[string]any{"keys": keys})
}
