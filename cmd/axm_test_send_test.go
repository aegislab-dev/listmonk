package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// LST-41: which addresses may receive a campaign test message, and as what.
func TestClassifyTestRecipients(t *testing.T) {
	var (
		me      = auth.User{Type: auth.UserTypeUser, Name: "Me", Email: null.StringFrom("Me@Outside.example")}
		api     = auth.User{Type: auth.UserTypeAPI, Name: "API", Email: null.StringFrom("api@outside.example")}
		noEmail = auth.User{Type: auth.UserTypeUser, Name: "No mail"}
		// A password account can type any address into its profile: it has no own address.
		typed   = auth.User{Type: auth.UserTypeUser, Name: "Typed", PasswordLogin: true, Email: null.StringFrom("typed@outside.example")}
		domains = []string{"corp.example"}
		sub     = models.Subscriber{Email: "known@partner.example", Name: "Known"}
		staff   = models.Subscriber{Email: "staff@corp.example"}
	)
	sub.ID, staff.ID = 7, 9
	subs := map[string]models.Subscriber{sub.Email: sub}
	type want struct {
		kind     string
		rejected bool
	}
	for _, tc := range []struct {
		name    string
		email   string
		user    auth.User
		subs    map[string]models.Subscriber
		domains []string
		want    want
	}{
		{"a subscriber the user may see", "known@partner.example", me, subs, domains, want{kind: testKindSubscriber}},
		{"the user's own address, in another case", "me@outside.example", me, subs, domains, want{kind: testKindOwn}},
		{"an API user has no address of its own", "api@outside.example", api, subs, domains, want{rejected: true}},
		{"a user without an address", "someone@outside.example", noEmail, subs, domains, want{rejected: true}},
		{"a password account's profile address", "typed@outside.example", typed, subs, domains, want{rejected: true}},
		{"a password account in a test domain", "anyone@corp.example", typed, subs, domains, want{kind: testKindInternal}},
		{"a test domain", "anyone@corp.example", me, subs, domains, want{kind: testKindInternal}},
		{"a subdomain of a test domain", "anyone@a.corp.example", me, subs, domains, want{rejected: true}},
		{"a domain that only ends like a test domain", "anyone@notcorp.example", me, subs, domains, want{rejected: true}},
		{"an outside address that is no subscriber", "stranger@outside.example", me, subs, domains, want{rejected: true}},
		// A subscriber of a list the user may not see is simply not in subs.
		{"a subscriber the user may not see", "known@partner.example", me, nil, domains, want{rejected: true}},
		{"no test domains: the own address still works", "me@outside.example", me, subs, nil, want{kind: testKindOwn}},
		{"no test domains: nothing else does", "anyone@corp.example", me, subs, nil, want{rejected: true}},
		{"a subscriber in a test domain stays a subscriber", "staff@corp.example", me,
			map[string]models.Subscriber{staff.Email: staff}, domains, want{kind: testKindSubscriber}},
		{"an address with a display name", "name <anyone@corp.example>", me, subs, domains, want{rejected: true}},
		{"not an address", "corp.example", me, subs, domains, want{rejected: true}},
	} {
		got, rejected, err := classifyTestRecipients([]string{tc.email}, tc.subs, tc.user, tc.domains)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if tc.want.rejected {
			if len(got) != 0 || !reflect.DeepEqual(rejected, []string{tc.email}) {
				t.Errorf("%s: got %v rejected %v, want it rejected", tc.name, got, rejected)
			}
			continue
		}
		if len(rejected) != 0 || len(got) != 1 || got[0].Kind != tc.want.kind || got[0].Email != tc.email {
			t.Errorf("%s: got %+v rejected %v, want kind %s", tc.name, got, rejected, tc.want.kind)
		}
		if tc.want.kind == testKindSubscriber && got[0].Sub.ID == 0 {
			t.Errorf("%s: the subscriber was not kept", tc.name)
		}
	}

	// One bad address among good ones is reported, and the order is kept.
	got, rejected, err := classifyTestRecipients(
		[]string{"anyone@corp.example", "stranger@outside.example", "known@partner.example", "me@outside.example"}, subs, me, domains)
	if err != nil || len(got) != 3 || !reflect.DeepEqual(rejected, []string{"stranger@outside.example"}) ||
		got[0].Kind != testKindInternal || got[1].Kind != testKindSubscriber || got[2].Kind != testKindOwn {
		t.Errorf("mixed: got %+v rejected %v err %v", got, rejected, err)
	}

	// The limit of one request.
	many := []string{}
	for i := 0; i < testMaxRecipients+1; i++ {
		many = append(many, fmt.Sprintf("u%d@corp.example", i))
	}
	if _, _, err := classifyTestRecipients(many, nil, me, domains); err != errTestTooMany {
		t.Errorf("%d recipients: err %v, want errTestTooMany", len(many), err)
	}
	if got, rejected, err := classifyTestRecipients(many[:testMaxRecipients], nil, me, domains); err != nil || len(got) != testMaxRecipients || len(rejected) != 0 {
		t.Errorf("%d recipients: got %d rejected %v err %v", testMaxRecipients, len(got), rejected, err)
	}
}

