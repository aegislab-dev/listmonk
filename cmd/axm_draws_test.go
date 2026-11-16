package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// Exclude lists need the same read access as base lists.
func TestDynamicExcludeListPermissions(t *testing.T) {
	a := &App{}
	user := auth.User{ListPermissionsMap: map[int]map[string]struct{}{2: {auth.PermListGet: {}}, 3: {auth.PermListGet: {}}}}
	for _, tc := range []struct {
		name    string
		user    auth.User
		filter  core.DynamicFilter
		allowed bool
	}{
		{"exclude readable", user, core.DynamicFilter{Version: 2, Base: core.DynamicBase{Type: "lists", Lists: []int{2}}, Exclude: &core.DynamicExclude{Lists: []int{3}}}, true},
		{"exclude not readable", user, core.DynamicFilter{Version: 2, Base: core.DynamicBase{Type: "lists", Lists: []int{2}}, Exclude: &core.DynamicExclude{Lists: []int{4}}}, false},
		{"global base needs read all even with readable excludes", user, core.DynamicFilter{Version: 2, Base: core.DynamicBase{Type: "all"}, Exclude: &core.DynamicExclude{Lists: []int{3}}}, false},
		{"read all covers excludes", auth.User{PermissionsMap: map[string]struct{}{auth.PermListGetAll: {}}}, core.DynamicFilter{Version: 2, Base: core.DynamicBase{Type: "all"}, Exclude: &core.DynamicExclude{Lists: []int{9}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := echo.New().NewContext(httptest.NewRequest("POST", "/api/lists/dynamic/preview", nil), httptest.NewRecorder())
			c.Set(auth.UserHTTPCtxKey, tc.user)
			if err := a.checkDynamicBasePerm(c, tc.filter); (err == nil) != tc.allowed {
				t.Fatal(err)
			}
		})
	}
}

// The three draw routes exist, and the two that write or count need the permission to make
// lists (lists:manage_all or a team member).
func TestDrawRoutesAreGuarded(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "handlers.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, route := range []string{`"/api/lists/:id/draw/preview", a.listMaker(hasID(a.PreviewListDraw))`, `"/api/lists/:id/draw", a.listMaker(hasID(a.DrawList))`, `g.POST("/api/lists", a.listMaker(a.CreateList))`, `g.GET("/api/lists/draws", a.GetListDraws)`} {
		if !strings.Contains(src, route) {
			t.Fatalf("route missing: %s", route)
		}
	}
}

