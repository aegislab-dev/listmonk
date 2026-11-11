package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/goyesql/v2"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// AXM_TEST_DB_URL must name a disposable database. Each test owns a separate schema.
func dynamicTestDB(t *testing.T) (*sqlx.DB, map[string]string, string) {
	t.Helper()
	url := os.Getenv("AXM_TEST_DB_URL")
	if url == "" {
		t.Skip("AXM_TEST_DB_URL not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	db.MustExec("CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public; ALTER EXTENSION pgcrypto SET SCHEMA public")
	name := fmt.Sprintf("axm_test_%d", time.Now().UnixNano())
	db.MustExec("CREATE SCHEMA " + pq.QuoteIdentifier(name))
	db.Close()
	if strings.HasPrefix(url, "postgres") {
		u, err := urlpkg.Parse(url)
		if err != nil {
			t.Fatal(err)
		}
		v := u.Query()
		v.Set("search_path", name)
		u.RawQuery = v.Encode()
		url = u.String()
	} else {
		url += " search_path=" + name
	}
	db, err = sqlx.Connect("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Exec("DROP SCHEMA " + pq.QuoteIdentifier(name) + " CASCADE"); db.Close() })
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../..")
	raw, err := os.ReadFile(filepath.Join(root, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db.MustExec(string(raw))
	// Destructive schema.sql must never fall back to public during installation.
	// Add public only afterwards, to resolve pgcrypto functions in named queries.
	db.Close()
	if strings.HasPrefix(url, "postgres") {
		u, err := urlpkg.Parse(url)
		if err != nil {
			t.Fatal(err)
		}
		v := u.Query()
		v.Set("search_path", name+",public")
		u.RawQuery = v.Encode()
		url = u.String()
	} else {
		url += ",public"
	}
	db, err = sqlx.Connect("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	queries := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(root, "queries/*.sql"))
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := goyesql.ParseBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range parsed {
			queries[k] = v.Query
		}
	}
	return db, queries, string(raw)
}
func dbIDs(t *testing.T, db *sqlx.DB, q string, args ...any) []int {
	t.Helper()
	ids := []int{}
	if err := db.Select(&ids, q, args...); err != nil {
		t.Fatal(err, q)
	}
	return ids
}
func wantIDs(t *testing.T, got, want []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs got %v want %v", got, want)
	}
}
func testList(db *sqlx.DB, filter any, optin string) int {
	var id int
	db.Get(&id, `INSERT INTO lists(uuid,name,type,optin,axm_filter) VALUES(gen_random_uuid(),'Synthetic','private',$1,$2::jsonb) RETURNING id`, optin, filter)
	return id
}
func testSub(db *sqlx.DB, attrs, status string) int {
	var id int
	if err := db.Get(&id, `INSERT INTO subscribers(uuid,email,name,status,attribs) VALUES(gen_random_uuid(),gen_random_uuid()::text||'@example.invalid','Synthetic',$1,$2::jsonb) RETURNING id`, status, attrs); err != nil {
		panic(err)
	}
	return id
}
func testFilter(base string, ids []int) string {
	b, _ := json.Marshal(DynamicFilter{Version: 1, Base: DynamicBase{Type: base, Lists: ids}, Match: "all", Conditions: []DynamicCondition{{Key: "plan", Op: "eq", Value: func() *string { s := "pro"; return &s }()}}})
	return string(b)
}
func testCore(t *testing.T, db *sqlx.DB, q map[string]string) *Core {
	t.Helper()
	queries := &models.Queries{}
	// Scan only the statements used here; production scans the complete map at startup.
	typ := reflect.ValueOf(queries).Elem()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Type().Field(i)
		raw := q[field.Tag.Get("query")]
		if raw == "" {
			continue
		}
		if typ.Field(i).Kind() == reflect.String {
			typ.Field(i).SetString(raw)
		} else if !strings.Contains(raw, "%") {
			stmt, err := db.Preparex(raw)
			if err != nil {
				t.Fatal(field.Name, err)
			}
			t.Cleanup(func() { stmt.Close() })
			typ.Field(i).Set(reflect.ValueOf(stmt))
		}
	}
	return New(&Opt{DB: db, Queries: queries, Log: log.New(io.Discard, "", 0)}, nil)
}

func TestDynamicDBOperators(t *testing.T) {
	db, _, _ := dynamicTestDB(t)
	cases := []struct {
		attrs, key, op, value string
		want                  bool
	}{
		{`{"x":"Pro"}`, "x", "eq", "Pro", true}, {`{"x":"Pro"}`, "x", "eq", "pro", false},
		{`{"x":12}`, "x", "eq", "12", true}, {`{"x":false}`, "x", "eq", "false", true},
		{`{"x":{}}`, "x", "eq", "{}", false}, {`{"x":[]}`, "x", "eq", "[]", false},
		{`{"x":"a%_b'\\c"}`, "x", "contains", `%_b'\`, true},
		{`{"x":"Pro"}`, "x", "contains", "pR", true}, {`{"x":false}`, "x", "contains", "als", true},
		{`{"회사명":"서울 회사"}`, "회사명", "contains", "서울", true},
		{`{}`, "x", "empty", "", true}, {`{"x":null}`, "x", "empty", "", true},
		{`{"x":" \t\n\r"}`, "x", "empty", "", true}, {`{"x":[]}`, "x", "empty", "", true},
		{`{"x":{}}`, "x", "empty", "", false}, {`{"x":false}`, "x", "empty", "", false},
		{`{"tags":["vip",1]}`, "tags", "has", "vip", true}, {`{"tags":[1]}`, "tags", "has", "1", false},
		{`{"tags":"vip"}`, "tags", "has", "vip", false}, {`{"x":"pro"}`, "x", "SQL", "pro", false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := map[string]any{"key": tc.key, "op": tc.op}
			if tc.op != "empty" {
				c["value"] = tc.value
			}
			raw, _ := json.Marshal(map[string]any{"version": 1, "match": "all", "conditions": []any{c}})
			var got bool
			if err := db.Get(&got, `SELECT axm_filter_match($1::jsonb,$2::jsonb)`, tc.attrs, string(raw)); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	for _, match := range []string{"all", "any"} {
		f := fmt.Sprintf(`{"version":1,"match":%q,"conditions":[{"key":"x","op":"eq","value":"yes"},{"key":"y","op":"empty"}]}`, match)
		var got bool
		db.Get(&got, `SELECT axm_filter_match('{"x":"no"}'::jsonb,$1::jsonb)`, f)
		if got != (match == "any") {
			t.Fatal(match, got)
		}
	}
	for _, raw := range []string{`{}`, `{"version":1,"match":"all","conditions":[]}`, `{"version":1,"match":"all","conditions":{}}`} {
		var got bool
		if err := db.Get(&got, `SELECT axm_filter_match('{}',$1::jsonb)`, raw); err != nil || got {
			t.Fatal(raw, got, err)
		}
	}
}

func TestDynamicDBMembershipAndQueries(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := testCore(t, db, q)
	single := testList(db, nil, "single")
	double := testList(db, nil, "double")
	filter := testFilter("lists", []int{single, double})
	dynamic := testList(db, filter, "single")
	ids := []int{}
	for i := 0; i < 7; i++ {
		status := "enabled"
		if i == 4 {
			status = "blocklisted"
		}
		ids = append(ids, testSub(db, `{"plan":"pro"}`, status))
	}
	// Single valid, double unconfirmed, double confirmed, unsubscribed, blocklisted, orphan, excluded.
	for i, row := range []struct {
		list   int
		status string
	}{{single, "unconfirmed"}, {double, "unconfirmed"}, {double, "confirmed"}, {single, "unsubscribed"}, {single, "confirmed"}, {0, ""}, {single, "confirmed"}} {
		if row.list > 0 {
			db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,$3)`, ids[i], row.list, row.status)
		}
	}
	db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unsubscribed')`, ids[6], dynamic)
	want := []int{ids[0], ids[2]}
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dynamic), want)
	all := testFilter("all", nil)
	var count int
	db.Get(&count, q["axm-preview-dynamic-filter"], all, dynamic)
	if count != 2 {
		t.Fatal(count)
	}
	// Members are recalculated without adding relationship rows.
	db.MustExec(`UPDATE subscribers SET attribs='{}' WHERE id=$1`, ids[0])
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dynamic), []int{ids[2]})
	db.MustExec(`UPDATE subscribers SET attribs='{"plan":"pro"}' WHERE id=$1`, ids[0])
	for _, source := range [][]int{{dynamic}, {dynamic, single}} {
		out, n, err := c.QuerySubscribers("", "", source, "", "asc", "", 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != n {
			t.Fatal(len(out), n)
		}
		actual := []int{}
		for _, s := range out {
			actual = append(actual, s.ID)
		}
		if len(source) == 1 {
			wantIDs(t, actual, want)
		} else {
			wantIDs(t, actual, []int{ids[0], ids[2], ids[3], ids[4], ids[6]})
		}
	}
	out, n, err := c.QuerySubscribers("", "", []int{dynamic}, "unsubscribed", "asc", "", 0, 100)
	if err != nil || n != 1 || len(out) != 1 || out[0].ID != ids[6] {
		t.Fatal(out, n, err)
	}
	export, err := c.ExportSubscribers("", "", nil, []int{dynamic}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	exported := []int{}
	for {
		rows, err := export()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			exported = append(exported, r.ID)
		}
	}
	wantIDs(t, exported, want)
	target := testList(db, nil, "single")
	if err := c.AddSubscriptionsByQuery("", "", []int{dynamic}, []int{target}, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 ORDER BY subscriber_id`, target), want)
	if err := c.DeleteSubscriptionsByQuery("", "", []int{dynamic}, []int{target}, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.AddSubscriptionsByQuery("", "", []int{dynamic}, []int{target}, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.UnsubscribeListsByQuery("", "", []int{dynamic}, []int{target}, ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 AND status='unsubscribed' ORDER BY subscriber_id`, target), want)
	// Counts and nullable JSON scan through actual Core create/get/update paths.
	l, err := c.GetList(dynamic, "")
	if err != nil || l.AxmFilter == nil || l.SubscriberCount != 2 {
		t.Fatal(l, err)
	}
	lists, _, err := c.QueryLists("", "", "", "", nil, "", "", true, nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lists {
		if l.ID == dynamic && l.SubscriberCount != 2 {
			t.Fatal(l)
		}
	}
	db.MustExec(`DELETE FROM lists WHERE id=ANY($1::int[])`, pq.Array([]int{single, double}))
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dynamic), []int{})
}

func TestDynamicDBTriggerPaths(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	normal := testList(db, nil, "single")
	dynamic := testList(db, testFilter("all", nil), "single")
	targets := pq.Array([]int{normal, dynamic})
	var sub int
	if err := db.Get(&sub, q["insert-subscriber"], "10000000-0000-0000-0000-000000000001", "insert@example.invalid", "Synthetic", "enabled", `{}`, targets, pq.Array([]string{}), "confirmed"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Queryx(q["upsert-subscriber"], "10000000-0000-0000-0000-000000000002", "upsert@example.invalid", "Synthetic", `{}`, targets, "confirmed", true, true)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := db.Exec(q["update-subscriber-with-lists"], sub, "insert@example.invalid", "Updated", "enabled", `{}`, targets, pq.Array([]string{}), "confirmed", true, pq.Array([]int{}), false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(q["add-subscribers-to-lists"], pq.Array([]int{sub}), targets, "confirmed"); err != nil {
		t.Fatal(err)
	}
	c := testCore(t, db, q)
	if err := c.AddSubscriptionsByQuery("", "", nil, []int{normal, dynamic}, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1`, dynamic), []int{})
	db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unsubscribed')`, sub, dynamic)
	db.MustExec(`UPDATE subscriber_lists SET status='confirmed' WHERE list_id=$1`, dynamic)
	wantIDs(t, dbIDs(t, db, `SELECT subscriber_id FROM subscriber_lists WHERE list_id=$1 AND status='unsubscribed'`, dynamic), []int{sub})
	var n int
	db.Get(&n, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id=$1`, normal)
	if n != 2 {
		t.Fatal(n)
	}
}

func TestDynamicDBCampaignsAndUnsubscribe(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	normal := testList(db, nil, "single")
	double := testList(db, nil, "double")
	dynamic := testList(db, testFilter("lists", []int{normal}), "single")
	allDynamic := testList(db, testFilter("all", nil), "single")
	ids := []int{}
	for i := 0; i < 5; i++ {
		attrs := `{"plan":"pro"}`
		if i == 2 {
			attrs = `{"plan":"free"}`
		}
		s := testSub(db, attrs, "enabled")
		ids = append(ids, s)
		db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'confirmed')`, s, normal)
	}
	db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unconfirmed')`, ids[0], double)
	db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES($1,$2,'unsubscribed')`, ids[4], dynamic)
	cases := []struct {
		name, typ   string
		lists, want []int
	}{{"dynamic", "regular", []int{dynamic}, []int{ids[0], ids[1], ids[3]}}, {"mixed", "regular", []int{normal, dynamic}, ids}, {"regular", "regular", []int{normal}, ids}, {"optin", "optin", []int{double, dynamic}, []int{ids[0]}}, {"zero", "regular", []int{testList(db, testFilter("lists", []int{99999}), "single")}, []int{}}}
	var dynamicCamp int
	for _, tc := range cases {
		for _, batch := range []int{1, 2, 100} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, batch), func(t *testing.T) {
				var camp int
				err := db.Get(&camp, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger,type,status) VALUES(gen_random_uuid(),'Synthetic','Synthetic','test@example.invalid','body','email',$1,'running') RETURNING id`, tc.typ)
				if err != nil {
					t.Fatal(err)
				}
				for _, l := range tc.lists {
					db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES($1,$2,'Synthetic')`, camp, l)
				}
				rows, err := db.Queryx(q["next-campaigns"], pq.Array([]int{}), pq.Array([]int{}))
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
				var toSend, max int
				db.QueryRow(`SELECT to_send,max_subscriber_id FROM campaigns WHERE id=$1`, camp).Scan(&toSend, &max)
				if toSend != len(tc.want) {
					t.Fatalf("to_send %d want %v", toSend, tc.want)
				}
				if len(tc.want) > 0 && max != tc.want[len(tc.want)-1] {
					t.Fatalf("max %d", max)
				}
				actual := []int{}
				after := 0
				query := q["next-campaign-subscribers-dynamic"]
				if tc.name == "regular" {
					query = q["next-campaign-subscribers"]
				}
				for i := 0; i < 20; i++ {
					var out []models.Subscriber
					if err := db.Select(&out, query, camp, tc.typ, after, max, pq.Array(tc.lists), batch); err != nil {
						t.Fatal(err)
					}
					if len(out) == 0 {
						break
					}
					for _, s := range out {
						actual = append(actual, s.ID)
						after = s.ID
					}
				}
				wantIDs(t, actual, tc.want)
				if tc.name == "dynamic" {
					dynamicCamp = camp
				}
				db.MustExec(`UPDATE campaigns SET status='finished' WHERE id=$1`, camp)
			})
		}
	}
	var campUUID, subUUID string
	db.Get(&campUUID, `SELECT uuid FROM campaigns WHERE id=$1`, dynamicCamp)
	db.Get(&subUUID, `SELECT uuid FROM subscribers WHERE id=$1`, ids[0])
	if _, err := db.Exec(q["unsubscribe-by-campaign"], campUUID, subUUID, false); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, dbIDs(t, db, `SELECT list_id FROM subscriber_lists WHERE subscriber_id=$1 AND status='unsubscribed' ORDER BY list_id`, ids[0]), []int{normal, dynamic})
	wantIDs(t, dbIDs(t, db, `SELECT * FROM axm_dynamic_list_members($1)`, dynamic), []int{ids[1], ids[3]})
	// all-base unsubscribe affects every ordinary subscription, and blocklist still excludes globally.
	var allCamp int
	db.Get(&allCamp, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'Synthetic','S','test@example.invalid','b','email') RETURNING id`)
	db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES($1,$2,'Synthetic')`, allCamp, allDynamic)
	db.Get(&campUUID, `SELECT uuid FROM campaigns WHERE id=$1`, allCamp)
	if _, err := db.Exec(q["unsubscribe-by-campaign"], campUUID, subUUID, true); err != nil {
		t.Fatal(err)
	}
	var status string
	db.Get(&status, `SELECT status FROM subscribers WHERE id=$1`, ids[0])
	if status != "blocklisted" {
		t.Fatal(status)
	}
}

func TestDynamicDBMigrationParity(t *testing.T) {
	db, _, schema := dynamicTestDB(t)
	marker := "-- AXM dynamic lists. Keep this block identical to V6_2_1_axm_3."
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../migrations/v6.2.1-axm.3.go"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(schema, marker)
	end := strings.Index(schema[start:], "-- Verified external reply addresses (AXM).")
	if end < 0 {
		t.Fatal("missing sender schema block boundary")
	}
	block := schema[start : start+end]
	if !strings.Contains(string(raw), strings.TrimSpace(block)) {
		t.Fatal("schema and migration SQL differ")
	}
	defs := func() []string {
		var out []string
		err := db.Select(&out, `SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND proname LIKE 'axm_%' ORDER BY proname`)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := defs()
	// Simulate official schema, then upgrade twice.
	db.MustExec(`DROP TRIGGER axm_guard_dynamic_subscription ON subscriber_lists; ALTER TABLE lists DROP COLUMN axm_filter CASCADE`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_3(db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, defs()) {
		t.Fatal("function definitions differ after migration")
	}
	var n int
	db.Get(&n, `SELECT COUNT(*) FROM pg_trigger WHERE tgrelid='subscriber_lists'::regclass AND tgname='axm_guard_dynamic_subscription'`)
	if n != 1 {
		t.Fatal(n)
	}
}

// schema.sql starts with DROP statements. A public relation with the same name
// must survive installation into the isolated test schema.
func TestDynamicDBPreservesPublic(t *testing.T) {
	dsn := os.Getenv("AXM_TEST_DB_URL")
	if dsn == "" {
		t.Skip("AXM_TEST_DB_URL not set")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var exists bool
	if err := db.Get(&exists, `SELECT to_regclass('public.subscribers') IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if !exists {
		db.MustExec(`CREATE TABLE public.subscribers(axm_sentinel BOOLEAN)`)
		t.Cleanup(func() {
			cleanup, err := sqlx.Connect("postgres", dsn)
			if err == nil {
				cleanup.Exec(`DROP TABLE public.subscribers`)
				cleanup.Close()
			}
		})
	}
	var before int64
	db.Get(&before, `SELECT 'public.subscribers'::regclass::oid`)
	dynamicTestDB(t)
	var after int64
	if err := db.Get(&after, `SELECT 'public.subscribers'::regclass::oid`); err != nil || before != after {
		t.Fatalf("public relation changed: %d -> %d (%v)", before, after, err)
	}
}
