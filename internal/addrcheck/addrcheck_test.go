package addrcheck

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDNS answers from a table. A domain that is not in the table does not exist.
type fakeDNS struct {
	mu    sync.Mutex
	mx    map[string][]*net.MX
	mxErr map[string]error
	ip    map[string]bool
	ipErr map[string]error
	ip6Err map[string]error // only the AAAA lookup fails
	ipErr4 map[string]error // only the A lookup fails
	ip6    map[string]bool  // has an AAAA record
	hold  chan struct{} // when set, every MX lookup waits for it
	mxN   map[string]int
	times []time.Time // when each MX lookup started
	ipN   map[string]int
}

var (
	errNX      = &net.DNSError{Err: "no such host", IsNotFound: true}
	errTimeout = &net.DNSError{Err: "i/o timeout", IsTimeout: true}
	errServer  = &net.DNSError{Err: "server misbehaving", IsTemporary: true}
)

func newFake() *fakeDNS {
	return &fakeDNS{
		mx: map[string][]*net.MX{}, mxErr: map[string]error{}, ip: map[string]bool{}, ipErr: map[string]error{}, ip6Err: map[string]error{}, ipErr4: map[string]error{}, ip6: map[string]bool{},
		mxN: map[string]int{}, ipN: map[string]int{},
	}
}

func (f *fakeDNS) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	f.mu.Lock()
	f.mxN[name]++
	f.times = append(f.times, time.Now())
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mxErr[name]; err != nil {
		return nil, err
	}
	if mx := f.mx[name]; len(mx) > 0 {
		return mx, nil
	}
	return nil, errNX
}

func (f *fakeDNS) LookupIP(ctx context.Context, network, name string) ([]net.IP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ipN[name]++
	if err := f.ipErr[name]; err != nil {
		return nil, err
	}
	if network == "ip6" {
		if err := f.ip6Err[name]; err != nil {
			return nil, err
		}
		if f.ip6[name] {
			return []net.IP{net.ParseIP("2001:db8::1")}, nil
		}
		return nil, errNX
	}
	if err := f.ipErr4[name]; err != nil {
		return nil, err
	}
	if f.ip[name] {
		return []net.IP{net.IPv4(192, 0, 2, 1)}, nil
	}
	return nil, errNX
}

func (f *fakeDNS) counts(name string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mxN[name], f.ipN[name]
}

func mx(hosts ...string) []*net.MX {
	out := make([]*net.MX, len(hosts))
	for i, h := range hosts {
		out[i] = &net.MX{Host: h, Pref: uint16(i * 10)}
	}
	return out
}

func TestClassify(t *testing.T) {
	for _, row := range []struct {
		in         string
		email      string
		domain     string
		format     bool
		role       bool
		disposable bool
	}{
		{" Kim@Example.COM ", "kim@example.com", "example.com", false, false, false},
		{"Info+x@Example.com", "info+x@example.com", "example.com", false, true, false},
		{"NO-REPLY@example.com", "no-reply@example.com", "example.com", false, true, false},
		{"information@example.com", "information@example.com", "example.com", false, false, false},
		{"kim+info@example.com", "kim+info@example.com", "example.com", false, false, false},
		{"a@mailinator.com", "a@mailinator.com", "mailinator.com", false, false, true},
		{"a@Sub.Mailinator.com", "a@sub.mailinator.com", "sub.mailinator.com", false, false, true},
		{"support@mailinator.com", "support@mailinator.com", "mailinator.com", false, true, true},
		{"a@mailinator.com.example.org", "a@mailinator.com.example.org", "mailinator.com.example.org", false, false, false},
		{"a@googlemail.example", "a@googlemail.example", "googlemail.example", false, false, false},
		{"a@한글.kr", "a@한글.kr", "xn--bj0bj06e.kr", false, false, false},
		{"kim", "kim", "", true, false, false},
		{"kim@", "kim@", "", true, false, false},
		{"Kim <kim@example.com>", "Kim <kim@example.com>", "", true, false, false},
		{"a b@example.com", "a b@example.com", "", true, false, false},
		{"info@", "info@", "", true, false, false},
		{"", "", "", true, false, false},
	} {
		got := Classify(row.in)
		want := Result{Email: row.email, Domain: row.domain, Format: row.format, Role: row.role, Disposable: row.disposable}
		if got != want {
			t.Errorf("Classify(%q) = %+v, want %+v", row.in, got, want)
		}
	}
}

