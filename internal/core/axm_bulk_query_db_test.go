package core

import (
	"fmt"
	"strings"
	"testing"
)

// Bulk actions by query (delete, blocklist, add to / remove from / unsubscribe
// from lists) take a WHERE expression written by the user. These tests run
// them against a real database.

type bulkEnv struct {
	*drawEnv
	listA, listB, target int
	// seoul and busan are on list A, other is on list B, loose is on no list.
	seoul, busan, other, loose int
}

func newBulkEnv(t *testing.T) *bulkEnv {
	e := &bulkEnv{drawEnv: newDrawEnv(t)}
	e.listA, e.listB, e.target = e.list("A", "single"), e.list("B", "single"), e.list("Target", "single")
	e.seoul = e.subs(e.listA, 1, "confirmed", `{"city":"Seoul"}`)[0]
	e.busan = e.subs(e.listA, 1, "unconfirmed", `{"city":"Busan"}`)[0]
	e.other = e.subs(e.listB, 1, "confirmed", `{"city":"Seoul"}`)[0]
	e.loose = testSub(e.db, `{"city":"Seoul"}`, "enabled")
	// Something a write smuggled into the expression would leave a mark on.
	e.db.MustExec(`CREATE SEQUENCE bulk_canary`)
	e.db.MustExec(`CREATE TABLE bulk_secret (id INT)`)
	return e
}

func (e *bulkEnv) allSubs() []int {
	return dbIDs(e.t, e.db, `SELECT id FROM subscribers ORDER BY id`)
}

func (e *bulkEnv) blocked() []int {
	return dbIDs(e.t, e.db, `SELECT id FROM subscribers WHERE status = 'blocklisted' ORDER BY id`)
}