func TestCleanTestEmails(t *testing.T) {
	got := cleanTestEmails([]string{" A@Corp.Example ", "a@corp.example", "", "  ", "b@corp.example"})
	if want := []string{"a@corp.example", "b@corp.example"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The sample values that are typed in by hand, and the stand-in subscriber built from them.
func TestTestSample(t *testing.T) {
	user := auth.User{Type: auth.UserTypeUser, Name: "로그인 사용자", Email: null.StringFrom("me@corp.example")}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }

	for _, tc := range []struct {
		name   string
		sample *testSample
		kind   string
		bad    bool
	}{
		{"no sample", nil, testSampleDefault, false},
		{"an empty sample", &testSample{}, testSampleDefault, false},
		{"blank name and null attribs", &testSample{Name: "  ", Attribs: raw("null")}, testSampleDefault, false},
		{"a name only", &testSample{Name: "홍길동"}, testSampleCustom, false},
		{"attribs only", &testSample{Attribs: raw(`{"company":"크레바스"}`)}, testSampleCustom, false},
		{"empty attribs: typed in, with nothing", &testSample{Attribs: raw(`{}`)}, testSampleCustom, false},
		{"attribs that are a list", &testSample{Attribs: raw(`["a"]`)}, "", true},
		{"attribs that are a text", &testSample{Attribs: raw(`"a"`)}, "", true},
		{"attribs that are not JSON", &testSample{Attribs: raw(`{company}`)}, "", true},
		{"a name that is too long", &testSample{Name: strings.Repeat("가", testSampleMaxName+1)}, "", true},
		{"a name at the limit", &testSample{Name: strings.Repeat("가", testSampleMaxName)}, testSampleCustom, false},
		{"attribs that are too long", &testSample{Attribs: raw(`{"a":"` + strings.Repeat("x", testSampleMaxAttribs) + `"}`)}, "", true},
	} {
		v, err := readTestSample(tc.sample)
		if (err != nil) != tc.bad || (!tc.bad && v.Kind != tc.kind) {
			t.Errorf("%s: kind %q err %v, want kind %q bad %v", tc.name, v.Kind, err, tc.kind, tc.bad)
		}
	}

	// The default sample: the name is the user's for their own address and the part before
	// the "@" for another, the attributes are those of the preview's demo subscriber.
	def := testSampleValues{Kind: testSampleDefault}
	own := buildTestSample(def, "me@corp.example", true, user)
	other := buildTestSample(def, "design.team@corp.example", false, user)
	if own.Name != "로그인 사용자" || other.Name != "design.team" {
		t.Errorf("default names: own %q other %q", own.Name, other.Name)
	}
	if !reflect.DeepEqual(own.Attribs, dummySubscriber.Attribs) || !reflect.DeepEqual(other.Attribs, dummySubscriber.Attribs) {
		t.Errorf("default attribs: %v %v", own.Attribs, other.Attribs)
	}
	if noName := buildTestSample(def, "me@corp.example", true, auth.User{}); noName.Name != "me" {
		t.Errorf("own address of a user without a name: %q", noName.Name)
	}

	// Typed-in values and a borrowed subscriber: the values are used as given.
	custom, err := readTestSample(&testSample{Name: "홍길동", Attribs: raw(`{"company":"크레바스","n":1234567}`)})
	if err != nil {
		t.Fatal(err)
	}
	borrowed := testSampleValues{Kind: testSampleSubscriber, SubID: 7, Name: "김하늘", Attribs: models.JSON{"company": "하늘상사"}}
	for name, tc := range map[string]struct {
		v           testSampleValues
		wantName    string
		wantCompany string
	}{
		"custom":   {custom, "홍길동", "크레바스"},
		"borrowed": {borrowed, "김하늘", "하늘상사"},
	} {
		s := buildTestSample(tc.v, "anyone@corp.example", false, user)
		if s.Name != tc.wantName || s.Attribs["company"] != tc.wantCompany {
			t.Errorf("%s: name %q attribs %v", name, s.Name, s.Attribs)
		}
		// Whatever the sample, the stand-in is no real subscriber and goes to the recipient.
		if s.ID != 0 || s.UUID != dummyUUID || s.Email != "anyone@corp.example" || s.Status != models.SubscriberStatusEnabled {
			t.Errorf("%s: id %d uuid %s email %s status %s", name, s.ID, s.UUID, s.Email, s.Status)
		}
	}
	for _, s := range []models.Subscriber{own, other} {
		if s.ID != 0 || s.UUID != dummyUUID {
			t.Errorf("default sample: id %d uuid %s", s.ID, s.UUID)
		}
	}
	// A typed-in sample is used exactly as typed: without a name the name is empty, for the
	// user's own address too, so that a message with empty values can be tested.
	onlyAttribs, _ := readTestSample(&testSample{Attribs: raw(`{"company":"크레바스"}`)})
	for _, own := range []bool{false, true} {
		if s := buildTestSample(onlyAttribs, "design@corp.example", own, user); s.Name != "" || s.Attribs["company"] != "크레바스" || s.Attribs["city"] != nil {
			t.Errorf("attribs only (own %v): name %q attribs %v", own, s.Name, s.Attribs)
		}
	}
	nothing, _ := readTestSample(&testSample{Attribs: raw(`{}`)})
	if s := buildTestSample(nothing, "design@corp.example", false, user); s.Name != "" || len(s.Attribs) != 0 || s.UUID != dummyUUID {
		t.Errorf("empty typed-in sample: name %q attribs %v", s.Name, s.Attribs)
	}
	if s := buildTestSample(testSampleValues{Kind: testSampleSubscriber, SubID: 7}, "design@corp.example", false, user); s.Name != "" || s.Attribs == nil {
		t.Errorf("borrowed subscriber without a name: name %q attribs %v", s.Name, s.Attribs)
	}
	// A name without attribs has no attributes at all, not the demo ones.
	onlyName, _ := readTestSample(&testSample{Name: "홍길동"})
	if s := buildTestSample(onlyName, "design@corp.example", false, user); s.Attribs == nil || len(s.Attribs) != 0 {
		t.Errorf("name only: attribs %v", s.Attribs)
	}
}

func TestTestSendLimiter(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	l := &testSendLimiter{now: func() time.Time { return now }}

	// Up to the limit goes through, in requests of several messages.
	for i := 0; i < testRateLimit/10; i++ {
		if ok, _ := l.take(1, 10); !ok {
			t.Fatalf("request %d was refused below the limit", i)
		}
		now = now.Add(time.Minute)
	}
	// The next message does not, and nothing is reserved by a refused request.
	ok, wait := l.take(1, 1)
	if ok {
		t.Fatalf("message %d went through", testRateLimit+1)
	}
	// The first ten were sent at 9:00 and leave the window at 9:10; it is 9:03 now.
	if want := testRateWindow - 3*time.Minute; wait != want {
		t.Errorf("wait %s, want %s", wait, want)
	}
	if ok, _ := l.take(1, 1); ok {
		t.Fatal("a refused request made room")
	}
	// Another user is counted apart.
	if ok, _ := l.take(2, 10); !ok {
		t.Fatal("another user was refused")
	}
	// A request that needs more room than the oldest messages free waits for the later ones.
	if ok, wait := l.take(1, 10); ok || wait != testRateWindow-3*time.Minute {
		t.Errorf("ten more: ok %v wait %s", ok, wait)
	}
	if ok, wait := l.take(1, 11); ok || wait != testRateWindow-2*time.Minute {
		t.Errorf("eleven more: ok %v wait %s", ok, wait)
	}
	// Just before the first messages leave the window: still refused. At that moment: allowed.
	now = time.Date(2026, 11, 19, 9, 9, 59, 0, time.UTC)
	if ok, _ := l.take(1, 1); ok {
		t.Fatal("allowed before the window moved")
	}
	now = time.Date(2026, 11, 19, 9, 10, 0, 0, time.UTC)
	if ok, _ := l.take(1, 10); !ok {
		t.Fatal("refused after the window moved")
	}
	if ok, _ := l.take(1, 1); ok {
		t.Fatal("the window let through more than the limit")
	}
	// Messages that were reserved but not sent are given back, the newest first.
	l.giveBack(1, 4)
	if ok, _ := l.take(1, 5); ok {
		t.Fatal("more was given back than asked")
	}
	if ok, _ := l.take(1, 4); !ok {
		t.Fatal("the messages given back were not free")
	}
	l.giveBack(1, 0)
	l.giveBack(1, 1000)
	l.giveBack(99, 1)
	if ok, _ := l.take(1, 1); ok {
		t.Fatal("a wrong give-back made room")
	}
	// Long after, everything is allowed again.
	now = now.Add(time.Hour)
	if ok, _ := l.take(1, testMaxRecipients); !ok {
		t.Fatal("refused after an hour")
	}

	// The zero value works with the real clock.
	var z testSendLimiter
	if ok, _ := z.take(1, 1); !ok {
		t.Fatal("zero limiter refused the first message")
	}
}

// The test domains follow the rule of the sender domains.
func TestNormalizeTestDomains(t *testing.T) {
	set := models.Settings{AxmTestDomains: []string{" Corp.Example ", "corp.example", "other.example"}}
	if err := normalizeSenderSettings(&set); err != nil {
		t.Fatal(err)
	}
	if want := []string{"corp.example", "other.example"}; !reflect.DeepEqual(set.AxmTestDomains, want) {
		t.Errorf("got %v, want %v", set.AxmTestDomains, want)
	}
	// Left out of a request, the setting is saved as an empty list and not as null.
	empty := models.Settings{}
	if err := normalizeSenderSettings(&empty); err != nil || empty.AxmTestDomains == nil || len(empty.AxmTestDomains) != 0 {
		t.Errorf("empty: %v %v", empty.AxmTestDomains, err)
	}
	if b, _ := json.Marshal(empty.AxmTestDomains); string(b) != "[]" {
		t.Errorf("empty marshals as %s", b)
	}
	for _, bad := range []string{"", "corp", "a@corp.example", "https://corp.example", "corp .example", "-corp.example", "corp..example", "코프.example"} {
		set := models.Settings{AxmTestDomains: []string{bad}}
		if err := normalizeSenderSettings(&set); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A bad test domain does not depend on the sender domains, and the other way round.
	set = models.Settings{AxmSenderDomains: []string{"corp.example"}, AxmTestDomains: []string{"bad"}}
	if err := normalizeSenderSettings(&set); err == nil {
		t.Error("a bad test domain was accepted next to a good sender domain")
	}
}