func TestDisposableListIsLoadedAndSpareOfCommonProviders(t *testing.T) {
	if len(disposableDomains) < 5000 {
		t.Fatalf("only %d disposable domains loaded", len(disposableDomains))
	}
	for d := range disposableDomains {
		if strings.ContainsAny(d, " #@") || !strings.Contains(d, ".") {
			t.Fatalf("bad list entry %q", d)
		}
	}
	for _, d := range []string{"gmail.com", "naver.com", "daum.net", "hanmail.net", "kakao.com", "nate.com",
		"outlook.com", "hotmail.com", "yahoo.com", "icloud.com", "proton.me"} {
		if isDisposable(d) {
			t.Errorf("%s is treated as disposable", d)
		}
	}
}

func TestResolve(t *testing.T) {
	f := newFake()
	f.mx["ok.test."] = mx("mx1.ok.test.", "mx2.ok.test.")
	f.mx["null.test."] = mx(".")
	f.mx["mixed.test."] = mx(".", "mx.mixed.test.")
	f.ip["aonly.test."] = true
	f.mxErr["slow.test."] = errTimeout
	f.mxErr["broken.test."] = errServer
	f.mxErr["plain.test."] = errors.New("connection refused")
	f.ipErr["ipslow.test."] = errTimeout
	// No MX, no A, and the AAAA lookup failed: that is a hiccup, not a dead domain, whichever
	// family fails (the Go resolver would call it "not found").
	f.ip6Err["half.test."] = errServer
	f.ipErr4["half4.test."] = errServer
	f.ip["v6only.test."] = false
	f.ip6["v6only.test."] = true
	f.ip6Err["a-ok.test."] = errServer
	f.ip["a-ok.test."] = true
	c := NewChecker(Options{Resolver: f})

	got := c.CheckDomains(context.Background(), []string{
		"ok.test", "null.test", "mixed.test", "aonly.test", "gone.test",
		"slow.test", "broken.test", "plain.test", "ipslow.test", "ok.test",
		"half.test", "half4.test", "v6only.test", "a-ok.test",
	}, time.Second)
	want := map[string]DomainStatus{
		"ok.test": StatusOK, "null.test": StatusNullMX, "mixed.test": StatusOK,
		"aonly.test": StatusOK, "gone.test": StatusDead,
		"slow.test": StatusUnknown, "broken.test": StatusUnknown, "plain.test": StatusUnknown,
		"ipslow.test": StatusUnknown,
		"half.test": StatusUnknown, "half4.test": StatusUnknown, "v6only.test": StatusOK, "a-ok.test": StatusOK,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses\n got %v\nwant %v", got, want)
	}
	// A domain with MX is not asked for its address; one without is.
	if m, i := f.counts("ok.test."); m != 1 || i != 0 {
		t.Errorf("ok.test looked up %d MX, %d IP", m, i)
	}
	if m, i := f.counts("gone.test."); m != 1 || i != 2 { // both families are asked
		t.Errorf("gone.test looked up %d MX, %d IP", m, i)
	}
	// A failed MX lookup is not turned into a verdict by an address lookup.
	if _, i := f.counts("slow.test."); i != 0 {
		t.Errorf("slow.test address looked up %d times", i)
	}
}

