package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/labstack/echo/v4"
)

// LST-70: "select all, delete" on a bounces screen that is filtered by campaign or source
// deletes what the screen shows, not every bounce.
func TestDeleteAllBouncesStaysInsideFilter(t *testing.T) {
	e := newRulesEnv(t)
	var campA, campB, sub int
	e.get(&campA, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'A','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&campB, `INSERT INTO campaigns(uuid,name,subject,from_email,body,messenger) VALUES(gen_random_uuid(),'B','S','test@example.invalid','b','email') RETURNING id`)
	e.get(&sub, `INSERT INTO subscribers (uuid, email, name, status) VALUES (gen_random_uuid(), 'bf@example.invalid', 'P', 'enabled') RETURNING id`)

	// campaign, source: A/api x2, A/ses, B/api, no campaign/api.
	seed := func() {
		e.db.MustExec(`DELETE FROM bounces`)
		for _, r := range []struct {
			camp   any
			source string
		}{{campA, "api"}, {campA, "api"}, {campA, "ses"}, {campB, "api"}, {nil, "api"}} {
			e.db.MustExec(`INSERT INTO bounces (subscriber_id, campaign_id, type, source, meta) VALUES ($1, $2, 'soft', $3, '{}')`, sub, r.camp, r.source)
		}
	}
	total := func() int {
		var n int
		e.get(&n, `SELECT COUNT(*) FROM bounces`)
		return n
	}
	del := func(q url.Values) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/bounces?"+q.Encode(), nil)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		if err := e.a.DeleteBounces(c); err != nil {
			he, ok := err.(*echo.HTTPError)
			if !ok {
				t.Fatal(err)
			}
			return he.Code
		}
		return http.StatusOK
	}
	camp := func(id int) url.Values { return url.Values{"all": {"true"}, "campaign_id": {itoa(id)}} }

	for _, c := range []struct {
		name string
		q    url.Values
		code int
		left int
	}{
		{"no filter still deletes everything", url.Values{"all": {"true"}}, 200, 0},
		{"campaign filter", camp(campA), 200, 2},
		{"source filter", url.Values{"all": {"true"}, "source": {"api"}}, 200, 1},
		{"campaign and source", url.Values{"all": {"true"}, "campaign_id": {itoa(campA)}, "source": {"api"}}, 200, 3},
		{"other campaign", camp(campB), 200, 4},
		{"unknown campaign deletes nothing", camp(campB + 1000), 200, 5},
		{"unreadable campaign is refused, nothing deleted", url.Values{"all": {"true"}, "campaign_id": {"abc"}}, 400, 5},
		{"zero campaign is refused, nothing deleted", url.Values{"all": {"true"}, "campaign_id": {"0"}}, 400, 5},
		{"empty campaign is refused, nothing deleted", url.Values{"all": {"true"}, "campaign_id": {""}}, 400, 5},
		{"empty source is refused, nothing deleted", url.Values{"all": {"true"}, "source": {""}}, 400, 5},
		{"two campaigns are refused, nothing deleted", url.Values{"all": {"true"}, "campaign_id": {itoa(campA), itoa(campB)}}, 400, 5},
		{"filter without all deletes nothing and wants ids", url.Values{"campaign_id": {itoa(campA)}}, 400, 5},
	} {
		seed()
		if code := del(c.q); code != c.code || total() != c.left {
			t.Errorf("%s: code %d (want %d), %d bounces left (want %d)", c.name, code, c.code, total(), c.left)
		}
	}

	// Ids given with a filter: the ids win, the filter only narrows "all".
	seed()
	var ids []int
	if err := e.db.Select(&ids, `SELECT id FROM bounces WHERE campaign_id = $1 ORDER BY id LIMIT 1`, campB); err != nil {
		t.Fatal(err)
	}
	if code := del(url.Values{"id": {itoa(ids[0])}, "campaign_id": {itoa(campA)}}); code != 200 || total() != 4 {
		t.Errorf("by id: code %d, %d left (want 4)", code, total())
	}
	// Without "all" the filters are not read at all, so what worked before still works.
	for _, f := range []url.Values{{"campaign_id": {"0"}}, {"campaign_id": {""}}, {"campaign_id": {"abc"}}, {"source": {""}}} {
		seed()
		ids = nil
		if err := e.db.Select(&ids, `SELECT id FROM bounces ORDER BY id LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		q := url.Values{"id": {itoa(ids[0])}}
		for k, v := range f {
			q[k] = v
		}
		if code := del(q); code != 200 || total() != 4 {
			t.Errorf("by id with %v: code %d, %d left (want 4)", f, code, total())
		}
	}
}
