package core

import (
	"testing"

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
	// A blocked subscriber who changes address moves the record.
	e.db.MustExec(`UPDATE subscribers SET email='other@example.invalid' WHERE id=$1`, id)
	var n int
	e.db.Get(&n, `SELECT COUNT(*) FROM axm_suppressed_emails WHERE email_hash = axm_email_hash('someone@example.invalid')`)
	if n != 0 || e.suppressed() != 1 {
		t.Fatalf("the old address is released: %d, %d", n, e.suppressed())
	}
	e.db.MustExec(`UPDATE subscribers SET email='Someone@Example.invalid' WHERE id=$1`, id)

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
