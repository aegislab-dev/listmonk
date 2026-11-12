package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
)

// columnsTestCore is testCore with the English messages, so error paths can be exercised.
func columnsTestCore(t *testing.T, db *sqlx.DB, q map[string]string) *Core {
	t.Helper()
	c := testCore(t, db, q)
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../i18n/en.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.i18n, err = i18n.New(raw); err != nil {
		t.Fatal(err)
	}
	return c
}

func subIDs(subs models.Subscribers) []int {
	ids := []int{}
	for _, s := range subs {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestSubscriberColumnsDBSort(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)
	list := testList(db, nil, "single")
	// Numbers written three ways, text, blanks and missing values.
	rows := []string{
		`{"follower":"12,500"}`,  // 0: 12500
		`{"follower":"9000"}`,    // 1: 9000
		`{"follower":100000}`,    // 2: 100000
		`{"follower":"약 3만"}`,    // 3: text
		`{"follower":""}`,        // 4: blank
		`{}`,                     // 5: missing
		`{"follower":" 1e3 "}`,   // 6: 1000
		`{"follower":"abc"}`,     // 7: text
		`{"follower":null}`,      // 8: null
		`{"follower":"9000"}`,    // 9: 9000 again, later ID
		`{"follower":["a","b"]}`, // 10: a list sorts as its text
	}
	ids := []int{}
	for _, attrs := range rows {
		id := testSub(db, attrs, "enabled")
		ids = append(ids, id)
		db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, id, list)
	}
	pick := func(idx ...int) []int {
		out := []int{}
		for _, i := range idx {
			out = append(out, ids[i])
		}
		return out
	}
	// Numbers first by value, then text, and subscribers without a value last in both directions.
	for _, tc := range []struct {
		order string
		want  []int
	}{
		{"asc", pick(6, 1, 9, 0, 2, 10, 7, 3, 4, 5, 8)},
		{"desc", pick(2, 0, 9, 1, 6, 3, 7, 10, 8, 5, 4)},
	} {
		out, n, err := c.QuerySubscribers("", "", []int{list}, "", tc.order, "attribs.follower", 0, 100)
		if err != nil || n != len(rows) {
			t.Fatal(n, err)
		}
		wantIDs(t, subIDs(out), tc.want)
	}
	// A column nobody has a value for (country before it is filled in) keeps the ID order.
	out, _, err := c.QuerySubscribers("", "", []int{list}, "", "asc", "attribs.country", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, subIDs(out), ids)
	// Paging follows the same order.
	out, _, err = c.QuerySubscribers("", "", []int{list}, "", "asc", "attribs.follower", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, subIDs(out), pick(9, 0, 2))
	// Keys with quotes, backslashes or placeholders are only ever a literal.
	for _, key := range []string{`k'); DROP TABLE lists; --`, `k\'`, `%query%`, `%order%`, `회사 명`} {
		if _, n, err := c.QuerySubscribers("", "", []int{list}, "", "desc", "attribs."+key, 0, 100); err != nil || n != len(rows) {
			t.Fatal(key, n, err)
		}
	}
	// Anything else falls back to the default order, as before.
	out, _, err = c.QuerySubscribers("", "", []int{list}, "", "asc", "attribs.", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, subIDs(out), ids)
	var lists int
	db.Get(&lists, `SELECT COUNT(*) FROM lists`)
	if lists != 1 {
		t.Fatal("lists table changed")
	}
}

