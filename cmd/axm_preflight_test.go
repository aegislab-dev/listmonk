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

func TestSendEnvelope(t *testing.T) {
	hdr := func(sets ...map[string]string) models.Headers { return models.Headers(sets) }
	for _, c := range []struct {
		name                             string
		messenger                        string
		headers                          models.Headers
		replyTo                          string
		policy                           bool
		wantSubject, wantFrom, wantReply string
	}{
		{"stored values", "email", nil, "", false, "S", "F <f@x.test>", ""},
		{"the reply address is only used under the sender policy", "email", nil, "r@x.test", false, "S", "F <f@x.test>", ""},
		{"under the policy it is used", "email", nil, "r@x.test", true, "S", "F <f@x.test>", "r@x.test"},
		{"headers replace the fields (any letter case)", "email", hdr(map[string]string{"from": "E <e@x.test>", "SUBJECT": "H", "Reply-to": "h@x.test"}), "", false, "H", "E <e@x.test>", "h@x.test"},
		{"under the policy the campaign's reply address wins over a header", "email", hdr(map[string]string{"Reply-To": "h@x.test"}), "r@x.test", true, "S", "F <f@x.test>", "r@x.test"},
		{"a header reply address counts when the campaign has none", "email", hdr(map[string]string{"Reply-To": "h@x.test"}), "", true, "S", "F <f@x.test>", "h@x.test"},
		{"the first set that has it counts", "email-alt", hdr(map[string]string{"X-A": "1"}, map[string]string{"Subject": "one"}, map[string]string{"Subject": "two"}), "", false, "one", "F <f@x.test>", ""},
		{"another messenger ignores the headers", "webhook", hdr(map[string]string{"From": "E <e@x.test>", "Subject": "H"}), "r@x.test", true, "S", "F <f@x.test>", ""},
	} {
		subject, from, reply := sendEnvelope("S", "F <f@x.test>", c.replyTo, c.messenger, c.headers, c.policy)
		if subject != c.wantSubject || from != c.wantFrom || reply != c.wantReply {
			t.Errorf("%s: got %q %q %q, want %q %q %q", c.name, subject, from, reply, c.wantSubject, c.wantFrom, c.wantReply)
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
		From: "Team <team@crevasse.ai>", Unsubscribe: true, UnsubscribeKnown: true, RecentExcluded: -1,
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
		{"resumed with a time that has passed", with(func(f *preflightFacts) { f.Mode, f.SendAt = "resume", null.TimeFrom(now.Add(-time.Minute)) }),
			map[string]string{"schedule.past": "warn", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"resumed with a time ahead", with(func(f *preflightFacts) { f.Mode, f.SendAt = "resume", null.TimeFrom(now.Add(time.Hour)) }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"no list left on resume only warns", with(func(f *preflightFacts) { f.Mode, f.LiveLists, f.Remaining = "resume", 0, 0 }),
			map[string]string{"lists.none": "warn", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"scheduled ahead", with(func(f *preflightFacts) { f.Mode, f.SendAt = "schedule", null.TimeFrom(now.Add(time.Hour)) }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"stock from", with(func(f *preflightFacts) { f.From = "noreply@listmonk.yoursite.com" }),
			map[string]string{"from.placeholder": "warn", "unsubscribe.missing": "ok"}},
		{"no unsubscribe link", with(func(f *preflightFacts) { f.Unsubscribe = false }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "warn"}},
		{"unsubscribe link not known", with(func(f *preflightFacts) { f.UnsubscribeKnown = false }),
			map[string]string{"from.placeholder": "ok"}},
		{"another messenger has no such line", with(func(f *preflightFacts) { f.Messenger, f.Unsubscribe = "webhook", false }),
			map[string]string{"from.placeholder": "ok"}},
		{"opt-in campaigns have no such line", with(func(f *preflightFacts) { f.CampType, f.Unsubscribe = "optin", false }),
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
