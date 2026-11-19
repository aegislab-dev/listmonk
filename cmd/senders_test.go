package main

import (
	"encoding/json"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

func TestResolveFrom(t *testing.T) {
	for _, tc := range []struct {
		name, email, want string
		valid             bool
	}{
		{"홍 길동, 팀", "a@crevasse.ai", `"홍 길동, 팀" <a@crevasse.ai>`, true},
		{"A", "a@CREVASSE.ai", `"A" <a@CREVASSE.ai>`, true},
		{"A", "a@newsletter.crevasse.ai", "Team <team@crevasse.ai>", true},
		{"A", "a@example.com", "Team <team@crevasse.ai>", true},
		{"API", "", "Team <team@crevasse.ai>", false},
		{"A", "Name <a@crevasse.ai>", `"A" <a@crevasse.ai>`, true},
	} {
		u := auth.User{Name: tc.name, Email: null.NewString(tc.email, tc.valid)}
		if got := resolveFrom(u, "Team <team@crevasse.ai>", "app@example.org", []string{"crevasse.ai"}); got != tc.want {
			t.Errorf("%s: %s != %s", tc.email, got, tc.want)
		}
	}
	if got := resolveFrom(auth.User{}, "", "app@example.org", nil); got != "app@example.org" {
		t.Fatal(got)
	}
}

// An API user must use the configured fallback even if it has an internal email.
func TestResolveFromAPIUser(t *testing.T) {
	user := auth.User{Type: auth.UserTypeAPI, Name: "API", Email: null.StringFrom("api@crevasse.ai")}
	for _, tc := range []struct{ defaultFrom, want string }{
		{"Team <team@crevasse.ai>", "Team <team@crevasse.ai>"},
		{"", "app@example.org"},
	} {
		if got := resolveFrom(user, tc.defaultFrom, "app@example.org", []string{"crevasse.ai"}); got != tc.want {
			t.Errorf("API user with internal email: got %q, want %q", got, tc.want)
		}
	}
}

func TestReplyToAllowed(t *testing.T) {
	for _, tc := range []struct {
		addr          string
		want, invalid bool
	}{
		{"", true, false}, {"a@CREVASSE.ai", true, false}, {"Name <verified@example.com>", true, false},
		{"a@sub.crevasse.ai", false, false}, {"no@example.com", false, false},
		{"a@crevasse.ai, b@crevasse.ai", false, true}, {"bad", false, true},
	} {
		got, err := replyToAllowed(tc.addr, auth.User{}, []string{"crevasse.ai"}, func(email string) (bool, error) { return email == "verified@example.com", nil })
		if got != tc.want || (err != nil) != tc.invalid {
			t.Errorf("%s: %v, %v", tc.addr, got, err)
		}
	}
}

func TestReservedSenderHeaders(t *testing.T) {
	for _, k := range []string{"reply-to", "REPLY-TO", "From", "sender", "Return-pATH", " From "} {
		if !hasReservedHeader([]map[string]string{{k: "anything"}}) {
			t.Fatal(k)
		}
	}
	if hasReservedHeader([]map[string]string{{"X-Custom": "Reply-To"}}) {
		t.Fatal("custom header rejected")
	}
}

func TestSenderSettings(t *testing.T) {
	for _, tc := range []struct {
		domains     []string
		from, reply string
		valid       bool
	}{
		{nil, "", "", true},
		{[]string{" CREVASSE.AI ", "crevasse.ai"}, "Team <team@crevasse.ai>", "a@example.com", true},
		{[]string{"crevasse.ai"}, "a@example.com", "", false},
		{[]string{"bad..com"}, "", "", false},
		{[]string{"-bad.com"}, "", "", false},
		{[]string{"x@y.com"}, "", "", false},
		{[]string{"crevasse.ai"}, "", "a@example.com\r\nBcc: b@example.com", false},
	} {
		set := models.Settings{AxmSenderDomains: tc.domains, AxmDefaultFrom: tc.from, AxmDefaultReplyTo: tc.reply}
		if err := normalizeSenderSettings(&set); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
		if tc.valid && len(set.AxmSenderDomains) > 1 {
			t.Fatal("not deduplicated")
		}
	}
}

func TestMissingReplyVsEmpty(t *testing.T) {
	for _, raw := range []string{`{}`, `{"reply_to":""}`, `{"reply_to":"a@example.com"}`} {
		var req campReq
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if (req.ReplyToInput == nil) != (raw == `{}`) {
			t.Fatal("missing reply indistinguishable from empty", raw)
		}
	}
}

func TestDefaultReply(t *testing.T) {
	cfg := &Config{SenderDomains: []string{"crevasse.ai"}, DefaultReplyTo: "team@example.com"}
	for _, email := range []string{"a@crevasse.ai", "a@example.com", ""} {
		got := defaultReplyTo(auth.User{Email: null.StringFrom(email)}, cfg)
		if (got == "") != (email == "a@crevasse.ai") {
			t.Fatal(email, got)
		}
	}
}

func TestPlaceholderFrom(t *testing.T) {
	for v, want := range map[string]bool{
		"change-me@crevasse.ai":          true,
		"Team <Change-Me@crevasse.ai>":   true,
		"changeme@crevasse.ai":           true,
		"change_me@crevasse.ai":          true,
		"a@example.com":                  true,
		"a@mail.example.org":             true,
		"a@example.net":                  true,
		"a@foo.example":                  true,
		"a@foo.invalid":                  true,
		"a@foo.test":                     true,
		"aegislab_us@crevasse.ai":        false,
		"Team <aegislab_us@crevasse.ai>": false,
		"noreply@listmonk.yoursite.com":  false, // only the settings list refuses the stock address
		"a@notexample.com":               false,
		"change-me-not@crevasse.ai":      false,
		"not an address":                 false,
		"":                               false,
	} {
		if got := placeholderFrom(v); got != want {
			t.Errorf("placeholderFrom(%q) = %v, want %v", v, got, want)
		}
	}
	if !placeholderListFrom("noreply@listmonk.yoursite.com") || !placeholderListFrom("a@x.yoursite.com") || placeholderListFrom("a@crevasse.ai") {
		t.Fatal("placeholderListFrom")
	}
}

func TestMatchFromAddress(t *testing.T) {
	list := []string{"aegislab_us@crevasse.ai", "AEGIS 팀 <team@crevasse.ai>"}
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"aegislab_us@crevasse.ai", "aegislab_us@crevasse.ai", true},
		{"  aegislab_us@crevasse.ai", "aegislab_us@crevasse.ai", true},
		{"AegisLab_US@Crevasse.AI", "aegislab_us@crevasse.ai", true},
		{"Other name <aegislab_us@crevasse.ai>", "aegislab_us@crevasse.ai", true},
		{"AEGIS 팀 <team@crevasse.ai>", "AEGIS 팀 <team@crevasse.ai>", true},
		{"team@crevasse.ai", "AEGIS 팀 <team@crevasse.ai>", true},
		{"change-me@crevasse.ai", "", false},
		{"other@crevasse.ai", "", false},
		{"", "", false},
		{"   ", "", false},
		{"not an address", "", false},
	} {
		got, ok := matchFromAddress(tc.in, list)
		if got != tc.want || ok != tc.ok {
			t.Errorf("matchFromAddress(%q) = %q,%v want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := matchFromAddress("aegislab_us@crevasse.ai", nil); ok {
		t.Fatal("empty list matches nothing")
	}
	if got := defaultFromFor("team@crevasse.ai", list); got != "AEGIS 팀 <team@crevasse.ai>" {
		t.Fatal(got)
	}
	if got := defaultFromFor("listmonk <noreply@listmonk.yoursite.com>", list); got != list[0] {
		t.Fatal(got)
	}
}

func TestNormalizeFromAddresses(t *testing.T) {
	got, err := normalizeFromAddresses([]string{
		"  Aegislab_US@Crevasse.ai ", "aegislab_us@crevasse.ai", "AEGIS 팀 <Team@Crevasse.ai>",
		`"Kim, Min" <kim@crevasse.ai>`, "AEGIS 팀 <team@crevasse.ai>",
	})
	want := []string{"aegislab_us@crevasse.ai", "AEGIS 팀 <team@crevasse.ai>", `"Kim, Min" <kim@crevasse.ai>`}
	if err != nil || len(got) != len(want) {
		t.Fatalf("%v %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q != %q", i, got[i], want[i])
		}
	}
	if got, err := normalizeFromAddresses(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil must become an empty list: %#v %v", got, err)
	}
	for _, bad := range []string{
		"", "   ", "not an address", "change-me@crevasse.ai", "a@example.com", "noreply@listmonk.yoursite.com",
		`"News Team"@example.org`, `"change-me"@crevasse.ai`, `X <"a b"@foo.example>`,
		"a@foo.yoursite.com", "a@crevasse.ai\r\nBcc: x@y.z", "Team <a@crevasse.ai\n>", "a@foo.invalid",
	} {
		if _, err := normalizeFromAddresses([]string{"ok@crevasse.ai", bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	// A local part that needs quotes keeps them, so the entry is still a valid From.
	got, err = normalizeFromAddresses([]string{`"two words"@crevasse.ai`, `Kim <"a b"@crevasse.ai>`})
	if err != nil || len(got) != 2 || got[0] != `"two words"@crevasse.ai` || got[1] != `Kim <"a b"@crevasse.ai>` {
		t.Fatalf("%q %v", got, err)
	}
	if a, ok := matchFromAddress(`"two words"@crevasse.ai`, got); !ok || a != got[0] {
		t.Fatal(a, ok)
	}
	// A line break anywhere, also at either end, is refused rather than trimmed.
	for _, bad := range []string{"a@crevasse.ai\n", "\na@crevasse.ai", "a@crevasse.ai\r\n"} {
		if _, err := normalizeFromAddresses([]string{bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	// The settings normalizer applies it too.
	set := models.Settings{AxmFromAddresses: []string{" A@crevasse.ai "}}
	if err := normalizeSenderSettings(&set); err != nil || set.AxmFromAddresses[0] != "a@crevasse.ai" {
		t.Fatal(set.AxmFromAddresses, err)
	}
	set = models.Settings{AxmFromAddresses: []string{"change-me@crevasse.ai"}}
	if normalizeSenderSettings(&set) == nil {
		t.Fatal("placeholder must be refused in settings")
	}
}

func TestHasFromHeader(t *testing.T) {
	for _, tc := range []struct {
		h    []map[string]string
		want bool
	}{
		{nil, false},
		{[]map[string]string{{"X-Team": "a"}}, false},
		{[]map[string]string{{"Reply-To": "a@crevasse.ai"}}, false},
		{[]map[string]string{{"From": "a@crevasse.ai"}}, true},
		{[]map[string]string{{"X-Team": "a"}, {" fRom ": "a@crevasse.ai"}}, true},
	} {
		if got := hasFromHeader(tc.h); got != tc.want {
			t.Errorf("%v: %v", tc.h, got)
		}
	}
}