func TestSubscriberColumnsDBFilter(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)
	list := testList(db, nil, "single")
	other := testList(db, nil, "single")
	rows := []string{
		`{"country":"PH","follower":"600,000"}`, // 0
		`{"country":"PH","follower":"1200"}`,    // 1
		`{"country":"SG","follower":900000}`,    // 2
		`{"region":"PH","follower":"700000"}`,   // 3: no country yet
		`{"country":"PH","follower":"약 3만"}`,    // 4
		`{"country":"50% off' OR 1=1 --"}`,      // 5
	}
	ids := []int{}
	for _, attrs := range rows {
		id := testSub(db, attrs, "enabled")
		ids = append(ids, id)
		db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, id, list)
	}
	// The same subscribers' twins in another list must never be touched by list-scoped actions.
	outside := testSub(db, `{"country":"PH","follower":"600,000"}`, "enabled")
	db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, outside, other)

	query := func(filter, sql string) []int {
		t.Helper()
		exp, err := AttribFilterExp([]byte(filter))
		if err != nil {
			t.Fatal(filter, err)
		}
		out, n, err := c.QuerySubscribers("", AndSQLExp(sql, exp), []int{list}, "", "asc", "", 0, 100)
		if err != nil || n != len(out) {
			t.Fatal(filter, n, err)
		}
		return subIDs(out)
	}
	wantIDs(t, query(`[{"key":"country","op":"eq","value":"PH"}]`, ""), []int{ids[0], ids[1], ids[4]})
	wantIDs(t, query(`[{"key":"country","op":"eq","value":"PH"},{"key":"follower","op":"gte","value":"500,000"}]`, ""), []int{ids[0]})
	wantIDs(t, query(`[{"key":"follower","op":"lt","value":"650000"}]`, ""), []int{ids[0], ids[1]})
	wantIDs(t, query(`[{"key":"country","op":"empty"}]`, ""), []int{ids[3]})
	wantIDs(t, query(`[{"key":"country","op":"contains","value":"% off'"}]`, ""), []int{ids[5]})
	wantIDs(t, query(`[{"key":"country","op":"eq","value":"50% off' OR 1=1 --"}]`, ""), []int{ids[5]})
	// Text that looks like a bind parameter or a placeholder is compared as text.
	wantIDs(t, query(`[{"key":"country","op":"eq","value":"$1"}]`, ""), []int{})
	wantIDs(t, query(`[{"key":"%query%","op":"empty"},{"key":"%order%","op":"empty"}]`, ""), ids)
	// With the user's own SQL query, both must hold.
	wantIDs(t, query(`[{"key":"country","op":"eq","value":"PH"}]`, "subscribers.attribs->>'follower' = '1200' OR subscribers.attribs->>'follower' = '900000'"), []int{ids[1]})
	// Sorting a filtered list.
	exp, _ := AttribFilterExp([]byte(`[{"key":"follower","op":"gt","value":"0"}]`))
	out, _, err := c.QuerySubscribers("", exp, []int{list}, "", "desc", "attribs.follower", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, subIDs(out), []int{ids[2], ids[3], ids[0], ids[1]})

	// Export applies the same conditions.
	exp, _ = AttribFilterExp([]byte(`[{"key":"country","op":"eq","value":"PH"}]`))
	next, err := c.ExportSubscribers("", exp, nil, []int{list}, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Batches of two: the conditions hold on every batch.
	exported := []int{}
	for {
		batch, err := next()
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			exported = append(exported, s.ID)
		}
	}
	wantIDs(t, exported, []int{ids[0], ids[1], ids[4]})

	// "Select all" actions with a condition touch only the subscribers the table showed.
	exp, _ = AttribFilterExp([]byte(`[{"key":"country","op":"eq","value":"PH"},{"key":"follower","op":"gte","value":"500000"}]`))
	if err := c.BlocklistSubscribersByQuery("", exp, []int{list}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT id FROM subscribers WHERE status='blocklisted' ORDER BY id`), []int{ids[0]})
	exp, _ = AttribFilterExp([]byte(`[{"key":"follower","op":"lte","value":"1200"}]`))
	if err := c.DeleteSubscribersByQuery("", exp, []int{list}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT id FROM subscribers ORDER BY id`), []int{ids[0], ids[2], ids[3], ids[4], ids[5], outside})
	exp, _ = AttribFilterExp([]byte(`[{"key":"country","op":"eq","value":"SG"}]`))
	members := func() []int {
		return dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 AND status != 'unsubscribed' ORDER BY 1`, other)
	}
	if err := c.AddSubscriptionsByQuery("", exp, []int{list}, []int{other}, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, members(), []int{ids[2], outside})
	if err := c.UnsubscribeListsByQuery("", exp, []int{list}, []int{other}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, members(), []int{outside})
	if err := c.DeleteSubscriptionsByQuery("", exp, []int{list}, []int{other}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 ORDER BY 1`, other), []int{outside})
	// A condition that matches nobody changes nothing.
	exp, _ = AttribFilterExp([]byte(`[{"key":"country","op":"eq","value":"nowhere"}]`))
	if err := c.DeleteSubscribersByQuery("", exp, []int{list}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT id FROM subscribers ORDER BY id`), []int{ids[0], ids[2], ids[3], ids[4], ids[5], outside})
}

func TestSubscriberColumnsDBPresets(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := columnsTestCore(t, db, q)
	// A second preset exists only for this test, to cover choosing and inheriting one.
	saved := ColumnPresets
	ColumnPresets = append(append([]ColumnPreset{}, saved...), ColumnPreset{ID: "testb2b", Columns: []string{"company"}})
	t.Cleanup(func() { ColumnPresets = saved })

	plain, err := c.CreateList(models.List{Name: "plain"})
	if err != nil || plain.AxmPreset != DefaultColumnPreset {
		t.Fatal(plain.AxmPreset, err)
	}
	b2b, err := c.CreateList(models.List{Name: "b2b", AxmPreset: "testb2b"})
	if err != nil || b2b.AxmPreset != "testb2b" {
		t.Fatal(b2b.AxmPreset, err)
	}
	if _, err := c.CreateList(models.List{Name: "bad", AxmPreset: "nope"}); err == nil {
		t.Fatal("unknown preset accepted")
	}
	// A new dynamic list takes the preset of its first base list, unless one is given.
	dyn := func(name, preset string, base ...int) models.List {
		t.Helper()
		f := models.List{Name: name, AxmPreset: preset}
		raw := []byte(testFilter("lists", base))
		if len(base) == 0 {
			raw = []byte(testFilter("all", nil))
		}
		j := types.JSONText(raw)
		f.AxmFilter = &j
		out, err := c.CreateList(f)
		if err != nil {
			t.Fatal(name, err)
		}
		return out
	}
	if got := dyn("inherits", "", b2b.ID, plain.ID).AxmPreset; got != "testb2b" {
		t.Fatal(got)
	}
	if got := dyn("first base wins", "", plain.ID, b2b.ID).AxmPreset; got != DefaultColumnPreset {
		t.Fatal(got)
	}
	if got := dyn("chosen", DefaultColumnPreset, b2b.ID).AxmPreset; got != DefaultColumnPreset {
		t.Fatal(got)
	}
	if got := dyn("all subscribers", "").AxmPreset; got != DefaultColumnPreset {
		t.Fatal(got)
	}
	// Editing keeps the preset unless one is given, and refuses an unknown one.
	up, err := c.UpdateList(b2b.ID, models.List{Name: "b2b renamed"})
	if err != nil || up.AxmPreset != "testb2b" || up.Name != "b2b renamed" {
		t.Fatal(up.AxmPreset, up.Name, err)
	}
	up, err = c.UpdateList(b2b.ID, models.List{Name: "b2b renamed", AxmPreset: DefaultColumnPreset})
	if err != nil || up.AxmPreset != DefaultColumnPreset {
		t.Fatal(up.AxmPreset, err)
	}
	if _, err := c.UpdateList(b2b.ID, models.List{Name: "b2b renamed", AxmPreset: "nope"}); err == nil {
		t.Fatal("unknown preset accepted on update")
	}
	// The base list changing later does not change the dynamic list.
	var got string
	db.Get(&got, `SELECT axm_preset FROM lists WHERE name='inherits'`)
	if got != "testb2b" {
		t.Fatal(got)
	}
	// Lists read through the other list queries carry the preset too.
	all, err := c.GetLists("", "", true, nil)
	if err != nil || len(all) == 0 {
		t.Fatal(err)
	}
	for _, l := range all {
		if !ValidColumnPreset(l.AxmPreset) {
			t.Fatalf("%s: %q", l.Name, l.AxmPreset)
		}
	}
}

func TestSubscriberColumnsDBMigration(t *testing.T) {
	db, _, _ := dynamicTestDB(t)
	old := testList(db, nil, "single")
	// The schema before this release, then the upgrade twice.
	db.MustExec(`ALTER TABLE lists DROP COLUMN axm_preset`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_5(db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	var preset string
	if err := db.Get(&preset, `SELECT axm_preset FROM lists WHERE id=$1`, old); err != nil || preset != DefaultColumnPreset {
		t.Fatal(preset, err)
	}
	// A value set after the upgrade survives running it again.
	db.MustExec(`UPDATE lists SET axm_preset='later' WHERE id=$1`, old)
	if err := migrations.V6_2_1_axm_5(db, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	db.Get(&preset, `SELECT axm_preset FROM lists WHERE id=$1`, old)
	if preset != "later" {
		t.Fatal(preset)
	}
	// An insert that does not name the column, as an older image would do, gets the default.
	db.MustExec(`INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'old image','private','single')`)
	db.Get(&preset, `SELECT axm_preset FROM lists WHERE name='old image'`)
	if preset != DefaultColumnPreset {
		t.Fatal(preset)
	}
	// The column added by the upgrade is the one a new install creates.
	var def string
	db.Get(&def, `SELECT data_type || ' ' || is_nullable || ' ' || column_default FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='lists' AND column_name='axm_preset'`)
	if def != "text NO 'creator'::text" {
		t.Fatal(def)
	}
}
