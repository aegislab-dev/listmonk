package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// The 11/23 production failure: the same conditions as the screen (all subscribers, valid
// subscription, country = US, follower >= 10,000, follower < 100,000) answered 500 because
// Postgres could not make a parallel query's shared memory segment ("could not resize shared
// memory segment ... No space left on device", /dev/shm full).
//
// To see it fail without the retry, point AXM_TEST_DB_URL at a Postgres started with a tiny
// /dev/shm that is already full:
//
//	docker run --shm-size=4m ... postgres:17.7-alpine
//	docker exec <pg> dd if=/dev/zero of=/dev/shm/fill bs=1k count=3900
//
// The test forces parallel plans on its connection, once with a custom plan and once with a
// cached generic plan (the prepared statements keep the plan they first used). On an ordinary
// database it only checks that the counts are right.
func TestDynamicDBPreviewWithoutSharedMemory(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	// One connection, so the session settings below apply to every preview query.
	db.SetMaxOpenConns(1)
	c := testCore(t, db, q)
	base := testList(db, nil, "single")
	db.MustExec(`WITH s AS (
		INSERT INTO subscribers(uuid,email,name,status,attribs)
		SELECT gen_random_uuid(), 'u'||g||'@example.invalid', 'Synthetic', 'enabled',
			jsonb_build_object('country', CASE WHEN g % 2 = 0 THEN 'US' ELSE 'KR' END, 'follower', g)
		FROM generate_series(1, 20000) g RETURNING id)
		INSERT INTO subscriber_lists(subscriber_id,list_id,status) SELECT id, $1, 'confirmed' FROM s`, base)
	db.MustExec(`ANALYZE subscribers; ANALYZE subscriber_lists`)
	str := func(s string) *string { return &s }
	raw, _ := json.Marshal(DynamicFilter{Version: 1, Base: DynamicBase{Type: "all"}, Match: "all", Conditions: []DynamicCondition{
		{Key: "country", Op: "eq", Value: str("US")},
		{Key: "follower", Op: "gte", Value: str("10,000")},
		{Key: "follower", Op: "lt", Value: str("100,000")},
	}})
	f, err := ValidateDynamicFilter(raw)
	if err != nil {
		t.Fatal(err)
	}
	// US is every even g; the follower range is 10,000 <= g < 100,000.
	want := 0
	for g := 10000; g <= 20000; g++ {
		if g%2 == 0 {
			want++
		}
	}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		t.Run(mode, func(t *testing.T) {
			db.MustExec(`SET debug_parallel_query = on; SET plan_cache_mode = ` + mode)
			// Several runs: the statements are prepared and reused, as in production.
			for i := 0; i < 7; i++ {
				p, err := c.PreviewDynamicFilter(f, 0, 3)
				if err != nil {
					t.Fatalf("run %d: %v", i, err)
				}
				if p.Count != want || len(p.Sample) != 3 || p.Excluded.Blocklisted != 0 {
					t.Fatalf("run %d: %+v want count %d", i, p, want)
				}
			}
		})
	}
}

// The retry itself, without depending on how full the DB's /dev/shm is: the first try fails
// with the production error, and the second runs in a transaction with parallel workers off
// and a fresh plan.
func TestRunPreviewJobRetriesWithoutParallelWorkers(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := testCore(t, db, q)
	setting, err := db.Preparex(`SELECT current_setting('max_parallel_workers_per_gather') || '/' || current_setting('plan_cache_mode')`)
	if err != nil {
		t.Fatal(err)
	}
	defer setting.Close()
	var tries int
	var got string
	err = c.runPreviewJob(func(ctx context.Context, use func(*sqlx.Stmt) *sqlx.Stmt) error {
		tries++
		if tries == 1 {
			return &pq.Error{Code: "53100", Message: `could not resize shared memory segment "/PostgreSQL.1" to 2097152 bytes: No space left on device`}
		}
		return use(setting).GetContext(ctx, &got)
	})
	if err != nil || tries != 2 || got != "0/force_custom_plan" {
		t.Fatalf("tries %d, settings %q, err %v", tries, got, err)
	}
	// The setting is local to the retry's transaction.
	if err := db.Get(&got, `SELECT current_setting('max_parallel_workers_per_gather')`); err != nil || got == "0" {
		t.Fatalf("setting leaked: %q %v", got, err)
	}

	// Errors that more workers cannot explain are not retried.
	for _, code := range []string{"57014", "22P02", "42601"} {
		tries = 0
		err = c.runPreviewJob(func(ctx context.Context, use func(*sqlx.Stmt) *sqlx.Stmt) error {
			tries++
			return &pq.Error{Code: pq.ErrorCode(code)}
		})
		var pe *pq.Error
		if tries != 1 || !errors.As(err, &pe) || string(pe.Code) != code {
			t.Fatalf("%s: tries %d, err %v", code, tries, err)
		}
	}
	// A retry that fails too reports its own error.
	tries = 0
	err = c.runPreviewJob(func(ctx context.Context, use func(*sqlx.Stmt) *sqlx.Stmt) error {
		tries++
		return &pq.Error{Code: "53100"}
	})
	if tries != 2 || classifyPreviewError(err) != PreviewReasonResource {
		t.Fatalf("tries %d, err %v", tries, err)
	}
}
