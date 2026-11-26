package main

import (
	"net/http"
	"testing"

	"github.com/lib/pq"
)

// LST-73: the dashboard numbers are company-wide, so only roles that read across lists,
// subscribers or campaigns (or see the analytics) get them.
func TestDashboardNeedsACompanyWidePermission(t *testing.T) {
	e := newAuditEnv(t)
	role := func(name string, perms ...string) int {
		var id int
		e.get(&id, `INSERT INTO roles(type,name,permissions) VALUES('user',$1,$2) RETURNING id`, name, pq.StringArray(append([]string{}, perms...)))
		return id
	}
	user := func(name string, roleID int) int {
		var id int
		e.get(&id, `INSERT INTO users(username,email,name,user_role_id,list_role_id,status,type) VALUES($1,$1||'@example.invalid',$1,$2,NULL,'enabled','user') RETURNING id`, name, roleID)
		return id
	}
	// Own-list roles read and write only what their lists hold.
	denied := map[string]int{
		"campaigns:get, subscribers:get": user("own", role("own", "campaigns:get", "subscribers:get", "campaigns:manage", "subscribers:manage")),
		"no permission at all":           user("nobody", role("nobody")),
		"bounces and settings":           user("other", role("other", "bounces:get", "bounces:manage", "settings:get", "lists:get")),
	}
	allowed := map[string]int{
		"lists:get_all":           user("a1", role("a1", "lists:get_all")),
		"lists:manage_all":        user("a2", role("a2", "lists:manage_all")),
		"subscribers:get_all":     user("a3", role("a3", "subscribers:get_all")),
		"campaigns:get_all":       user("a4", role("a4", "campaigns:get_all")),
		"campaigns:manage_all":    user("a5", role("a5", "campaigns:manage_all")),
		"campaigns:get_analytics": user("a6", role("a6", "campaigns:get_analytics")),
		"super admin":             e.superAdmin,
		"operator":                e.admin,
	}
	for name, id := range denied {
		u := e.as(id)
		if code, _ := e.call(e.a.auth.Perm(e.a.GetDashboardCounts, dashboardPerms...), u, "GET", "/api/dashboard/counts", "", ""); code != http.StatusForbidden {
			t.Errorf("counts for %s: %d, want 403", name, code)
		}
		if code, _ := e.call(e.a.auth.Perm(e.a.GetDashboardCharts, dashboardPerms...), u, "GET", "/api/dashboard/charts", "", ""); code != http.StatusForbidden {
			t.Errorf("charts for %s: %d, want 403", name, code)
		}
	}
	for name, id := range allowed {
		u := e.as(id)
		if code, _ := e.call(e.a.auth.Perm(e.a.GetDashboardCounts, dashboardPerms...), u, "GET", "/api/dashboard/counts", "", ""); code != http.StatusOK {
			t.Errorf("counts for %s: %d, want 200", name, code)
		}
		if code, _ := e.call(e.a.auth.Perm(e.a.GetDashboardCharts, dashboardPerms...), u, "GET", "/api/dashboard/charts", "", ""); code != http.StatusOK {
			t.Errorf("charts for %s: %d, want 200", name, code)
		}
	}
}