func (e *bulkEnv) on(list int, status string) []int {
	return dbIDs(e.t, e.db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id = $1 AND ($2 = '' OR status = $2::subscription_status) ORDER BY 1`, list, status)
}

// canaryUntouched tells whether nextval() was ever called on the sequence.
func (e *bulkEnv) canaryUntouched() bool {
	var called bool
	if err := e.db.Get(&called, `SELECT is_called FROM bulk_canary`); err != nil {
		e.t.Fatal(err)
	}
	return !called
}

// actions are the five bulk actions, with list A as the source filter.
func (e *bulkEnv) actions(exp string) map[string]func() error {
	src, dst := []int{e.listA}, []int{e.target}
	return map[string]func() error{
		"blocklist":   func() error { return e.c.BlocklistSubscribersByQuery("", exp, src, "") },
		"delete":      func() error { return e.c.DeleteSubscribersByQuery("", exp, src, "") },
		"add":         func() error { return e.c.AddSubscriptionsByQuery("", exp, src, dst, "confirmed", "") },
		"remove":      func() error { return e.c.DeleteSubscriptionsByQuery("", exp, src, dst, "") },
		"unsubscribe": func() error { return e.c.UnsubscribeListsByQuery("", exp, src, dst, "") },
	}
}

// The expression used to be checked with $1 = true (a dry run) and then run
// with $1 = false inside the writing statement. An expression that looks at $1
// could behave during the check and do something else when it counted.
func TestBulkQueryExpressionCannotWrite(t *testing.T) {
	for _, exp := range []string{
		// Harmless on the dry run, a write (nextval) on the real run.
		`CASE WHEN $1 THEN TRUE ELSE (SELECT nextval('bulk_canary') > 0) END`,
		`NOT $1 AND nextval('bulk_canary') > 0`,
		// The same without the dry-run switch.
		`nextval('bulk_canary') > 0`,
		`(SELECT setval('bulk_canary', 100) > 0)`,
	} {
		e := newBulkEnv(t)
		for name, run := range e.actions(exp) {
			if err := run(); err == nil {
				t.Errorf("%s with %q: no error", name, exp)
			}
		}
		if !e.canaryUntouched() {
			t.Errorf("%q wrote to the database", exp)
		}
		wantIDs(t, e.allSubs(), []int{e.seoul, e.busan, e.other, e.loose})
		wantIDs(t, e.blocked(), []int{})
		wantIDs(t, e.on(e.target, ""), []int{})
		e.db.Close()
	}
}

// The expression may read the subscriber tables and nothing else.
func TestBulkQueryExpressionTables(t *testing.T) {
	e := newBulkEnv(t)
	for _, exp := range []string{
		`EXISTS (SELECT 1 FROM users)`,
		`subscribers.id IN (SELECT id FROM bulk_secret)`,
		`(SELECT COUNT(*) FROM settings) >= 0`,
		`CASE WHEN $1 THEN TRUE ELSE EXISTS (SELECT 1 FROM sessions) END`,
	} {
		for name, run := range e.actions(exp) {
			if err := run(); err == nil {
				t.Errorf("%s with %q: no error", name, exp)
			}
		}
		// It is the table check that refuses it, before the expression runs.
		if _, err := e.c.querySubscriberIDs("", exp, []int{e.listA}, ""); err == nil || !strings.Contains(err.Error(), "is not allowed") {
			t.Errorf("%q: error %v does not name the table", exp, err)
		}
	}
	wantIDs(t, e.allSubs(), []int{e.seoul, e.busan, e.other, e.loose})
	wantIDs(t, e.blocked(), []int{})
	wantIDs(t, e.on(e.target, ""), []int{})

	// The allowed tables still work.
	exp := `subscribers.id IN (SELECT subscriber_id FROM subscriber_lists sl JOIN lists l ON l.id = sl.list_id WHERE l.name = 'A')
		AND NOT EXISTS (SELECT 1 FROM bounces b WHERE b.subscriber_id = subscribers.id)
		AND NOT EXISTS (SELECT 1 FROM campaign_views v JOIN campaigns c ON c.id = v.campaign_id WHERE v.subscriber_id = subscribers.id)
		AND NOT EXISTS (SELECT 1 FROM link_clicks k JOIN links ON links.id = k.link_id WHERE k.subscriber_id = subscribers.id)`
	if err := e.c.BlocklistSubscribersByQuery("", exp, nil, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.blocked(), []int{e.seoul, e.busan})
}

// Ordinary expressions reach the same subscribers as before.
func TestBulkQueryTargets(t *testing.T) {
	const seoul = `subscribers.attribs->>'city' = 'Seoul'`

	t.Run("blocklist by expression and list", func(t *testing.T) {
		e := newBulkEnv(t)
		if err := e.c.BlocklistSubscribersByQuery("", seoul, []int{e.listA}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.blocked(), []int{e.seoul})
		// Blocklisting unsubscribes from every list.
		wantIDs(t, e.on(e.listA, "unsubscribed"), []int{e.seoul})
		wantIDs(t, e.on(e.listA, "unconfirmed"), []int{e.busan})
	})

	t.Run("blocklist by expression on all lists", func(t *testing.T) {
		e := newBulkEnv(t)
		if err := e.c.BlocklistSubscribersByQuery("", seoul, nil, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.blocked(), []int{e.seoul, e.other, e.loose})
	})

	t.Run("delete by subscription status", func(t *testing.T) {
		e := newBulkEnv(t)
		if err := e.c.DeleteSubscribersByQuery("", "", []int{e.listA}, "unconfirmed"); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.allSubs(), []int{e.seoul, e.other, e.loose})
	})

	t.Run("delete by search text", func(t *testing.T) {
		e := newBulkEnv(t)
		e.db.MustExec(`UPDATE subscribers SET name = 'Find Me' WHERE id = ANY($1)`, []byte(fmt.Sprintf("{%d,%d}", e.busan, e.other)))
		if err := e.c.DeleteSubscribersByQuery("find me", "", []int{e.listA}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.allSubs(), []int{e.seoul, e.other, e.loose})
	})

	t.Run("delete everyone on a list", func(t *testing.T) {
		e := newBulkEnv(t)
		if err := e.c.DeleteSubscribersByQuery("", "", []int{e.listA}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.allSubs(), []int{e.other, e.loose})
	})

	t.Run("list actions", func(t *testing.T) {
		e := newBulkEnv(t)
		// A subscriber already on the target keeps their status.
		e.join(e.target, e.other, "unsubscribed")
		if err := e.c.AddSubscriptionsByQuery("", seoul, nil, []int{e.target}, "confirmed", ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.on(e.target, "confirmed"), []int{e.seoul, e.loose})
		wantIDs(t, e.on(e.target, "unsubscribed"), []int{e.other})

		if err := e.c.UnsubscribeListsByQuery("", seoul, []int{e.listA}, []int{e.target}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.on(e.target, "unsubscribed"), []int{e.seoul, e.other})
		wantIDs(t, e.on(e.target, "confirmed"), []int{e.loose})

		if err := e.c.DeleteSubscriptionsByQuery("", seoul, []int{e.listB}, []int{e.target}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.on(e.target, ""), []int{e.seoul, e.loose})

		// Two target lists at once, with the default status.
		if err := e.c.AddSubscriptionsByQuery("", "", []int{e.listA}, []int{e.target, e.listB}, "", ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.on(e.listB, "unconfirmed"), []int{e.seoul, e.busan})
		wantIDs(t, e.on(e.target, "unconfirmed"), []int{e.busan})
	})

	t.Run("dynamic list as the source", func(t *testing.T) {
		e := newBulkEnv(t)
		// Everyone on list A whose city is Seoul.
		dynamic := testList(e.db, bulkCityFilter(e.listA, "Seoul"), "single")
		if err := e.c.AddSubscriptionsByQuery("", "", []int{dynamic}, []int{e.target}, "confirmed", ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.on(e.target, ""), []int{e.seoul})
		// With the user's own expression on top.
		if err := e.c.BlocklistSubscribersByQuery("", `subscribers.attribs->>'city' = 'Busan'`, []int{dynamic}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.blocked(), []int{})
		if err := e.c.DeleteSubscribersByQuery("", seoul, []int{dynamic}, ""); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, e.allSubs(), []int{e.busan, e.other, e.loose})
	})

	t.Run("nobody matches", func(t *testing.T) {
		e := newBulkEnv(t)
		for name, run := range e.actions(`subscribers.attribs->>'city' = 'Nowhere'`) {
			if err := run(); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		wantIDs(t, e.allSubs(), []int{e.seoul, e.busan, e.other, e.loose})
		wantIDs(t, e.blocked(), []int{})
		wantIDs(t, e.on(e.target, ""), []int{})
	})

	t.Run("broken expression", func(t *testing.T) {
		e := newBulkEnv(t)
		for name, run := range e.actions(`subscribers.nope = 1`) {
			if err := run(); err == nil {
				t.Errorf("%s: no error", name)
			}
		}
		wantIDs(t, e.allSubs(), []int{e.seoul, e.busan, e.other, e.loose})
	})
}

// bulkCityFilter is a dynamic list filter: members of the list whose city matches.
func bulkCityFilter(list int, city string) string {
	return fmt.Sprintf(`{"version":1,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[{"key":"city","op":"eq","value":%q}]}`, list, city)
}
