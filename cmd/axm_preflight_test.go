package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

func TestPlaceholderFrom(t *testing.T) {
	for from, want := range map[string]bool{
		"에이지스랩 <aegislab_us@crevasse.ai>":          false,
		"team@crevasse.ai":                         false,
		"noreply@listmonk.yoursite.com":            true,
		"Listmonk <noreply@listmonk.yoursite.com>": true,
		"change-me@crevasse.ai":                    true,
		"Change_Me@crevasse.ai":                    false, // only "change-me" and "changeme" are stock values
		"changeme@crevasse.ai":                     true,
		"test@example.com":                         true,
		" team@crevasse.ai":                        true,
		"team@crevasse.ai ":                        true,
		"not an address":                           true,
		"you@crevasse.ai":                          true,
		"yourname@crevasse.ai":                     true,
		"youngmin@crevasse.ai":                     false,
	} {
		if got := placeholderFrom(from); got != want {
			t.Errorf("%q: got %v, want %v", from, got, want)
		}
	}
}

func TestUnsubscribeLink(t *testing.T) {
	const link = `<a href="{{ UnsubscribeURL }}">x</a>`
	for _, c := range []struct {
		name       string
		f          preflightFacts
		has, known bool
	}{
		{"in the body", preflightFacts{Body: link, TemplateBody: `{{ template "content" . }}`}, true, true},
		{"in the template", preflightFacts{Body: "hi", TemplateBody: `{{ template "content" . }}` + link}, true, true},
		{"nowhere", preflightFacts{Body: "hi", TemplateBody: `<html>{{ template "content" . }}</html>`}, false, true},
		{"visual body decides alone", preflightFacts{ContentType: models.CampaignContentTypeVisual, Body: "hi", TemplateBody: link}, false, true},
		{"template unknown", preflightFacts{Body: "hi"}, false, false},
		{"template calls another template", preflightFacts{Body: "hi", TemplateBody: `{{ template "footer" . }}`}, false, false},
		{"a body that calls another one", preflightFacts{Body: `{{ template "footer" . }}`, TemplateBody: "x"}, false, false},
	} {
		has, known := unsubscribeLink(c.f)
		if has != c.has || known != c.known {
			t.Errorf("%s: got has=%v known=%v, want %v %v", c.name, has, known, c.has, c.known)
		}
	}
}

func TestPreflightMode(t *testing.T) {
	at := null.TimeFrom(time.Now())
	for _, c := range []struct {
		status string
		sendAt null.Time
		want   string
	}{
		{models.CampaignStatusDraft, null.Time{}, "start"},
		{models.CampaignStatusDraft, at, "schedule"},
		{models.CampaignStatusPaused, null.Time{}, "resume"},
		{models.CampaignStatusPaused, at, "resume"},
	} {
		if got := preflightMode(c.status, c.sendAt); got != c.want {
			t.Errorf("%s %v: %s", c.status, c.sendAt.Valid, got)
		}
	}
}

func TestPreflightChecks(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	good := preflightFacts{
		Mode: "start", CampType: "regular", Messenger: "email", ContentType: "richtext",
		Total: 300, Remaining: 300, ListCount: 2, LiveLists: 2, Now: now,
		From: "Team <team@crevasse.ai>", Body: `{{ UnsubscribeURL }}`, RecentExcluded: -1,
	}
	with := func(f func(*preflightFacts)) preflightFacts { c := good; f(&c); return c }

	for _, c := range []struct {
		name string
		f    preflightFacts
		want map[string]string // key -> level; a key that is not here must be absent
	}{
		{"all good", good, map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"nobody blocks a regular campaign", with(func(f *preflightFacts) { f.Total = 0 }),
			map[string]string{"recipients.zero": "block", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"nobody only warns for an opt-in campaign", with(func(f *preflightFacts) { f.Total, f.CampType = 0, "optin" }),
			map[string]string{"recipients.zero": "warn", "from.placeholder": "ok"}},
		{"nobody left on resume only warns", with(func(f *preflightFacts) { f.Mode, f.Total, f.Remaining = "resume", 40, 0 }),
			map[string]string{"recipients.zero": "warn", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"people left on resume", with(func(f *preflightFacts) { f.Mode, f.Total, f.Remaining = "resume", 0, 12 }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"no list left blocks", with(func(f *preflightFacts) { f.LiveLists, f.Total = 0, 0 }),
			map[string]string{"lists.none": "block", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"scheduled in the past", with(func(f *preflightFacts) { f.Mode, f.SendAt = "schedule", null.TimeFrom(now.Add(-time.Minute)) }),
			map[string]string{"schedule.past": "warn", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"scheduled ahead", with(func(f *preflightFacts) { f.Mode, f.SendAt = "schedule", null.TimeFrom(now.Add(time.Hour)) }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"stock from", with(func(f *preflightFacts) { f.From = "noreply@listmonk.yoursite.com" }),
			map[string]string{"from.placeholder": "warn", "unsubscribe.missing": "ok"}},
		{"no unsubscribe link", with(func(f *preflightFacts) { f.Body, f.TemplateBody = "hi", "{{ template \"content\" . }}" }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "warn"}},
		{"unsubscribe link not known", with(func(f *preflightFacts) { f.Body = "hi" }),
			map[string]string{"from.placeholder": "ok"}},
		{"another messenger has no such line", with(func(f *preflightFacts) { f.Messenger, f.Body = "webhook", "hi" }),
			map[string]string{"from.placeholder": "ok"}},
		{"opt-in campaigns have no such line", with(func(f *preflightFacts) { f.CampType, f.Body = "optin", "hi" }),
			map[string]string{"from.placeholder": "ok"}},
		{"recent rule with the number", with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentExcluded = true, 14, 42 }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "recent.notApplied": "warn"}},
		{"recent rule on an opt-in campaign is not shown", with(func(f *preflightFacts) { f.RecentEnabled, f.CampType = true, "optin" }),
			map[string]string{"from.placeholder": "ok"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]string{}
			for _, ch := range preflightChecks(c.f) {
				if _, dup := got[ch.Key]; dup {
					t.Fatalf("%s twice", ch.Key)
				}
				got[ch.Key] = ch.Level
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}

	// The number the recent rule would exclude is left out when it could not be counted.
	for excluded, wantKey := range map[int]bool{42: true, 0: true, -1: false} {
		f := with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentExcluded = true, 7, excluded })
		for _, ch := range preflightChecks(f) {
			if ch.Key == "recent.notApplied" {
				if _, has := ch.Params["excluded"]; has != wantKey {
					t.Errorf("excluded %d: params %v", excluded, ch.Params)
				}
			}
		}
	}
}
