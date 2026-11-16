package core

import (
	"fmt"
	"testing"

	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

type suppressEnv struct{ *drawEnv }

func newSuppressEnv(t *testing.T) *suppressEnv { return &suppressEnv{newDrawEnv(t)} }

func (e *suppressEnv) sub(email, status string) int {
	var id int
	if err := e.db.Get(&id, `INSERT INTO subscribers(uuid,email,name,status) VALUES(gen_random_uuid(),$1,'Synthetic',$2) RETURNING id`, email, status); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *suppressEnv) suppressed() int {
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_suppressed_emails`)
	return n
}

func (e *suppressEnv) status(email string) (string, []string) {
	var st string
	if err := e.db.Get(&st, `SELECT status FROM subscribers WHERE lower(email)=lower($1)`, email); err != nil {
		e.t.Fatal(err)
	}
	var subs []string
	e.db.Select(&subs, `SELECT sl.status FROM subscriber_lists sl JOIN subscribers s ON s.id=sl.subscriber_id WHERE lower(s.email)=lower($1) ORDER BY sl.list_id`, email)
	return st, subs
}

// The table follows the blocklist: blocking adds the address, unblocking removes it, deleting a
// blocked subscriber keeps it, and a new subscriber with the address starts blocklisted.
func TestSuppressionFollowsBlocklist(t *testing.T) {
	e := newSuppressEnv(t)
	l := e.list("L", "single")
	id := e.sub("Someone@Example.invalid", "enabled")
	e.join(l, id, "confirmed")
	if e.suppressed() != 0 {
		t.Fatal("nothing is suppressed yet")
	}
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, id)
	if e.suppressed() != 1 {
		t.Fatal("blocking adds the address")
	}
	// Unblocking takes it off again.
	e.db.MustExec(`UPDATE subscribers SET status='enabled' WHERE id=$1`, id)
	if e.suppressed() != 0 {
		t.Fatal("unblocking removes the address")
	}
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, id)
	// A blocked subscriber who changes address keeps the old address blocked and blocks the new one.
	e.db.MustExec(`UPDATE subscribers SET email='other@example.invalid' WHERE id=$1`, id)
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_suppressed_emails WHERE email_hash = axm_email_hash('someone@example.invalid')`)
	if n != 1 || e.suppressed() != 2 {
		t.Fatalf("the old address stays blocked, the new one is added: %d, %d", n, e.suppressed())
	}
	e.db.MustExec(`UPDATE subscribers SET email='Someone@Example.invalid' WHERE id=$1`, id)
	e.db.MustExec(`DELETE FROM axm_suppressed_emails WHERE email_hash = axm_email_hash('other@example.invalid')`)

	// Deleting keeps the address suppressed.
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, id)
	if e.suppressed() != 1 {
		t.Fatal("deleting a blocked subscriber keeps the record")
	}
	// The same address, in any case, comes back blocklisted and unsubscribed.
	back := e.sub("someone@example.invalid", "enabled")
	e.join(l, back, "confirmed")
	st, subs := e.status("someone@example.invalid")
	if st != "blocklisted" || len(subs) != 1 || subs[0] != "unsubscribed" {
		t.Fatalf("%s %v", st, subs)
	}
	// An unblocked address is free again, and deleting it then leaves nothing behind.
	e.db.MustExec(`UPDATE subscribers SET status='enabled' WHERE id=$1`, back)
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, back)
	if e.suppressed() != 0 {
		t.Fatal("an unblocked address is not kept")
	}
	fresh := e.sub("someone@example.invalid", "enabled")
	if st, _ := e.status("someone@example.invalid"); st != "enabled" {
		t.Fatalf("fresh %d is %s", fresh, st)
	}
	// Deleting an ordinary subscriber leaves no record.
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, fresh)
	if e.suppressed() != 0 {
		t.Fatal("ordinary delete")
	}
}

