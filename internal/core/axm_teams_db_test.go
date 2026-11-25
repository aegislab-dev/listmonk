package core

import (
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
)

type teamEnv struct {
	*drawEnv
}

func newTeamEnv(t *testing.T) *teamEnv {
	e := &teamEnv{newDrawEnv(t)}
	// Role 1 is the Super Admin user role, as after an install.
	e.db.MustExec(`INSERT INTO roles(id,type,name,permissions) VALUES(1,'user','Super Admin','{}')`)
	e.db.MustExec(`SELECT setval(pg_get_serial_sequence('roles','id'), 1)`)
	return e
}

// userRole makes a user role; perms are its permissions.
func (e *teamEnv) userRole(name string, perms ...string) int {
	var id int
	if err := e.db.Get(&id, `INSERT INTO roles(type,name,permissions) VALUES('user',$1,$2) RETURNING id`, name, pq.StringArray(perms)); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *teamEnv) user(name string, userRole int) int {
	var id int
	if err := e.db.Get(&id, `INSERT INTO users(username,email,name,user_role_id,status,type) VALUES($1,$1||'@example.invalid',$1,$2,'enabled','user') RETURNING id`, name, userRole); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// roleLists returns the names of the lists a list role holds.
func (e *teamEnv) roleLists(role int) []string {
	var out []string
	if err := e.db.Select(&out, `SELECT l.name FROM roles c JOIN lists l ON l.id = c.list_id WHERE c.parent_id = $1 ORDER BY l.name`, role); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *teamEnv) setTags(list int, tags ...string) {
	e.db.MustExec(`UPDATE lists SET tags = $2 WHERE id = $1`, list, pq.StringArray(tags))
}

func wantStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if got == nil {
		got = []string{}
	}
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v want %v", what, got, want)
	}
}

func statusOf(err error) int {
	if he, ok := err.(*echo.HTTPError); ok {
		return he.Code
	}
	return 0
}

// A team role holds exactly the lists that carry its tag, whichever way the tag arrives.
func TestTeamRoleFollowsTags(t *testing.T) {
	e := newTeamEnv(t)
	a := e.list("A", "single", "팀:가", "x")
	e.list("B", "single", "팀:나")
	e.list("C", "single")

	team, err := e.c.CreateTeam("가", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "lists already tagged join at once", e.roleLists(team.ID), []string{"A"})

	e.list("D", "single", "팀:가") // a new list
	wantStrings(t, "new list", e.roleLists(team.ID), []string{"A", "D"})

	e.setTags(a, "팀:나") // retagged away
	wantStrings(t, "retag away", e.roleLists(team.ID), []string{"D"})
	e.setTags(a, "x", "팀:가", "팀:나") // two teams: both roles
	wantStrings(t, "two team tags", e.roleLists(team.ID), []string{"A", "D"})

	other, err := e.c.CreateTeam("나", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "second team", e.roleLists(other.ID), []string{"A", "B"})

	e.setTags(a)
	wantStrings(t, "tag removed", e.roleLists(team.ID), []string{"D"})
	wantStrings(t, "tag removed (other)", e.roleLists(other.ID), []string{"B"})

	// Both child rows give list:get and list:manage.
	var perms []string
	e.db.Select(&perms, `SELECT array_to_string(permissions, ',') FROM roles WHERE parent_id = $1`, team.ID)
	if len(perms) != 1 || perms[0] != "list:get,list:manage" {
		t.Fatalf("permissions %v", perms)
	}

	// Deleting a list removes it from the role.
	e.db.MustExec(`DELETE FROM lists WHERE name = 'D'`)
	wantStrings(t, "list deleted", e.roleLists(team.ID), nil)

	// Same tag twice is refused.
	if _, err := e.c.CreateTeam("가", 0); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("duplicate team: %v", err)
	}
}

// An ordinary list role is not touched by any of this.
func TestOrdinaryListRoleUntouched(t *testing.T) {
	e := newTeamEnv(t)
	l := e.list("Plain", "single", "팀:가")
	var role int
	e.db.Get(&role, `INSERT INTO roles(type,name) VALUES('list','Manual') RETURNING id`)
	e.db.MustExec(`INSERT INTO roles(parent_id,list_id,type,permissions) VALUES($1,$2,'list','{list:get}')`, role, l)
	if _, err := e.c.CreateTeam("가", 0); err != nil {
		t.Fatal(err)
	}
	e.list("More", "single", "팀:가")
	e.setTags(l, "팀:나")
	wantStrings(t, "manual role", e.roleLists(role), []string{"Plain"})
	if e.c.TeamTagOfRole(role) != "" {
		t.Fatal("an ordinary role has no team tag")
	}
}

