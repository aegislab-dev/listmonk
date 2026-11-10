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
