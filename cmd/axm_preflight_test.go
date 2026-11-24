package main

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

func TestStockFrom(t *testing.T) {
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
		if got := stockFrom(from); got != want {
			t.Errorf("%q: got %v, want %v", from, got, want)
		}
	}
}

func TestSendEnvelope(t *testing.T) {
	hdr := func(sets ...map[string]string) models.Headers { return models.Headers(sets) }
	type srv = map[string]string
	for _, c := range []struct {
		name      string
		messenger string
		headers   models.Headers
		server    srv
		replyTo   string
		policy    bool
		want      [3]string // subject, from, reply-to
	}{
		{"stored values", "email", nil, nil, "", false, [3]string{"S", "F <f@x.test>", ""}},
		{"the reply address is only used under the sender policy", "email", nil, nil, "r@x.test", false, [3]string{"S", "F <f@x.test>", ""}},
		{"under the policy it is used", "email", nil, nil, "r@x.test", true, [3]string{"S", "F <f@x.test>", "r@x.test"}},
		{"headers replace the fields (any letter case)", "email", hdr(srv{"from": "E <e@x.test>", "SUBJECT": "H", "Reply-to": "h@x.test"}), nil, "", false, [3]string{"H", "E <e@x.test>", "h@x.test"}},
		{"under the policy the campaign's reply address wins over a header", "email", hdr(srv{"Reply-To": "h@x.test"}), nil, "r@x.test", true, [3]string{"S", "F <f@x.test>", "r@x.test"}},
		{"a header reply address counts when the campaign has none", "email", hdr(srv{"Reply-To": "h@x.test"}), nil, "", true, [3]string{"S", "F <f@x.test>", "h@x.test"}},
		{"the first set that has it counts", "email-alt", hdr(srv{"X-A": "1"}, srv{"Subject": "one"}, srv{"Subject": "two"}), nil, "", false, [3]string{"one", "F <f@x.test>", ""}},
		{"the SMTP server's headers replace the fields", "email", nil, srv{"Subject": "SMTP subject", "From": "T <t@x.test>", "Reply-To": "tr@x.test"}, "", false, [3]string{"SMTP subject", "T <t@x.test>", "tr@x.test"}},
		{"a header of the campaign replaces the server's", "email", hdr(srv{"Subject": "Own"}), srv{"Subject": "SMTP subject", "From": "T <t@x.test>"}, "", false, [3]string{"Own", "T <t@x.test>", ""}},
		{"the sender policy's reply address is set after the server's", "email", nil, srv{"Reply-To": "tr@x.test"}, "r@x.test", true, [3]string{"S", "F <f@x.test>", "r@x.test"}},
		{"another messenger ignores the headers", "webhook", hdr(srv{"From": "E <e@x.test>", "Subject": "H"}), srv{"Subject": "X"}, "r@x.test", true, [3]string{"S", "F <f@x.test>", ""}},
	} {
		subject, from, reply := sendEnvelope("S", "F <f@x.test>", c.replyTo, c.messenger, c.server, c.headers, c.policy)
		if got := [3]string{subject, from, reply}; got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
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
		From: "Team <team@crevasse.ai>", Unsubscribe: true, UnsubscribeKnown: true,
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
		{"no list left only warns for an opt-in campaign", with(func(f *preflightFacts) { f.LiveLists, f.Total, f.CampType = 0, 0, "optin" }),
			map[string]string{"lists.none": "warn", "from.placeholder": "ok"}},
		{"no list left on resume only warns", with(func(f *preflightFacts) { f.Mode, f.LiveLists, f.Remaining = "resume", 0, 0 }),
			map[string]string{"lists.none": "warn", "from.placeholder": "ok", "unsubscribe.missing": "ok"}},
		{"servers that disagree about a header", with(func(f *preflightFacts) { f.HeadersVary = true }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "mail.headers": "warn"}},
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
		{"recent rule with the number is information, not a warning", with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentScope, f.RecentExcluded = true, 14, "all", 42 }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "recent.applied": "info"}},
		{"recent rule that leaves out everybody blocks", with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentScope, f.RecentExcluded = true, 14, "all", 300 }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "recent.applied": "info", "recent.all": "block"}},
		{"recent rule that leaves out all but some does not block", with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentScope, f.RecentExcluded = true, 14, "all", 299 }),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "recent.applied": "info"}},
		{"recent rule on resume with nobody left only warns", with(func(f *preflightFacts) {
			f.Mode, f.Remaining, f.RecentEnabled, f.RecentDays, f.RecentScope, f.RecentExcluded, f.RecentHeld = "resume", 0, true, 14, "all", 300, 300
		}),
			map[string]string{"from.placeholder": "ok", "unsubscribe.missing": "ok", "recent.applied": "info", "recipients.zero": "warn"}},
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

	// The old line that said the rule is not applied is gone.
	for _, ch := range preflightChecks(with(func(f *preflightFacts) { f.RecentEnabled, f.RecentDays, f.RecentExcluded = true, 7, 3 })) {
		if ch.Key == "recent.notApplied" {
			t.Errorf("the rule is applied now: %v", ch)
		}
	}
}

