package utils

import "testing"

func TestSanitizeURI(t *testing.T) {
	cases := map[string]string{
		"":                                 "/",
		"   ":                              "/",
		"/":                                "/",
		"/admin/lists":                     "/admin/lists",
		"/admin/lists/":                    "/admin/lists",
		"https://evil.example/admin/lists": "/admin/lists",
		"//evil.example/admin":             "/admin",
		"/admin/../etc":                    "/",
		"/admin/campaigns/analytics?id=52": "/admin/campaigns/analytics?id=52",
		"/admin/campaigns/analytics?id=52&from=1&to=2": "/admin/campaigns/analytics?id=52&from=1&to=2",
		"https://evil.example/a?next=//x.example":      "/a?next=//x.example",
	}
	for in, want := range cases {
		if got := SanitizeURI(in); got != want {
			t.Errorf("SanitizeURI(%q) = %q, want %q", in, got, want)
		}
	}
}