func TestCacheTTLAndUnknownCachedBriefly(t *testing.T) {
	f := newFake()
	f.mx["ok.test."] = mx("mx.ok.test.")
	f.mxErr["slow.test."] = errTimeout
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	c := NewChecker(Options{Resolver: f, Now: clock})
	check := func() map[string]DomainStatus {
		return c.CheckDomains(context.Background(), []string{"ok.test", "gone.test", "slow.test"}, time.Second)
	}
	lookups := func(wantOK, wantGone, wantSlow int) {
		t.Helper()
		ok, _ := f.counts("ok.test.")
		gone, _ := f.counts("gone.test.")
		slow, _ := f.counts("slow.test.")
		if ok != wantOK || gone != wantGone || slow != wantSlow {
			t.Fatalf("lookups ok=%d gone=%d slow=%d, want %d %d %d", ok, gone, slow, wantOK, wantGone, wantSlow)
		}
	}

	check()
	lookups(1, 1, 1)
	check() // all three come from the cache, the unknown one for UnknownTTL only
	lookups(1, 1, 1)
	advance(59 * time.Second)
	check()
	lookups(1, 1, 1)
	advance(2 * time.Second) // past the 60 seconds of an unknown answer
	check()
	lookups(1, 1, 2)

	advance(25 * time.Hour) // past the 1 day of a dead domain, within the 7 days of a working one
	f.mu.Lock()
	f.mx["gone.test."] = mx("mx.gone.test.")
	f.mu.Unlock()
	if got := check(); got["gone.test"] != StatusOK || got["ok.test"] != StatusOK {
		t.Fatalf("after a day: %v", got)
	}
	lookups(1, 2, 3)

	advance(7 * 24 * time.Hour)
	check()
	lookups(2, 3, 4)
}

func TestCacheSizeLimit(t *testing.T) {
	f := newFake()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	c := NewChecker(Options{Resolver: f, MaxEntries: 3, Concurrency: 1, Now: func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(time.Second)
		return now
	}})
	for _, d := range []string{"a.test", "b.test", "c.test", "d.test", "e.test"} {
		c.CheckDomains(context.Background(), []string{d}, time.Second)
	}
	c.mu.Lock()
	_, hasA := c.cache["a.test"]
	_, hasE := c.cache["e.test"]
	n := len(c.cache)
	c.mu.Unlock()
	if n != 3 || hasA || !hasE {
		t.Fatalf("cache holds %d entries, a=%v e=%v", n, hasA, hasE)
	}
}

func TestBudgetLeavesPendingAndFinishesInBackground(t *testing.T) {
	f := newFake()
	f.mx["ok.test."] = mx("mx.ok.test.")
	f.hold = make(chan struct{})
	c := NewChecker(Options{Resolver: f})

	start := time.Now()
	got := c.CheckDomains(context.Background(), []string{"ok.test", "gone.test"}, 30*time.Millisecond)
	if got["ok.test"] != StatusPending || got["gone.test"] != StatusPending {
		t.Fatalf("within budget: %v", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the call waited %v", time.Since(start))
	}

	close(f.hold) // the lookups go on after the caller left
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = c.CheckDomains(context.Background(), []string{"ok.test", "gone.test"}, 30*time.Millisecond)
		if got["ok.test"] == StatusOK && got["gone.test"] == StatusDead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never finished: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m, _ := f.counts("ok.test."); m != 1 {
		t.Fatalf("ok.test looked up %d times", m)
	}
}

func TestCancelledContextReturnsPending(t *testing.T) {
	f := newFake()
	f.hold = make(chan struct{})
	defer close(f.hold)
	c := NewChecker(Options{Resolver: f})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := c.CheckDomains(ctx, []string{"a.test"}, time.Minute)
	if got["a.test"] != StatusPending {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrentCallersShareOneLookup(t *testing.T) {
	f := newFake()
	f.mx["ok.test."] = mx("mx.ok.test.")
	f.hold = make(chan struct{})
	c := NewChecker(Options{Resolver: f})

	var wg sync.WaitGroup
	results := make([]DomainStatus, 20)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.CheckDomains(context.Background(), []string{"ok.test"}, 5*time.Second)["ok.test"]
		}()
	}
	// Let every caller join before the lookup answers.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m, _ := f.counts("ok.test."); m == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(f.hold)
	wg.Wait()
	for i, st := range results {
		if st != StatusOK {
			t.Fatalf("caller %d got %s", i, st)
		}
	}
	if m, _ := f.counts("ok.test."); m != 1 {
		t.Fatalf("looked up %d times, want 1", m)
	}
}

func TestConcurrencyAndQueueLimits(t *testing.T) {
	f := newFake()
	f.hold = make(chan struct{})
	c := NewChecker(Options{Resolver: f, Concurrency: 2, MaxQueued: 3})
	domains := []string{"a.test", "b.test", "c.test", "d.test", "e.test"}
	got := c.CheckDomains(context.Background(), domains, 50*time.Millisecond)
	for _, d := range domains {
		if got[d] != StatusPending {
			t.Fatalf("%s = %s", d, got[d])
		}
	}
	f.mu.Lock()
	started := len(f.mxN)
	f.mu.Unlock()
	c.mu.Lock()
	queued := len(c.inflight)
	c.mu.Unlock()
	if started != 2 || queued != 3 {
		t.Fatalf("%d lookups started (want 2), %d queued (want 3)", started, queued)
	}
	close(f.hold)
	// The domains over the queue limit are picked up by a later call.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = c.CheckDomains(context.Background(), domains, 50*time.Millisecond)
		done := 0
		for _, st := range got {
			if st == StatusDead {
				done++
			}
		}
		if done == len(domains) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never finished: %v", got)
		}
	}
}