// Renaming a team renames its tag on the lists too; a tag that lists already carry is refused.
func TestTeamRename(t *testing.T) {
	e := newTeamEnv(t)
	e.list("A", "single", "팀:가", "keep")
	e.list("B", "single", "팀:가", "팀:가-2")
	e.list("Taken", "single", "팀:다")
	team, err := e.c.CreateTeam("가", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.UpdateTeam(team.ID, "다", 0, false); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("a tag that lists carry: %v", err)
	}
	got, err := e.c.UpdateTeam(team.ID, "새 이름", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "팀:새-이름" || got.Name != "새 이름" || got.Lists != 2 {
		t.Fatalf("%+v", got)
	}
	var tags []string
	e.db.Select(&tags, `SELECT array_to_string(tags, ',') FROM lists WHERE name IN ('A','B') ORDER BY name`)
	wantStrings(t, "tags", tags, []string{"팀:새-이름,keep", "팀:새-이름,팀:가-2"})
	wantStrings(t, "role lists", e.roleLists(team.ID), []string{"A", "B"})
}

// Members: one team per person, the default user role is given on joining, super admins and
// people who read every list are kept out.
func TestTeamMembers(t *testing.T) {
	e := newTeamEnv(t)
	member := e.userRole("member", "subscribers:get", "campaigns:get")
	admin := e.userRole("operator", "lists:get_all", "lists:manage_all", "subscribers:get_all")
	u1, u2, op, sa := e.user("u1", member), e.user("u2", member), e.user("op", admin), e.user("sa", 1)

	t1, err := e.c.CreateTeam("하나", member)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := e.c.CreateTeam("둘", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.SetTeamMembers(t1.ID, []int{sa}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("super admin: %v", err)
	}
	if _, err := e.c.SetTeamMembers(t2.ID, []int{op}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("admin role without a default user role: %v", err)
	}
	if _, err := e.c.SetTeamMembers(t1.ID, []int{u1, 99999}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown user: %v", err)
	}
	got, err := e.c.SetTeamMembers(t1.ID, []int{u1, u2, op})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 3 {
		t.Fatalf("%+v", got.Members)
	}
	var roleOfOp int
	e.db.Get(&roleOfOp, `SELECT user_role_id FROM users WHERE id=$1`, op)
	if roleOfOp != member {
		t.Fatalf("the operator should now have the team's user role, has %d", roleOfOp)
	}

	// A person is in one team: adding u1 to the other team moves them, and the first team
	// drops everyone not listed.
	if _, err := e.c.SetTeamMembers(t2.ID, []int{u1}); err != nil {
		t.Fatal(err)
	}
	var in1, in2 []int
	e.db.Select(&in1, `SELECT id FROM users WHERE list_role_id=$1 ORDER BY id`, t1.ID)
	e.db.Select(&in2, `SELECT id FROM users WHERE list_role_id=$1 ORDER BY id`, t2.ID)
	sort.Ints(in1)
	if !reflect.DeepEqual(in1, []int{u2, op}) || !reflect.DeepEqual(in2, []int{u1}) {
		t.Fatalf("members %v %v", in1, in2)
	}
	if _, err := e.c.SetTeamMembers(t1.ID, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM users`)
	if n != 4 {
		t.Fatalf("users must stay: %d", n)
	}
}

// A team with people or lists is not deleted; an empty one is. The default user role must not
// read all lists.
func TestTeamDeleteAndUserRole(t *testing.T) {
	e := newTeamEnv(t)
	admin := e.userRole("operator", "lists:manage_all")
	member := e.userRole("member", "subscribers:get")
	if _, err := e.c.CreateTeam("가", admin); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("default role that reads all lists: %v", err)
	}
	if _, err := e.c.CreateTeam("가", 424242); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown default role: %v", err)
	}
	for _, bad := range []string{"", "  ", "a,b"} {
		if _, err := e.c.CreateTeam(bad, 0); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("name %q: %v", bad, err)
		}
	}
	team, err := e.c.CreateTeam("가", member)
	if err != nil {
		t.Fatal(err)
	}
	u := e.user("u", member)
	e.c.SetTeamMembers(team.ID, []int{u})
	if err := e.c.DeleteTeam(team.ID); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("a team with a member: %v", err)
	}
	e.c.SetTeamMembers(team.ID, nil)
	l := e.list("L", "single", "팀:가")
	if err := e.c.DeleteTeam(team.ID); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("a team with a list: %v", err)
	}
	e.db.MustExec(`DELETE FROM lists WHERE id=$1`, l)
	if err := e.c.DeleteTeam(team.ID); err != nil {
		t.Fatal(err)
	}
	if teams, _ := e.c.GetTeams(); len(teams) != 0 {
		t.Fatalf("%+v", teams)
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM users WHERE id=$1`, u)
	if n != 1 {
		t.Fatal("the member's account was deleted with the team")
	}
}

// Apply to members gives the new default role to the people already in the team.
func TestTeamApplyUserRole(t *testing.T) {
	e := newTeamEnv(t)
	r1, r2 := e.userRole("r1", "subscribers:get"), e.userRole("r2", "campaigns:get")
	team, _ := e.c.CreateTeam("가", r1)
	u := e.user("u", r1)
	e.c.SetTeamMembers(team.ID, []int{u})
	if _, err := e.c.UpdateTeam(team.ID, "가", r2, false); err != nil {
		t.Fatal(err)
	}
	var role int
	e.db.Get(&role, `SELECT user_role_id FROM users WHERE id=$1`, u)
	if role != r1 {
		t.Fatal("not applied unless asked")
	}
	e.c.UpdateTeam(team.ID, "가", r2, true)
	e.db.Get(&role, `SELECT user_role_id FROM users WHERE id=$1`, u)
	if role != r2 {
		t.Fatal("applied")
	}
}

// What a viewer learns about a subscriber on lists of other teams.
func TestOtherTeams(t *testing.T) {
	e := newTeamEnv(t)
	mine := e.list("Mine", "single", "팀:가")
	b1 := e.list("B1", "single", "팀:나")
	b2 := e.list("B2", "single", "팀:나", "팀:다")
	none := e.list("None", "single")
	gone := e.list("Gone", "single", "팀:라")
	dyn := e.list("Dyn", "single", "팀:마")
	e.db.MustExec(`UPDATE lists SET axm_filter='{"version":1,"base":{"type":"all"},"match":"all","conditions":[]}' WHERE id=$1`, dyn)

	s := testSub(e.db, `{}`, "enabled")
	alone := testSub(e.db, `{}`, "enabled")
	for _, l := range []int{mine, b1, b2, none} {
		e.join(l, s, "confirmed")
	}
	e.join(gone, s, "unsubscribed") // not active: not counted
	e.join(dyn, s, "unsubscribed")
	e.join(mine, alone, "confirmed")

	got, err := e.c.OtherTeams([]int{s, alone}, []int{mine})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, o := range got[s] {
		seen = append(seen, o.Team+":"+string(rune('0'+o.Lists)))
	}
	wantStrings(t, "other teams of the shared subscriber", seen, []string{":1", "나:2", "다:1"})
	if len(got[alone]) != 0 {
		t.Fatalf("a subscriber of the viewer's team only has no other team: %+v", got[alone])
	}
}

// The teams a user sees, and the lists without a team.
func TestVisibleTeams(t *testing.T) {
	e := newTeamEnv(t)
	a := e.list("A", "single", "팀:가")
	e.list("B", "single", "팀:나", "x")
	e.list("C", "single")
	e.c.CreateTeam("가", 0)
	e.c.CreateTeam("라", 0) // a team without lists
	all, err := e.c.VisibleTeams(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	unowned := 0
	for _, x := range all {
		tags = append(tags, x.Tag)
		if x.RoleID == nil {
			unowned++
		}
	}
	wantStrings(t, "all", tags, []string{"팀:가", "팀:나", "팀:라"})
	if unowned != 1 {
		t.Fatalf("a tag without a team role: %d", unowned)
	}
	mine, _ := e.c.VisibleTeams(false, []int{a})
	if len(mine) != 1 || mine[0].Tag != "팀:가" || mine[0].Lists != 1 {
		t.Fatalf("%+v", mine)
	}
	ids, _ := e.c.TeamlessListIDs()
	if len(ids) != 1 {
		t.Fatalf("%v", ids)
	}
}

// Migration level: the trigger does nothing without team roles, and clearing a role's tag
// keeps the lists it has.
func TestTeamRoleTagCleared(t *testing.T) {
	e := newTeamEnv(t)
	l := e.list("A", "single", "팀:가")
	team, _ := e.c.CreateTeam("가", 0)
	e.db.MustExec(`UPDATE roles SET axm_team_tag = NULL WHERE id = $1`, team.ID)
	wantStrings(t, "kept", e.roleLists(team.ID), []string{"A"})
	e.setTags(l)
	wantStrings(t, "no longer follows the tag", e.roleLists(team.ID), []string{"A"})
}

// A team save waiting on a role edit must validate against the committed role.
func TestTeamRoleTransactionSerializes(t *testing.T) {
	e := newTeamEnv(t)
	role := e.userRole("member", "campaigns:get")
	e.db.SetMaxOpenConns(2)

	tx, err := e.c.beginTeamRoleTx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE roles SET permissions = '{campaigns:manage_all}' WHERE id = $1`, role); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := e.c.CreateTeam("가", role)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("team save completed before the role change committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if statusOf(err) != http.StatusBadRequest {
			t.Fatalf("team save after role change: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("team save still waiting after commit")
	}

	if _, err := e.c.CreateTeam("가", 0); err != nil {
		t.Fatalf("rejected save must roll back and release the lock: %v", err)
	}
}
