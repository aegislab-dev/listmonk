package core

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// The 11/23 production failure: the same conditions as the screen (all subscribers, valid
// subscription, country = US, follower >= 10,000, follower < 100,000) answered 500 because
// Postgres could not make a parallel query's shared memory segment ("could not resize shared
// memory segment ... No space left on device", /dev/shm full).
//
// Reproduce it by pointing AXM_TEST_DB_URL at a Postgres started with a tiny /dev/shm
// (docker run --shm-size=4m) and setting AXM_TEST_TINY_SHM=1. On an ordinary database the
// test still checks that the preview counts the rows right.
func TestDynamicDBPreviewWithoutSharedMemory(t *testing.T) {
	db, q, _ := dynamicTestDB(t)
	c := testCore(t, db, q)
	base := testList(db, nil, "single")
	// With a tiny /dev/shm, enough rows that the parallel hash join outgrows it (the
	// production plan); an ordinary run only needs the count to be right.
	rows := 20000
	if os.Getenv("AXM_TEST_TINY_SHM") != "" {
		rows = 300000
	}
	db.MustExec(`WITH s AS (
		INSERT INTO subscribers(uuid,email,name,status,attribs)
		SELECT gen_random_uuid(), 'u'||g||'@example.invalid', 'Synthetic', 'enabled',
			jsonb_build_object('country', CASE WHEN g % 2 = 0 THEN 'US' ELSE 'KR' END, 'follower', g)
		FROM generate_series(1, $2::int) g RETURNING id)
		INSERT INTO subscriber_lists(subscriber_id,list_id,status) SELECT id, $1, 'confirmed' FROM s`, base, rows)
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
	for g := 10000; g < 100000 && g <= rows; g++ {
		if g%2 == 0 {
			want++
		}
	}
	p, err := c.PreviewDynamicFilter(f, 0, 3)
	if err != nil {
		if os.Getenv("AXM_TEST_TINY_SHM") != "" {
			t.Logf("tiny shm: preview failed as in production: %v", err)
		}
		t.Fatalf("preview: %v", err)
	}
	if p.Count != want || len(p.Sample) != 3 {
		t.Fatalf("preview %+v want count %d", p, want)
	}
	var re *DynamicPreviewError
	if errors.As(err, &re) {
		t.Fatal("unexpected typed error")
	}
}
