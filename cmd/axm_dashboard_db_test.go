package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/labstack/echo/v4"
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

	// The real route table: the two routes as handlers.go registers them, a user set by a
	// middleware, requests through the router.
	srv := echo.New()
	g := srv.Group("", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			id, _ := strconv.Atoi(c.QueryParam("u"))
			c.Set(auth.UserHTTPCtxKey, e.as(id))
			return next(c)
		}
	})
	e.a.dashboardRoutes(g, e.a.auth.Perm)
	status := func(path string, id int) int {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?u="+strconv.Itoa(id), nil))
		return rec.Code
	}
	// The charts are views and clicks of every campaign: analytics only.
	charts := map[string]bool{"campaigns:get_analytics": true, "super admin": true}

	for name, id := range denied {
		if code := status("/api/dashboard/counts", id); code != http.StatusForbidden {
			t.Errorf("counts for %s: %d, want 403", name, code)
		}
		if code := status("/api/dashboard/charts", id); code != http.StatusForbidden {
			t.Errorf("charts for %s: %d, want 403", name, code)
		}
	}
	for name, id := range allowed {
		if code := status("/api/dashboard/counts", id); code != http.StatusOK {
			t.Errorf("counts for %s: %d, want 200", name, code)
		}
		want := http.StatusForbidden
		if charts[name] {
			want = http.StatusOK
		}
		if code := status("/api/dashboard/charts", id); code != want {
			t.Errorf("charts for %s: %d, want %d", name, code, want)
		}
	}
}
