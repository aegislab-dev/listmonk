package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// presetCall runs a handler the way the route does (behind the permission guard) with the
// :id parameter set. It returns the status and the "data" of the answer.
func (e *teamsEnv) presetCall(h echo.HandlerFunc, guard bool, user auth.User, method, id, body string) (int, json.RawMessage) {
	e.t.Helper()
	if guard {
		h = e.a.auth.Perm(h, "lists:manage_all")
	}
	req := httptest.NewRequest(method, "/api/lists/presets", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(auth.UserHTTPCtxKey, user)
	if id != "" {
		c.SetParamNames("id")
		c.SetParamValues(id)
	}
	if err := h(c); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he.Code, nil
		}
		e.t.Fatal(err)
	}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Data
}

func TestColumnPresetsPermissions(t *testing.T) {
	e := newTeamsEnv(t)
	admin, member, sa := e.as(e.admin), e.as(e.ua), e.as(e.superAdmin)
	body := `{"name":"Sales","columns":["company","title"]}`

	// Whoever may not manage all lists cannot make, edit or delete a preset.
	for _, c := range []struct {
		h              echo.HandlerFunc
		method, id, in string
	}{
		{e.a.CreateColumnPreset, "POST", "", body},
		{e.a.UpdateColumnPreset, "PUT", "1", body},
		{e.a.DeleteColumnPreset, "DELETE", "1", ""},
	} {
		if code, _ := e.presetCall(c.h, true, member, c.method, c.id, c.in); code != http.StatusForbidden {
			t.Fatalf("%s as a member: %d", c.method, code)
		}
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_column_presets`)
	if n != 0 {
		t.Fatal("a refused call changed data", n)
	}

	code, data := e.presetCall(e.a.CreateColumnPreset, true, admin, "POST", "", body)
	var made struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
	}
	json.Unmarshal(data, &made)
	if code != 200 || made.ID != "u1" || made.Name != "Sales" || len(made.Columns) != 2 {
		t.Fatal(code, string(data))
	}
	if code, _ := e.presetCall(e.a.CreateColumnPreset, true, sa, "POST", "", `{"name":"Super","columns":["a"]}`); code != 200 {
		t.Fatal("super admin", code)
	}
	// A bad request is a 400, not a 500.
	for _, in := range []string{`{"name":"","columns":["a"]}`, `{"name":"x","columns":[]}`, `{"name":"sales","columns":["a"]}`, `not json`} {
		if code, _ := e.presetCall(e.a.CreateColumnPreset, true, admin, "POST", "", in); code != 400 {
			t.Fatalf("%s: %d", in, code)
		}
	}

	// Anybody who is logged in reads the presets; only the one who manages all lists sees
	// the names of the lists that use one (other teams' lists are not for members to read).
	if _, err := e.core.CreateList(models.List{Name: "Secret team list", AxmPreset: made.ID}); err != nil {
		t.Fatal(err)
	}
	read := func(u auth.User) (counts map[string]int, names int) {
		code, data := e.presetCall(e.a.GetColumnPresets, false, u, "GET", "", "")
		if code != 200 {
			t.Fatal(code)
		}
		var rows []struct {
			ID        string `json:"id"`
			ListCount int    `json:"list_count"`
			Lists     []struct {
				Name string `json:"name"`
			} `json:"lists"`
			Builtin bool `json:"builtin"`
		}
		json.Unmarshal(data, &rows)
		counts = map[string]int{}
		for _, r := range rows {
			counts[r.ID] = r.ListCount
			if r.ID == made.ID {
				names += len(r.Lists)
			} else if r.ID != "creator" && len(r.Lists) > 0 {
				t.Fatal("names of a preset nobody uses:", r.ID)
			}
			if u.UserRoleID != auth.SuperAdminRoleID && !u.HasPerm("lists:manage_all") && len(r.Lists) > 0 {
				t.Fatal("names leaked to", u.Username)
			}
		}
		return counts, names
	}
	counts, names := read(member)
	if counts[made.ID] != 1 || names != 0 || len(counts) != 4 {
		t.Fatal(counts, names)
	}
	if _, names := read(admin); names != 1 {
		t.Fatal("admin sees names:", names)
	}

	// Built-in presets are not rows: their IDs cannot be edited or deleted.
	for _, id := range []string{"creator", "b2b", "u1", "0", "-1", "x"} {
		if code, _ := e.presetCall(e.a.UpdateColumnPreset, true, admin, "PUT", id, body); code != 400 && code != 404 {
			t.Fatalf("PUT %s: %d", id, code)
		}
		if id == "u1" || id == "creator" || id == "b2b" || id == "0" || id == "-1" || id == "x" {
			if code, _ := e.presetCall(e.a.DeleteColumnPreset, true, admin, "DELETE", id, ""); code != 400 && code != 404 {
				t.Fatalf("DELETE %s: %d", id, code)
			}
		}
	}
	e.get(&n, `SELECT COUNT(*) FROM axm_column_presets`)
	if n != 2 {
		t.Fatal("presets:", n)
	}

	// Editing and deleting through the API; the answer says how many lists moved.
	if code, _ := e.presetCall(e.a.UpdateColumnPreset, true, admin, "PUT", "1", `{"name":"Sales 2","columns":["title","company","industry"]}`); code != 200 {
		t.Fatal(code)
	}
	code, data = e.presetCall(e.a.DeleteColumnPreset, true, admin, "DELETE", "1", "")
	var del struct {
		Lists   int    `json:"lists"`
		Default string `json:"default"`
	}
	json.Unmarshal(data, &del)
	if code != 200 || del.Lists != 1 || del.Default != "creator" {
		t.Fatal(code, string(data))
	}
	if _, names := read(admin); names != 0 {
		t.Fatal(names)
	}

	// The server config carries the presets (built-in first, then the others).
	cfg := e.a.columnPresetsForConfig()
	if len(cfg) != 3 || cfg[0].ID != "creator" || cfg[1].ID != "b2b" || cfg[2].Name != "Super" {
		t.Fatalf("%+v", cfg)
	}
}