func drawCall(t *testing.T, h echo.HandlerFunc, method, path, id, body string, user auth.User) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, user)
	if id != "" {
		c.Set("id", atoi(id))
	}
	if err := h(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he.Code, nil
		}
		if err == auth.ErrPermDenied {
			return http.StatusForbidden, nil
		}
		t.Fatal(err)
	}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	m := map[string]json.RawMessage{}
	json.Unmarshal(out.Data, &m)
	if len(m) == 0 && len(out.Data) > 0 {
		m["_"] = out.Data
	}
	return rec.Code, m
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func TestDrawAPI(t *testing.T) {
	e := newSendsEnv(t, 40)
	a := &App{core: e.core, i18n: &i18n.I18n{}}
	admin := auth.User{UserRoleID: auth.SuperAdminRoleID}
	src := itoa(e.listID)

	// Values out of range are refused, by the preview and by the draw.
	for _, body := range []string{
		`{"unit":"count","value":0}`, `{"unit":"count","value":-3}`, `{"unit":"count","value":10000001}`,
		`{"unit":"percent","value":0}`, `{"unit":"percent","value":101}`, `{"unit":"tenth","value":3}`, `{}`,
		`{"unit":"count","value":5,"exclude_scope":"nobody"}`, `{"unit":"count","value":5,"exclude_days":-1}`,
		`{"unit":"count","value":5,"exclude_days":99999}`, `not json`,
	} {
		if code, _ := drawCall(t, a.PreviewListDraw, "POST", "/x", src, body, admin); code != http.StatusBadRequest {
			t.Fatalf("preview %s: %d", body, code)
		}
		if code, _ := drawCall(t, a.DrawList, "POST", "/x", src, body, admin); code != http.StatusBadRequest {
			t.Fatalf("draw %s: %d", body, code)
		}
	}
	// A name that is too long is refused.
	long := `{"unit":"count","value":5,"name":"` + strings.Repeat("가", 201) + `"}`
	if code, _ := drawCall(t, a.DrawList, "POST", "/x", src, long, admin); code != http.StatusBadRequest {
		t.Fatalf("long name: %d", code)
	}
	// An unknown list.
	if code, _ := drawCall(t, a.DrawList, "POST", "/x", "99999", `{"unit":"count","value":5}`, admin); code != http.StatusBadRequest {
		t.Fatalf("unknown list: %d", code)
	}

	// The preview counts and writes nothing.
	code, out := drawCall(t, a.PreviewListDraw, "POST", "/x", src, `{"unit":"count","value":15}`, admin)
	if code != 200 {
		t.Fatal(code)
	}
	var counts models.DrawCounts
	b, _ := json.Marshal(out)
	json.Unmarshal(b, &counts)
	if counts.Target != 40 || counts.Candidates != 40 || counts.Requested != 15 || counts.WillDraw != 15 {
		t.Fatalf("%s", b)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_list_draws`)
	if n != 0 {
		t.Fatal("the preview wrote a record")
	}

	// A draw returns the new list and the record; the same name twice gets a number.
	body := `{"unit":"count","value":15,"name":"드로우 시험"}`
	code, out = drawCall(t, a.DrawList, "POST", "/x", src, body, auth.User{UserRoleID: auth.SuperAdminRoleID})
	if code != 200 {
		t.Fatal(code)
	}
	var list models.List
	var draw models.ListDraw
	json.Unmarshal(out["list"], &list)
	json.Unmarshal(out["draw"], &draw)
	if list.Name != "드로우 시험" || list.SubscriberCount != 15 || draw.SourceListID == nil || *draw.SourceListID != e.listID || draw.ListName != list.Name {
		t.Fatalf("%+v %+v", list, draw)
	}
	_, out = drawCall(t, a.DrawList, "POST", "/x", src, body, admin)
	json.Unmarshal(out["list"], &list)
	if list.Name != "드로우 시험 (2)" {
		t.Fatalf("%q", list.Name)
	}
	// A drawn list is not a source.
	if code, _ := drawCall(t, a.DrawList, "POST", "/x", itoa(list.ID), `{"unit":"count","value":1}`, admin); code != http.StatusBadRequest {
		t.Fatalf("drawn source: %d", code)
	}
	// 40 − 30 drawn: asking for more takes what is left; then nothing is left.
	code, out = drawCall(t, a.DrawList, "POST", "/x", src, `{"unit":"count","value":50}`, admin)
	json.Unmarshal(out["list"], &list)
	if code != 200 || list.SubscriberCount != 10 {
		t.Fatalf("%d %+v", code, list)
	}
	if code, _ := drawCall(t, a.DrawList, "POST", "/x", src, `{"unit":"count","value":1}`, admin); code != http.StatusBadRequest {
		t.Fatalf("nobody left: %d", code)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Records show to a user who may read the drawn list, or the source if the drawn list is gone.
func TestGetListDrawsPermissions(t *testing.T) {
	e := newSendsEnv(t, 30)
	a := &App{core: e.core, i18n: &i18n.I18n{}}
	var drawn []int
	for i := 0; i < 2; i++ {
		l, _, err := e.core.DrawList(e.listID, models.DrawSettings{Unit: "count", Value: 5}, core.DrawOpts{})
		if err != nil {
			t.Fatal(err)
		}
		drawn = append(drawn, l.ID)
	}
	list := func(user auth.User, query string) (int, []models.ListDraw) {
		req := httptest.NewRequest("GET", "/api/lists/draws"+query, nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(auth.UserHTTPCtxKey, user)
		if err := a.GetListDraws(c); err != nil {
			if err == auth.ErrPermDenied {
				return 403, nil
			}
			if he, ok := err.(*echo.HTTPError); ok {
				return he.Code, nil
			}
			t.Fatal(err)
		}
		var out struct {
			Data []models.ListDraw `json:"data"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Data
	}
	admin := auth.User{UserRoleID: auth.SuperAdminRoleID}
	if code, rows := list(admin, ""); code != 200 || len(rows) != 2 {
		t.Fatalf("%d %d", code, len(rows))
	}
	if code, rows := list(admin, "?source_id="+itoa(e.listID)); code != 200 || len(rows) != 2 {
		t.Fatalf("%d %d", code, len(rows))
	}
	if code, rows := list(admin, "?list_id="+itoa(drawn[1])); code != 200 || len(rows) != 1 || *rows[0].ListID != drawn[1] {
		t.Fatalf("%d %+v", code, rows)
	}
	for _, bad := range []string{"?source_id=-1", "?list_id=0", "?list_id=abc"} {
		if code, _ := list(admin, bad); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	// A user who may read only one drawn list sees only its record.
	one := auth.User{GetListIDs: []int{drawn[0]}, ListPermissionsMap: map[int]map[string]struct{}{drawn[0]: {auth.PermListGet: {}}}}
	if code, rows := list(one, ""); code != 200 || len(rows) != 1 || *rows[0].ListID != drawn[0] {
		t.Fatalf("%d %+v", code, rows)
	}
	// Asking for the draws of a source the user may not read is refused; no permission sees nothing.
	if code, _ := list(one, "?source_id="+itoa(e.listID)); code != 403 {
		t.Fatalf("%d", code)
	}
	if code, rows := list(auth.User{}, ""); code != 200 || len(rows) != 0 {
		t.Fatalf("%d %d", code, len(rows))
	}
	// Someone who reads the source sees all its records, also that of a drawn list that was deleted.
	e.db.MustExec(`DELETE FROM lists WHERE id = $1`, drawn[1])
	src := auth.User{GetListIDs: []int{e.listID}, ListPermissionsMap: map[int]map[string]struct{}{e.listID: {auth.PermListGet: {}}}}
	code, rows := list(src, "?source_id="+itoa(e.listID))
	if code != 200 || len(rows) != 2 || rows[0].ListID != nil && rows[1].ListID != nil || rows[0].DeletedAt == nil && rows[1].DeletedAt == nil {
		t.Fatalf("%d %+v", code, rows)
	}
}