// A scheduled campaign is read as "scheduled" and, after a restart, as "running": a check
// holds only when it holds in every one of them (LST-48 review).
func TestPreflightEveryStatus(t *testing.T) {
	if got := manager.SendStatuses(null.Time{}); len(got) != 1 || got[0] != models.CampaignStatusRunning {
		t.Fatalf("start: %v", got)
	}
	if got := manager.SendStatuses(null.TimeFrom(time.Now())); len(got) != 2 || got[0] != models.CampaignStatusScheduled || got[1] != models.CampaignStatusRunning {
		t.Fatalf("scheduled: %v", got)
	}

	both := []string{"scheduled", "running"}
	errBad := errors.New("bad")
	for _, tc := range []struct {
		name      string
		has       map[string]bool // missing key: cannot be rendered
		ok, known bool
	}{
		{"in both", map[string]bool{"scheduled": true, "running": true}, true, true},
		{"scheduled only", map[string]bool{"scheduled": true, "running": false}, false, true},
		{"running only", map[string]bool{"scheduled": false, "running": true}, false, true},
		{"missing in one, the other not rendered", map[string]bool{"running": false}, false, true},
		{"in one, the other not rendered", map[string]bool{"running": true}, true, true},
		{"none rendered", map[string]bool{}, true, false},
	} {
		ok, known := unsubscribeInAll(both, func(s string) (bool, error) {
			h, found := tc.has[s]
			if !found {
				return false, errBad
			}
			return h, nil
		})
		if ok != tc.ok || known != tc.known {
			t.Errorf("%s: got %v %v", tc.name, ok, known)
		}
	}

	// Links as CheckTracking counts them: tracked ones and untracked destinations.
	Links := func(tracked int, untracked ...string) manager.TrackingLinks {
		lk := manager.TrackingLinks{UntrackedURLs: []string{}}
		for i := 0; i < tracked; i++ {
			lk.All = append(lk.All, manager.TrackedLink{Kind: manager.TrackingViewManual, Dest: fmt.Sprintf("https://t.invalid/%d", i)})
			lk.Total++
			lk.Manual++
		}
		for _, u := range untracked {
			lk.All = append(lk.All, manager.TrackedLink{Kind: "untracked", Dest: u})
			lk.Total++
			lk.Untracked++
			if len(lk.UntrackedURLs) < manager.MaxUntrackedURLs {
				lk.UntrackedURLs = append(lk.UntrackedURLs, u)
			}
		}
		return lk
	}
	reps := map[string]manager.TrackingReport{
		// The review case: the pixel and an untracked link only while "scheduled".
		"scheduled": {State: manager.TrackingStateWarn, View: manager.TrackingViewManual, Links: Links(0, "https://example.invalid/a")},
		"running":   {State: manager.TrackingStateWarn, View: manager.TrackingViewMissing, Links: Links(0)},
	}
	get := func(s string) (manager.TrackingReport, error) { return reps[s], nil }
	r, err := trackingInAll(both, get)
	if err != nil || r.View != manager.TrackingViewMissing || r.Links.Untracked != 1 || r.Links.Total != 1 || r.State != manager.TrackingStateWarn {
		t.Fatalf("both warnings: %+v %v", r, err)
	}
	if r, err := trackingInAll([]string{"scheduled"}, get); err != nil || r.View != manager.TrackingViewManual || r.Links.Untracked != 1 {
		t.Fatalf("one status: %+v %v", r, err)
	}
	// Different untracked links in each status are all counted, once, with the tracked ones.
	reps["scheduled"] = manager.TrackingReport{State: manager.TrackingStateWarn, View: manager.TrackingViewManual, Links: Links(1, "https://a.invalid", "https://b.invalid")}
	reps["running"] = manager.TrackingReport{State: manager.TrackingStateWarn, View: manager.TrackingViewManual, Links: Links(1, "https://b.invalid", "https://c.invalid")}
	if r, _ := trackingInAll(both, get); r.Links.Untracked != 3 || r.Links.Total != 4 || r.Links.Manual != 1 || len(r.Links.UntrackedURLs) != 3 {
		t.Fatalf("links of both: %+v", r)
	}
	// Past the capped sample of addresses (LST-48 review): 20 common links, then one of each.
	var common []string
	for i := 0; i < 20; i++ {
		common = append(common, fmt.Sprintf("https://common.invalid/%d", i))
	}
	reps["scheduled"] = manager.TrackingReport{State: manager.TrackingStateWarn, View: manager.TrackingViewManual, Links: Links(0, append(slices.Clone(common), "https://only-scheduled.invalid")...)}
	reps["running"] = manager.TrackingReport{State: manager.TrackingStateWarn, View: manager.TrackingViewManual, Links: Links(0, append(slices.Clone(common), "https://only-running.invalid")...)}
	if r, _ := trackingInAll(both, get); r.Links.Untracked != 22 || r.Links.Total != 22 || len(r.Links.UntrackedURLs) != manager.MaxUntrackedURLs {
		t.Fatalf("past the sample: untracked %d total %d sample %d", r.Links.Untracked, r.Links.Total, len(r.Links.UntrackedURLs))
	}
	// Tracked in both: ok.
	reps["scheduled"] = manager.TrackingReport{State: manager.TrackingStateOK, View: manager.TrackingViewManual, Links: Links(1)}
	reps["running"] = reps["scheduled"]
	if r, _ := trackingInAll(both, get); r.State != manager.TrackingStateOK || r.Links.Total != 1 {
		t.Fatalf("ok in both: %+v", r)
	}
	if _, err := trackingInAll(both, func(s string) (manager.TrackingReport, error) {
		if s == "running" {
			return manager.TrackingReport{}, errBad
		}
		return reps[s], nil
	}); err == nil {
		t.Fatal("an error in one status is an error")
	}
}

