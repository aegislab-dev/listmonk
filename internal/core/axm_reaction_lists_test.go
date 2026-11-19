package core

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
)

func TestReactionSettings(t *testing.T) {
	settings := models.ReactionSettings{Click: models.ReactionNo}
	if err := settings.Validate(); err != nil || settings.Open != models.ReactionAny || settings.Click != models.ReactionNo {
		t.Fatalf("defaults: %+v, %v", settings, err)
	}
	for _, settings := range []models.ReactionSettings{{Open: "invalid"}, {Click: "invalid"}} {
		if err := settings.Validate(); err == nil {
			t.Fatalf("accepted %+v", settings)
		}
	}
}

func TestMaskReactionSample(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"alice@example.test", "al***@e***.test"},
		{"a@local", "a***@l***"},
		{"나래@예시.한국", "나래***@예***.한국"},
		{"invalid", "***"},
		{"a@", "***"},
	} {
		if got := maskEmail(tc.raw); got != tc.want {
			t.Errorf("maskEmail(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if maskName("  김가람") != "김*" || maskName("  ") != "" {
		t.Fatal("name masking does not handle leading whitespace and Unicode")
	}
}

func TestReactionDefaultName(t *testing.T) {
	raw, err := os.ReadFile("../../i18n/ko.json")
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{i18n: lang}
	at := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	settings := models.ReactionSettings{Open: models.ReactionYes, Click: models.ReactionNo}
	suffix := " · 열람함·클릭 안 함 · 2026-11-18 · 1,520명"
	if got := c.reactionDefaultName("북미", settings, at, 1520); got != "북미"+suffix {
		t.Fatalf("unexpected default %q", got)
	}
	name := c.reactionDefaultName(strings.Repeat("한", 240), settings, at, 1520)
	if utf8.RuneCountInString(name) != 200 || !strings.HasSuffix(name, suffix) {
		t.Fatal(name)
	}
	next := uniqueDrawName(map[string]bool{name: true}, name)
	if utf8.RuneCountInString(next) != 200 || !strings.HasSuffix(next, " (2)") {
		t.Fatal(next)
	}
}
