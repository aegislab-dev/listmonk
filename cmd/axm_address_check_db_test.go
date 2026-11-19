package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/auth"
)

// addrFakeDNS answers the address check from a table, so the tests never touch real
// DNS. A domain that is not in the table does not exist.
type addrFakeDNS struct {
	mu    sync.Mutex
	hold  chan struct{} // when set, MX lookups wait for it
	calls int
}

func (f *addrFakeDNS) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	f.mu.Lock()
	f.calls++
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	switch name {
	case "ok.test.", "mailinator.com.":
		return []*net.MX{{Host: "mx." + name, Pref: 10}}, nil
	case "null.test.":
		return []*net.MX{{Host: ".", Pref: 0}}, nil
	case "slow.test.":
		return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f *addrFakeDNS) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	if name == "aonly.test." {
		return []net.IPAddr{{IP: net.IPv4(192, 0, 2, 1)}}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

type addrEnv struct {
	*teamsEnv
	dns *addrFakeDNS
}

func newAddrEnv(t *testing.T) *addrEnv {
	e := &addrEnv{teamsEnv: newTeamsEnv(t), dns: &addrFakeDNS{}}
	e.a.addrCheck = addrcheck.NewChecker(addrcheck.Options{Resolver: e.dns})
	return e
}

// sub adds a subscriber with the given address and puts it on a list.
func (e *addrEnv) sub(email, status string, list int, subscription string) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO subscribers (uuid, email, name, status)
		VALUES (gen_random_uuid(), $1, 'Synthetic', $2::subscriber_status) RETURNING id`, email, status)
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, $3::subscription_status)`,
		id, list, subscription)
	return id
}

type addrListAnswer struct {
	addrcheck.Summary
	TooLarge bool `json:"too_large"`
	Limit    int  `json:"limit"`
	Detail   bool `json:"detail"`
}

func (e *addrEnv) check(user auth.User, lists ...int) (addrListAnswer, string) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"list_ids": lists})
	code, data := e.call(e.a.CheckListAddresses, user, "POST", "/api/lists/address-check", "", string(body))
	if code != http.StatusOK {
		e.t.Fatalf("list check HTTP %d %s", code, data)
	}
	var out addrListAnswer
	if err := json.Unmarshal(data, &out); err != nil {
		e.t.Fatal(err)
	}
	return out, string(data)
}

func addrCounts(s addrcheck.Summary) map[string]int {
	out := map[string]int{}
	for name, cs := range s.Classes {
		if cs.Count > 0 {
			out[name] = cs.Count
		}
	}
	return out
}

