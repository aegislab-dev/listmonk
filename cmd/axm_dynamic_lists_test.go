package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/labstack/echo/v4"
)

func TestDynamicBasePermissions(t *testing.T) {
	a := &App{}
	for _, tc := range []struct {
		name    string
		user    auth.User
		base    core.DynamicBase
		allowed bool
	}{
		{"global source denied", auth.User{}, core.DynamicBase{Type: "all"}, false},
		{"manage all cannot read all", auth.User{PermissionsMap: map[string]struct{}{auth.PermListManageAll: {}}}, core.DynamicBase{Type: "all"}, false},
		{"read all without SQL", auth.User{PermissionsMap: map[string]struct{}{auth.PermListGetAll: {}}}, core.DynamicBase{Type: "all"}, true},
		{"scoped sources", auth.User{ListPermissionsMap: map[int]map[string]struct{}{2: {auth.PermListGet: {}}}}, core.DynamicBase{Type: "lists", Lists: []int{2}}, true},
		{"one unauthorized source", auth.User{ListPermissionsMap: map[int]map[string]struct{}{2: {auth.PermListGet: {}}}}, core.DynamicBase{Type: "lists", Lists: []int{2, 3}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := echo.New().NewContext(httptest.NewRequest("POST", "/api/lists/dynamic/preview", nil), httptest.NewRecorder())
			c.Set(auth.UserHTTPCtxKey, tc.user)
			err := a.checkDynamicBasePerm(c, core.DynamicFilter{Base: tc.base})
			if (err == nil) != tc.allowed {
				t.Fatal(err)
			}
		})
	}
}

func TestDynamicPreviewManagePermissionBeforeCount(t *testing.T) {
	// core is intentionally nil: denial must happen before accessing subscriber data.
	a := &App{}
	for _, body := range []string{`{"filter":{},"list_id":0}`, `{"filter":{},"list_id":12}`} {
		req := httptest.NewRequest("POST", "/api/lists/dynamic/preview", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.Set(auth.UserHTTPCtxKey, auth.User{})
		if err := a.PreviewDynamicList(c); err != auth.ErrPermDenied {
			t.Fatal(err)
		}
	}
}

func TestDynamicSampleFollowsSubscriberReadPermission(t *testing.T) {
	perms := func(p ...string) auth.User {
		m := map[string]struct{}{}
		for _, name := range p {
			m[name] = struct{}{}
		}
		return auth.User{PermissionsMap: m}
	}
	all := core.DynamicFilter{Base: core.DynamicBase{Type: "all"}}
	lists := core.DynamicFilter{Base: core.DynamicBase{Type: "lists", Lists: []int{2}}}
	for _, tc := range []struct {
		name   string
		user   auth.User
		filter core.DynamicFilter
		want   bool
	}{
		{"no subscriber permission", perms(auth.PermListManageAll, auth.PermListGetAll), all, false},
		{"manage without read", perms(auth.PermSubscribersManage), lists, false},
		{"read all", perms(auth.PermSubscribersGetAll), all, true},
		{"read all with base lists", perms(auth.PermSubscribersGetAll), lists, true},
		{"list scoped read needs base lists", perms(auth.PermSubscribersGet), all, false},
		{"list scoped read with base lists", perms(auth.PermSubscribersGet), lists, true},
	} {
		if got := canSampleDynamic(tc.user, tc.filter); got != tc.want {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
}