func TestCheckAndSummarize(t *testing.T) {
	f := newFake()
	f.mx["ok.test."] = mx("mx.ok.test.")
	f.mx["mailinator.com."] = mx("mx.mailinator.com.")
	f.mx["null.test."] = mx(".")
	f.mxErr["slow.test."] = errTimeout
	c := NewChecker(Options{Resolver: f})
	emails := []string{
		"kim@ok.test",         // 1 fine
		"not-an-address",      // 2 format
		"lee@gone.test",       // 3 dead
		"info@gone.test",      // 4 dead + role: counted once, as confirmed
		"park@null.test",      // 5 null MX
		"temp@mailinator.com", // 6 disposable
		"Sales@ok.test",       // 7 role
		"choi@slow.test",      // 8 unknown
		"help@slow.test",      // 9 unknown + role
	}
	ids := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	verdicts := c.Check(context.Background(), emails, time.Second)

	wantClasses := [][]string{
		nil, {ClassFormat}, {ClassDead}, {ClassDead, ClassRole}, {ClassNullMX},
		{ClassDisposable}, {ClassRole}, {ClassUnknown}, {ClassRole, ClassUnknown},
	}
	for i, v := range verdicts {
		if !reflect.DeepEqual(v.Classes(), wantClasses[i]) {
			t.Errorf("%s: classes %v, want %v", emails[i], v.Classes(), wantClasses[i])
		}
	}

	s := Summarize(verdicts, ids, []bool{true, true, true, false, true, true, true, true, true}, true)
	if s.Total != 9 || s.Confirmed != 4 || s.Warning != 3 || s.Risky != 7 || s.Unknown != 2 || s.Pending != 0 {
		t.Fatalf("summary %+v", s)
	}
	counts := map[string]int{}
	for name, cs := range s.Classes {
		counts[name] = cs.Count
	}
	want := map[string]int{ClassFormat: 1, ClassProvider: 0, ClassTypo: 0, ClassDead: 2, ClassNullMX: 1, ClassDisposable: 1, ClassRole: 3, ClassUnknown: 2}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("class counts %v, want %v", counts, want)
	}
	role := s.Classes[ClassRole]
	if !reflect.DeepEqual(role.IDs, []int{4, 7, 9}) ||
		!reflect.DeepEqual(role.Emails, []string{"info@gone.test", "sales@ok.test", "help@slow.test"}) ||
		!reflect.DeepEqual(role.Samples, role.Emails) || role.Truncated ||
		!reflect.DeepEqual(role.Direct, []bool{false, true, true}) ||
		!reflect.DeepEqual(role.Tags, [][]string{{ClassDead, ClassRole}, {ClassRole}, {ClassRole, ClassUnknown}}) {
		t.Fatalf("role detail %+v", role)
	}

	// Without detail there are only counts.
	plain := Summarize(verdicts, ids, nil, false)
	for name, cs := range plain.Classes {
		if cs.Count != want[name] || cs.Samples != nil || cs.IDs != nil || cs.Emails != nil || cs.Tags != nil || cs.Direct != nil {
			t.Fatalf("%s without detail: %+v", name, cs)
		}
	}
}