// The Cc and Bcc headers add addresses to every e-mail: the campaign's first set that has
// the header, over the SMTP server's, split on commas (LST-48 review: 3 people were shown
// while a Cc address got a copy of each mail).
func TestEnvelopeCopies(t *testing.T) {
	got := envelopeCopies("email", map[string]string{"Cc": "server@x.invalid"},
		models.Headers{{"cc": "A <a@x.invalid>, b@x.invalid"}, {"Cc": "ignored@x.invalid"}, {"Bcc": "c@x.invalid, B@X.invalid"}})
	want := []mailCopy{{"cc", "a@x.invalid"}, {"cc", "b@x.invalid"}, {"bcc", "c@x.invalid"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := envelopeCopies("email", map[string]string{"Bcc": "audit@x.invalid"}, nil); len(got) != 1 || got[0].Kind != "bcc" {
		t.Fatalf("server only: %+v", got)
	}
	if got := envelopeCopies("postback", nil, models.Headers{{"Cc": "a@x.invalid"}}); len(got) != 0 {
		t.Fatalf("not e-mail: %+v", got)
	}

	f := preflightFacts{Mode: "start", CampType: "regular", Messenger: "email", Total: 3, LiveLists: 1, ListCount: 1,
		From: "News <news@crevasse.ai>", Unsubscribe: true, UnsubscribeKnown: true, Copies: []string{"a@x.invalid"}}
	var line *models.PreflightCheck
	for _, c := range preflightChecks(f) {
		if c.Key == "mail.copies" {
			c := c
			line = &c
		}
	}
	if line == nil || line.Level != "warn" || line.Params["mails"] != 3 || line.Params["n"] != 1 {
		t.Fatalf("copies line: %+v", line)
	}
}