func TestAddressCheckListAudienceAndClasses(t *testing.T) {
	e := newAddrEnv(t)
	member := e.as(e.ua)

	e.sub("kim@ok.test", "enabled", e.aList, "confirmed")
	e.sub("lee@aonly.test", "enabled", e.aList, "unconfirmed")
	malformed := e.sub("not-an-address", "enabled", e.aList, "confirmed")
	dead := e.sub("park@gone.test", "enabled", e.aList, "confirmed")
	deadRole := e.sub("info@gone.test", "enabled", e.aList, "confirmed")
	null := e.sub("choi@null.test", "enabled", e.aList, "confirmed")
	temp := e.sub("temp@mailinator.com", "enabled", e.aList, "confirmed")
	role := e.sub("Booking+seoul@ok.test", "enabled", e.aList, "confirmed")
	slow := e.sub("jung@slow.test", "enabled", e.aList, "confirmed")
	// Not in the audience: blocklisted, unsubscribed, and a list of another team.
	e.sub("blocked@gone.test", "blocklisted", e.aList, "confirmed")
	e.sub("left@gone.test", "enabled", e.aList, "unsubscribed")
	e.sub("other@gone.test", "enabled", e.bList, "confirmed")

	got, raw := e.check(member, e.aList, e.bList, e.aList)
	if got.Total != 9 || got.Confirmed != 4 || got.Warning != 2 || got.Risky != 6 || got.Unknown != 1 ||
		got.Pending != 0 || got.TooLarge || !got.Detail || got.Limit != 500000 {
		t.Fatalf("summary %s", raw)
	}
	want := map[string]int{"format": 1, "dead": 2, "nullmx": 1, "disposable": 1, "role": 2, "unknown": 1}
	if !reflect.DeepEqual(addrCounts(got.Summary), want) {
		t.Fatalf("classes %v, want %v", addrCounts(got.Summary), want)
	}
	for name, ids := range map[string][]int{
		"format": {malformed}, "dead": {dead, deadRole}, "nullmx": {null},
		"disposable": {temp}, "role": {deadRole, role}, "unknown": {slow},
	} {
		cs := got.Classes[name]
		if !reflect.DeepEqual(cs.IDs, ids) || len(cs.Emails) != len(ids) || len(cs.Samples) != len(ids) || cs.Truncated {
			t.Fatalf("%s detail %+v, want ids %v", name, cs, ids)
		}
	}
	if got.Classes["role"].Emails[1] != "booking+seoul@ok.test" {
		t.Fatalf("role addresses %v", got.Classes["role"].Emails)
	}

	// The other team's list is not counted for this user, and is for its own member.
	if other, raw := e.check(e.as(e.ub), e.aList, e.bList); other.Total != 1 || other.Confirmed != 1 {
		t.Fatalf("other team: %s", raw)
	}
	if all, raw := e.check(e.as(e.admin), e.aList, e.bList); all.Total != 10 || all.Confirmed != 5 {
		t.Fatalf("operator: %s", raw)
	}
	if none, raw := e.check(member, e.bList); none.Total != 0 || none.Risky != 0 || len(none.Classes) != 6 {
		t.Fatalf("no access: %s", raw)
	}
	if none, raw := e.check(member); none.Total != 0 || none.Risky != 0 {
		t.Fatalf("no lists: %s", raw)
	}

	// Without a subscriber permission there are counts but no addresses or IDs.
	noSubs := e.as(e.ua)
	delete(noSubs.PermissionsMap, auth.PermSubscribersGet)
	delete(noSubs.PermissionsMap, auth.PermSubscribersGetAll)
	hidden, raw := e.check(noSubs, e.aList)
	if hidden.Detail || hidden.Total != 9 || hidden.Risky != 6 || !reflect.DeepEqual(addrCounts(hidden.Summary), want) {
		t.Fatalf("hidden summary %s", raw)
	}
	for _, leak := range []string{"@", "samples", "emails", `"ids"`} {
		if strings.Contains(raw, leak) {
			t.Fatalf("answer without subscriber permission contains %q: %s", leak, raw)
		}
	}

	// Unsubscribing the confirmed-risk addresses through the ordinary API lowers the count.
	body, _ := json.Marshal(map[string]any{
		"ids": append(append([]int{}, got.Classes["dead"].IDs...), null, malformed), "action": "unsubscribe",
		"target_list_ids": []int{e.aList},
	})
	if code, data := e.call(e.a.ManageSubscriberLists, member, "PUT", "/api/subscribers/lists", "", string(body)); code != http.StatusOK {
		t.Fatalf("unsubscribe HTTP %d %s", code, data)
	}
	after, raw := e.check(member, e.aList)
	if after.Total != 5 || after.Confirmed != 0 || after.Warning != 2 || after.Unknown != 1 {
		t.Fatalf("after unsubscribing: %s", raw)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_audit_log WHERE action = $1`, auditSubListsUnsubscribe)
	if n != 1 {
		t.Fatalf("%d audit rows for the unsubscribe, want 1", n)
	}

	for _, body := range []string{`{"list_ids":[0]}`, `{"list_ids":[-1]}`, `{"list_ids":[2147483648]}`, `{"list_ids":"x"}`} {
		if code, _ := e.call(e.a.CheckListAddresses, member, "POST", "/x", "", body); code != http.StatusBadRequest {
			t.Fatalf("%s: HTTP %d", body, code)
		}
	}
}

func TestAddressCheckDoubleOptinAndDynamicLists(t *testing.T) {
	e := newAddrEnv(t)
	admin := e.as(e.admin)
	var doubleID, baseID, dynamicID int
	e.get(&doubleID, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Double','private','double') RETURNING id`)
	e.get(&baseID, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Dynamic base','private','single') RETURNING id`)
	filter := fmt.Sprintf(`{"version":2,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[]}`, baseID)
	e.get(&dynamicID, `INSERT INTO lists(uuid,name,type,optin,axm_filter)
		VALUES (gen_random_uuid(),'Dynamic','private','single',$1::jsonb) RETURNING id`, filter)

	confirmed := e.sub("a@gone.test", "enabled", doubleID, "confirmed")
	e.sub("b@gone.test", "enabled", doubleID, "unconfirmed") // not confirmed: not sent to
	e.sub("c@gone.test", "blocklisted", doubleID, "confirmed")
	inBase := e.sub("info@ok.test", "enabled", baseID, "unconfirmed")
	e.sub("d@gone.test", "enabled", baseID, "unsubscribed")
	// On both lists: counted once.
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status) VALUES ($1, $2, 'confirmed')`, confirmed, baseID)

	if got, raw := e.check(admin, doubleID); got.Total != 1 || got.Confirmed != 1 ||
		!reflect.DeepEqual(got.Classes["dead"].IDs, []int{confirmed}) {
		t.Fatalf("double opt-in: %s", raw)
	}
	if got, raw := e.check(admin, dynamicID); got.Total != 2 || got.Confirmed != 1 || got.Warning != 1 ||
		!reflect.DeepEqual(got.Classes["role"].IDs, []int{inBase}) {
		t.Fatalf("dynamic: %s", raw)
	}
	if got, raw := e.check(admin, doubleID, dynamicID, baseID); got.Total != 2 || got.Risky != 2 {
		t.Fatalf("mixed: %s", raw)
	}
}

func TestAddressCheckTooLargeAndPending(t *testing.T) {
	e := newAddrEnv(t)
	admin := e.as(e.admin)
	for i := 0; i < 3; i++ {
		e.sub(fmt.Sprintf("user%d@gone.test", i), "enabled", e.aList, "confirmed")
	}

	defer func(n int, d time.Duration) { addrCheckMaxAudience, addrCheckBudget = n, d }(addrCheckMaxAudience, addrCheckBudget)
	addrCheckMaxAudience = 2
	got, raw := e.check(admin, e.aList)
	if !got.TooLarge || got.Total != 3 || got.Limit != 2 || got.Risky != 0 || len(addrCounts(got.Summary)) != 0 || e.dns.calls != 0 {
		t.Fatalf("too large: %s (%d lookups)", raw, e.dns.calls)
	}

	// A lookup that is not ready within the budget is pending, and a later call has it.
	addrCheckMaxAudience, addrCheckBudget = 3, 50*time.Millisecond
	e.dns.mu.Lock()
	e.dns.hold = make(chan struct{})
	e.dns.mu.Unlock()
	got, raw = e.check(admin, e.aList)
	if got.TooLarge || got.Total != 3 || got.Pending != 3 || got.Risky != 0 || got.Unknown != 0 {
		t.Fatalf("pending: %s", raw)
	}
	close(e.dns.hold)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, raw = e.check(admin, e.aList)
		if got.Pending == 0 && got.Confirmed == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never finished: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.dns.mu.Lock()
	calls := e.dns.calls
	e.dns.mu.Unlock()
	if calls != 1 {
		t.Fatalf("gone.test looked up %d times, want 1", calls)
	}
}

func TestAddressCheckImport(t *testing.T) {
	e := newAddrEnv(t)
	importer := e.as(e.superAdmin)
	h := e.a.auth.Perm(e.a.CheckImportAddresses, auth.PermSubscribersImport)
	const route = "/api/subscribers/address-check"

	var before int
	e.get(&before, `SELECT COUNT(*) FROM subscribers`)
	body := `{"emails":["kim@ok.test","nope","Info@OK.test","lee@gone.test","temp@mailinator.com","x@null.test","y@slow.test","sales@gone.test"]}`
	code, data := e.call(h, importer, "POST", route, "", body)
	var got struct {
		addrcheck.Summary
		Issues map[int][]string `json:"issues"`
	}
	if err := json.Unmarshal(data, &got); err != nil || code != http.StatusOK {
		t.Fatalf("import check HTTP %d %s: %v", code, data, err)
	}
	wantIssues := map[int][]string{
		1: {"format"}, 2: {"role"}, 3: {"dead"}, 4: {"disposable"}, 5: {"nullmx"}, 6: {"unknown"}, 7: {"dead", "role"},
	}
	if !reflect.DeepEqual(got.Issues, wantIssues) {
		t.Fatalf("issues %v, want %v", got.Issues, wantIssues)
	}
	if got.Total != 8 || got.Confirmed != 4 || got.Warning != 2 || got.Risky != 6 || got.Unknown != 1 || got.Pending != 0 {
		t.Fatalf("summary %s", data)
	}
	if strings.Contains(string(data), "@") || strings.Contains(string(data), "samples") {
		t.Fatalf("the import answer repeats addresses: %s", data)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM subscribers`)
	if n != before {
		t.Fatalf("the check changed the subscribers: %d, was %d", n, before)
	}

	// Pending lookups are marked per row.
	defer func(d time.Duration) { addrCheckBudget = d }(addrCheckBudget)
	addrCheckBudget = 50 * time.Millisecond
	e.dns.mu.Lock()
	e.dns.hold = make(chan struct{})
	e.dns.mu.Unlock()
	code, data = e.call(h, importer, "POST", route, "", `{"emails":["info@later.test","kim@ok.test"]}`)
	got.Issues = nil
	if err := json.Unmarshal(data, &got); err != nil || code != http.StatusOK || got.Pending != 1 ||
		!reflect.DeepEqual(got.Issues, map[int][]string{0: {"role", "pending"}}) {
		t.Fatalf("pending import check HTTP %d %s: %v", code, data, err)
	}
	close(e.dns.hold)

	if code, data = e.call(h, importer, "POST", route, "", `{"emails":[]}`); code != http.StatusOK {
		t.Fatalf("empty check HTTP %d %s", code, data)
	}
	many := `{"emails":[` + strings.Repeat(`"a@ok.test",`, addrCheckMaxEmails) + `"a@ok.test"]}`
	if code, _ = e.call(h, importer, "POST", route, "", many); code != http.StatusBadRequest {
		t.Fatalf("over the limit: HTTP %d", code)
	}
	if code, _ = e.call(h, importer, "POST", route, "", `{"emails":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("bad body: HTTP %d", code)
	}
	// A team member has no import permission.
	if code, _ = e.call(h, e.as(e.ua), "POST", route, "", body); code != http.StatusForbidden {
		t.Fatalf("member import check: HTTP %d", code)
	}
}