func TestSummarizeLimitsDetailAndCountsPending(t *testing.T) {
	n := MaxDetail + 7
	verdicts := make([]Verdict, n)
	ids := make([]int, n)
	for i := range verdicts {
		verdicts[i] = Verdict{Result: Result{Email: "info@x.test", Domain: "x.test", Role: true}, Status: StatusPending}
		ids[i] = i + 1
	}
	// The last address is also on a dead domain: its tags say so although the role list is cut.
	verdicts[n-1].Status = StatusDead
	s := Summarize(verdicts, ids, nil, true)
	if dead := s.Classes[ClassDead]; dead.Count != 1 || !reflect.DeepEqual(dead.Tags, [][]string{{ClassDead, ClassRole}}) || dead.Direct != nil {
		t.Fatalf("dead detail %+v", dead)
	}
	role := s.Classes[ClassRole]
	if role.Count != n || len(role.IDs) != MaxDetail || len(role.Emails) != MaxDetail ||
		len(role.Samples) != MaxSamples || len(role.Tags) != MaxDetail || !role.Truncated {
		t.Fatalf("count %d ids %d emails %d samples %d truncated %v",
			role.Count, len(role.IDs), len(role.Emails), len(role.Samples), role.Truncated)
	}
	if s.Pending != n-1 || s.Warning != n-1 || s.Confirmed != 1 || s.Risky != n || s.Unknown != 0 || s.Classes[ClassUnknown].Count != 0 {
		t.Fatalf("summary %+v", s)
	}
}

// An address literal such as [192.0.2.17] is not a name: it is never looked up and is
// reported as unknown, not as a dead domain (RFC 5321 §4.1.3).
func TestAddressLiteralIsUnknownAndNotLookedUp(t *testing.T) {
	f := newFake()
	c := NewChecker(Options{Resolver: f})
	emails := []string{"qa@[192.0.2.17]", "Info@[192.0.2.18]", "ops@[IPv6:2001:db8::1]"}
	verdicts := c.Check(context.Background(), emails, time.Second)
	want := [][]string{{ClassUnknown}, {ClassRole, ClassUnknown}}
	for i, v := range verdicts {
		if v.Format {
			// The address rules of listmonk may refuse a literal; then it is malformed.
			continue
		}
		if !v.Literal || v.Confirmed() {
			t.Fatalf("%s: %+v", emails[i], v)
		}
		if i < len(want) && !reflect.DeepEqual(v.Classes(), want[i]) {
			t.Fatalf("%s: classes %v, want %v", emails[i], v.Classes(), want[i])
		}
	}
	if verdicts[0].Format {
		t.Fatalf("an IPv4 literal is malformed: %+v", verdicts[0])
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mxN) != 0 || len(f.ipN) != 0 {
		t.Fatalf("looked up %v %v", f.mxN, f.ipN)
	}
}

// Lookups start at most MaxPerSecond times a second however many callers ask, and the
// concurrency limit still holds.
func TestLookupsAreRateLimited(t *testing.T) {
	f := newFake()
	domains := make([]string, 60)
	for i := range domains {
		domains[i] = "d" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".test"
		f.mx[domains[i]+"."] = mx("mx.test.")
	}
	c := NewChecker(Options{Resolver: f, MaxPerSecond: 20, Concurrency: 8})
	start := time.Now()
	got := c.CheckDomains(context.Background(), domains, 10*time.Second)
	took := time.Since(start)
	for _, d := range domains {
		if got[d] != StatusOK {
			t.Fatalf("%s: %s", d, got[d])
		}
	}
	// 60 lookups at 100 a second (a millisecond more than the even gap): the last one starts
	// about 650ms after the first.
	if took < 600*time.Millisecond {
		t.Fatalf("60 lookups took %v, want at least 550ms at 100 a second", took)
	}
	if took > 5*time.Second {
		t.Fatalf("60 lookups took %v", took)
	}
}

// A domain that does not answer is not asked again by the next batch within UnknownTTL.
func TestUnknownIsNotAskedAgainWithinTTL(t *testing.T) {
	f := newFake()
	f.mxErr["silent.test."] = errTimeout
	c := NewChecker(Options{Resolver: f})
	for i := 0; i < 3; i++ {
		if got := c.CheckDomains(context.Background(), []string{"silent.test"}, time.Second); got["silent.test"] != StatusUnknown {
			t.Fatalf("round %d: %v", i, got)
		}
	}
	if mxN, _ := f.counts("silent.test."); mxN != 1 {
		t.Fatalf("silent.test looked up %d times", mxN)
	}
}