// Every way an address can come back is stopped: the importer's upsert, the API's insert, a
// subscription added to the blocked subscriber, and "delete all blocklisted subscribers".
func TestSuppressionStopsEveryEntry(t *testing.T) {
	e := newSuppressEnv(t)
	l := e.list("L", "single")
	l2 := e.list("L2", "double")
	id := e.sub("gone@example.invalid", "blocklisted")
	e.join(l, id, "unsubscribed")

	// The importer: upsert-subscriber with and without overwriting, in a list the person was not on.
	for _, overwrite := range []bool{false, true} {
		if _, err := e.db.Exec(e.q["upsert-subscriber"], "00000000-0000-4000-8000-000000000001", "gone@example.invalid", "x", `{}`, pq.Array([]int{l, l2}), "confirmed", overwrite, overwrite); err != nil {
			t.Fatal(err)
		}
		st, subs := e.status("gone@example.invalid")
		if st != "blocklisted" {
			t.Fatalf("import (overwrite %v) revived the subscriber: %s", overwrite, st)
		}
		for _, s := range subs {
			if s != "unsubscribed" {
				t.Fatalf("import (overwrite %v) subscribed a blocked subscriber: %v", overwrite, subs)
			}
		}
	}

	// Delete it, then import again: a new row, but blocklisted from the start.
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, id)
	if _, err := e.db.Exec(e.q["upsert-subscriber"], "00000000-0000-4000-8000-000000000002", "gone@example.invalid", "x", `{}`, pq.Array([]int{l}), "confirmed", true, true); err != nil {
		t.Fatal(err)
	}
	st, subs := e.status("gone@example.invalid")
	if st != "blocklisted" || len(subs) != 1 || subs[0] != "unsubscribed" {
		t.Fatalf("after delete and import: %s %v", st, subs)
	}
	e.db.MustExec(`DELETE FROM subscribers WHERE lower(email)='gone@example.invalid'`)

	// The API's insert (insert-subscriber), asking for an enabled subscriber on a list.
	var newID int
	if err := e.db.Get(&newID, e.q["insert-subscriber"], "00000000-0000-4000-8000-000000000003", "gone@example.invalid", "x", "enabled", `{}`, pq.Array([]int{l}), pq.Array([]string{}), "confirmed"); err != nil {
		t.Fatal(err)
	}
	st, subs = e.status("gone@example.invalid")
	if st != "blocklisted" || len(subs) != 1 || subs[0] != "unsubscribed" {
		t.Fatalf("after the API insert: %s %v", st, subs)
	}

	// Subscribing the blocked subscriber to one more list by hand.
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed') ON CONFLICT (subscriber_id,list_id) DO UPDATE SET status='confirmed'`, newID, l2)
	e.db.MustExec(`UPDATE subscriber_lists SET status='confirmed' WHERE subscriber_id=$1 AND list_id=$2`, newID, l)
	_, subs = e.status("gone@example.invalid")
	for _, s := range subs {
		if s != "unsubscribed" {
			t.Fatalf("a blocked subscriber got subscribed: %v", subs)
		}
	}

	// "Delete all blocklisted" keeps the addresses blocked.
	if _, err := e.db.Exec(e.q["delete-blocklisted-subscribers"]); err != nil {
		t.Fatal(err)
	}
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM subscribers`)
	if n != 0 || e.suppressed() != 1 {
		t.Fatalf("%d subscribers left, %d suppressed", n, e.suppressed())
	}
	// An ordinary (unblocked) subscriber is subscribed as always.
	ok := e.sub("fine@example.invalid", "enabled")
	e.join(l, ok, "confirmed")
	if st, subs := e.status("fine@example.invalid"); st != "enabled" || subs[0] != "confirmed" {
		t.Fatalf("%s %v", st, subs)
	}
}

// The unsubscribe link's query, with blocklist on, blocks the subscriber everywhere and the
// record stops a later import.
func TestSuppressionAfterUnsubscribeLink(t *testing.T) {
	e := newSuppressEnv(t)
	a := e.list("A", "single", "팀:가")
	b := e.list("B", "single", "팀:나")
	id := e.sub("link@example.invalid", "enabled")
	e.join(a, id, "confirmed")
	e.join(b, id, "confirmed")
	camp := e.campaign(a)
	var campUUID, subUUID string
	e.db.Get(&campUUID, `SELECT uuid FROM campaigns WHERE id=$1`, camp)
	e.db.Get(&subUUID, `SELECT uuid FROM subscribers WHERE id=$1`, id)
	if _, err := e.db.Exec(e.q["unsubscribe-by-campaign"], campUUID, subUUID, true); err != nil {
		t.Fatal(err)
	}
	st, subs := e.status("link@example.invalid")
	if st != "blocklisted" || len(subs) != 2 || subs[0] != "unsubscribed" || subs[1] != "unsubscribed" {
		t.Fatalf("%s %v", st, subs)
	}
	if e.suppressed() != 1 {
		t.Fatal("recorded")
	}
	// Without the blocklist flag (the preferences form) only the campaign's lists are left.
	id2 := e.sub("pref@example.invalid", "enabled")
	e.join(a, id2, "confirmed")
	e.join(b, id2, "confirmed")
	e.db.Get(&subUUID, `SELECT uuid FROM subscribers WHERE id=$1`, id2)
	if _, err := e.db.Exec(e.q["unsubscribe-by-campaign"], campUUID, subUUID, false); err != nil {
		t.Fatal(err)
	}
	st, subs = e.status("pref@example.invalid")
	if st != "enabled" || subs[0] != "unsubscribed" || subs[1] != "confirmed" {
		t.Fatalf("%s %v", st, subs)
	}
}

// Editing the e-mail of a blocked subscriber does not free the old address: adding it again
// gives a blocklisted subscriber.
func TestSuppressionSurvivesEmailEdit(t *testing.T) {
	e := newSuppressEnv(t)
	l := e.list("L", "single")
	id := e.sub("blocked@example.invalid", "enabled")
	e.join(l, id, "confirmed")
	e.db.MustExec(`UPDATE subscribers SET status='blocklisted' WHERE id=$1`, id)
	e.db.MustExec(`UPDATE subscribers SET email='moved@example.invalid' WHERE id=$1`, id)
	again := e.sub("blocked@example.invalid", "enabled")
	e.join(l, again, "confirmed")
	if st, subs := e.status("blocked@example.invalid"); st != "blocklisted" || subs[0] != "unsubscribed" {
		t.Fatalf("the old address came back: %s %v", st, subs)
	}
	// Taking the subscriber off the blocklist frees the address it has now, not the old one.
	e.db.MustExec(`UPDATE subscribers SET status='enabled' WHERE id=$1`, id)
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_suppressed_emails WHERE email_hash = axm_email_hash('moved@example.invalid')`)
	if n != 0 {
		t.Fatal("the current address is freed by the unblock")
	}
}

// Changing another subscriber's e-mail to a suppressed address blocks that subscriber and
// unsubscribes everything, as adding the address as a new subscriber would.
func TestSuppressedAddressCannotBeTakenByEmailEdit(t *testing.T) {
	e := newSuppressEnv(t)
	l, l2 := e.list("L", "single"), e.list("L2", "single")
	gone := e.sub("gone@example.invalid", "blocklisted")
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, gone)
	other := e.sub("temp@example.invalid", "enabled")
	e.join(l, other, "confirmed")
	e.join(l2, other, "confirmed")
	e.db.MustExec(`UPDATE subscribers SET email='Gone@Example.invalid' WHERE id=$1`, other)
	st, subs := e.status("gone@example.invalid")
	if st != "blocklisted" || len(subs) != 2 || subs[0] != "unsubscribed" || subs[1] != "unsubscribed" {
		t.Fatalf("%s %v", st, subs)
	}
	// An ordinary change of address is untouched.
	fine := e.sub("fine@example.invalid", "enabled")
	e.join(l, fine, "confirmed")
	e.db.MustExec(`UPDATE subscribers SET email='fine2@example.invalid' WHERE id=$1`, fine)
	if st, subs := e.status("fine2@example.invalid"); st != "enabled" || subs[0] != "confirmed" {
		t.Fatalf("%s %v", st, subs)
	}
}

// The delete trigger keeps the address even when the record is gone (a record that was
// cleaned by hand, or never made): deleting a blocked subscriber leaves it suppressed.
func TestDeleteKeepsBlockedAddress(t *testing.T) {
	e := newSuppressEnv(t)
	id := e.sub("kept@example.invalid", "blocklisted")
	e.db.MustExec(`DELETE FROM axm_suppressed_emails`)
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, id)
	if e.suppressed() != 1 {
		t.Fatal("the delete trigger did not keep the address")
	}
}

// The real edit path (the statement the subscriber form saves with) works when the e-mail is
// changed to a suppressed address: no error, the subscriber is blocklisted and every
// subscription ends up unsubscribed, whichever lists the edit sends.
func TestEditToSuppressedAddressThroughTheEditPath(t *testing.T) {
	e := newSuppressEnv(t)
	l1, l2, l3 := e.list("L1", "single"), e.list("L2", "single"), e.list("L3", "single")
	gone := e.sub("gone@example.invalid", "blocklisted")
	e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, gone)

	cases := []struct {
		name      string
		lists     []int
		deleteAll bool
		permitted []int
		preconf   bool
	}{
		{"admin, lists sent", []int{l1, l2}, true, []int{}, true},
		{"admin, other lists", []int{l3}, true, []int{}, false},
		{"member, own lists only", []int{l1}, true, []int{l1, l2}, false},
		{"no lists sent", nil, false, []int{}, false},
		{"no lists, delete the rest", nil, true, []int{}, false},
	}
	for i, c := range cases {
		temp := e.sub(fmt.Sprintf("temp%d@example.invalid", i), "enabled")
		e.join(l1, temp, "confirmed")
		e.join(l2, temp, "confirmed")
		sub := models.Subscriber{Email: "Gone@Example.invalid", Name: "Synthetic", Status: "enabled", Attribs: models.JSON{}}
		if _, _, err := e.c.UpdateSubscriberWithLists(temp, sub, c.lists, nil, c.preconf, c.deleteAll, false, c.permitted, false); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var st string
		e.db.Get(&st, `SELECT status FROM subscribers WHERE id=$1`, temp)
		var live int
		e.db.Get(&live, `SELECT COUNT(*) FROM subscriber_lists WHERE subscriber_id=$1 AND status != 'unsubscribed'`, temp)
		if st != "blocklisted" || live != 0 {
			t.Fatalf("%s: status %s, %d live subscriptions", c.name, st, live)
		}
		// Put the address back for the next case.
		e.db.MustExec(`DELETE FROM subscribers WHERE id=$1`, temp)
		e.db.MustExec(`INSERT INTO axm_suppressed_emails (email_hash) VALUES (axm_email_hash('gone@example.invalid')) ON CONFLICT DO NOTHING`)
	}
}
