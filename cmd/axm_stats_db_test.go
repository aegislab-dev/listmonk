package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	null "gopkg.in/volatiletech/null.v6"
)

// LST-64: the statistics dashboard. Results are public across teams; lists, members and
// addresses keep the permissions they had.

type statsOut struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MyTeam    string `json:"my_team"`
	Teams     []models.StatsTeam
	Campaigns []struct {
		ID                 int      `json:"id"`
		Name               string   `json:"name"`
		Subject            string   `json:"subject"`
		Sent               int      `json:"sent"`
		TeamTags           []string `json:"team_tags"`
		Viewers            int      `json:"viewers"`
		Clickers           int      `json:"clickers"`
		Hard               int      `json:"hard"`
		Soft               int      `json:"soft"`
		Complaint          int      `json:"complaint"`
		Own                bool     `json:"own"`
		ListsN             int      `json:"lists_n"`
		Lists              []models.StatsList
		TrackingOff        bool              `json:"tracking_off"`
		FollowupOf         int               `json:"followup_of"`
		FollowupCondition  map[string]string `json:"followup_condition"`
		FollowupParentSent int               `json:"followup_parent_sent"`
		CombinedClickers   int               `json:"combined_clickers"`
		CombinedViewers    int               `json:"combined_viewers"`
	} `json:"campaigns"`
}

func (e *reactionEnv) statsFor(user auth.User) (int, statsOut, string) {
	e.t.Helper()
	core.ResetStatsCache()
	code, data := e.call(e.a.GetStatsCampaigns, user, http.MethodGet, "/api/stats/campaigns", "", "")
	var out statsOut
	if code == http.StatusOK {
		if err := json.Unmarshal(data, &out); err != nil {
			e.t.Fatal(err)
		}
	}
	return code, out, string(data)
}

// statsCampaign makes a finished campaign of the given lists with 100 sends, 3 viewers (one is a
// view without a subscriber, one viewer opened twice), 2 clickers, and 1 hard, 1 soft, 1 complaint.
func (e *reactionEnv) statsCampaign(name string, startedDays int, lists ...int) int {
	e.t.Helper()
	var id int
	e.get(&id, `INSERT INTO campaigns(uuid,name,subject,from_email,body,status,messenger,to_send,sent,started_at)
		VALUES(gen_random_uuid(),$1,'Subject of '||$1,'Synthetic <sender@synthetic-sender.dev>','<p>x</p>','finished','email',100,100,NOW()-($2||' days')::INTERVAL) RETURNING id`, name, startedDays)
	for _, l := range lists {
		e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) SELECT $1,id,name FROM lists WHERE id=$2`, id, l)
	}
	subs := []int{e.addSub("enabled", "confirmed"), e.addSub("enabled", "confirmed"), e.addSub("enabled", "confirmed")}
	e.db.MustExec(`INSERT INTO campaign_views(campaign_id,subscriber_id) VALUES ($1,$2),($1,$2),($1,$3),($1,NULL)`, id, subs[0], subs[1])
	var link int
	e.get(&link, `INSERT INTO links(uuid,url) VALUES(gen_random_uuid(),'https://example.test/'||$1) RETURNING id`, name)
	e.db.MustExec(`INSERT INTO link_clicks(campaign_id,link_id,subscriber_id) VALUES ($1,$2,$3),($1,$2,NULL),($1,$2,NULL)`, id, link, subs[0])
	e.db.MustExec(`INSERT INTO bounces(subscriber_id,campaign_id,type,source) VALUES ($2,$1,'hard','api'),($3,$1,'soft','api'),($4,$1,'complaint','api')`, id, subs[0], subs[1], subs[2])
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) SELECT $1, unnest($2::INT[])`, id, pq.Array(subs))
	return id
}

func TestStatsNumbersTeamsAndVisibility(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID) // only the campaigns below
	own := e.statsCampaign("Own team", 5, e.aList)
	other := e.statsCampaign("Other team", 4, e.bList)
	both := e.statsCampaign("Both teams", 3, e.bothList)
	none := e.statsCampaign("No team", 2, e.plainList)
	var gone int
	e.get(&gone, `INSERT INTO lists(uuid,name,type,tags) VALUES(gen_random_uuid(),'Gone','private','{팀:가}') RETURNING id`)
	deleted := e.statsCampaign("Deleted list", 1, gone)
	e.db.MustExec(`DELETE FROM lists WHERE id = $1`, gone)
	old := e.statsCampaign("Old", 900, e.aList)
	e.db.MustExec(`UPDATE lists SET tags = array_append(tags, '출처:Webinar') WHERE id = $1`, e.aList)
	e.db.MustExec(`UPDATE lists SET tags = array_append(tags, '출처:Hidden source') WHERE id = $1`, e.bList)

	// Numbers: sent, unique viewers/clickers (a row without a subscriber is one person), bounce kinds.
	router := echo.New()
	initHTTPHandlers(router, e.a)
	const token = "synthetic-token"
	member := e.member
	member.Password = null.StringFrom(auth.HashAPIToken(token))
	e.db.MustExec(`UPDATE roles SET permissions = array_remove(permissions, 'campaigns:get_analytics') WHERE id = $1`, e.memberRole)
	restricted := e.as(e.ub)
	restricted.Password = member.Password
	e.db.MustExec(`UPDATE roles SET permissions = '{campaigns:get_analytics}' WHERE id = $1`, e.memberRole)
	analyticsOnly := e.as(e.plain)
	analyticsOnly.Password = member.Password
	e.a.auth.CacheAPIUsers([]auth.User{member, restricted, analyticsOnly})
	request := func(user auth.User, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"open":"any","click":"any"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.SetBasicAuth(user.Username, token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	core.ResetStatsCache()
	rec := request(member, http.MethodGet, "/api/stats/campaigns")
	code := rec.Code
	var response struct{ Data statsOut }
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	out := response.Data
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	by := map[int]int{}
	for i, c := range out.Campaigns {
		by[c.ID] = i
	}
	if _, ok := by[old]; ok {
		t.Error("a campaign older than 90 days is in the default range")
	}
	c := out.Campaigns[by[own]]
	if c.Sent != 100 || c.Viewers != 3 || c.Clickers != 2 || c.Hard != 1 || c.Soft != 1 || c.Complaint != 1 {
		t.Errorf("numbers: %+v", c)
	}
	if out.MyTeam != "팀:가" {
		t.Errorf("my team %q", out.MyTeam)
	}

	// The viewers and clickers match the campaign analytics' unique counts.
	for _, kind := range []string{"campaign_views", "link_clicks"} {
		var n int
		e.get(&n, `SELECT COALESCE(SUM(count),0) FROM (SELECT COUNT(*) AS count FROM (
			SELECT DISTINCT ON(subscriber_id, campaign_id) 1 FROM `+kind+` WHERE campaign_id = $1 ORDER BY subscriber_id, campaign_id) x) y`, own)
		want := c.Viewers
		if kind == "link_clicks" {
			want = c.Clickers
		}
		if n != want {
			t.Errorf("%s: analytics counts %d, stats %d", kind, n, want)
		}
	}

	// Teams: two tags -> two teams, no team, deleted list -> no team.
	if got := out.Campaigns[by[both]].TeamTags; len(got) != 2 {
		t.Errorf("both: %v", got)
	}
	if got := out.Campaigns[by[none]].TeamTags; len(got) != 0 {
		t.Errorf("plain list: %v", got)
	}
	if got := out.Campaigns[by[deleted]].TeamTags; len(got) != 0 {
		t.Errorf("deleted list: %v", got)
	}
	if len(out.Teams) != 2 {
		t.Errorf("teams: %+v", out.Teams)
	}

	// Visibility: a member of team 가 sees the team 나 campaign's results, but not its lists.
	if !c.Own || len(c.Lists) != 1 || c.Lists[0].Source != "Webinar" || c.Lists[0].CreatedAt == "" {
		t.Errorf("own campaign: %+v", c)
	}
	o := out.Campaigns[by[other]]
	if o.Own || len(o.Lists) != 0 || o.Name != "Other team" || o.Subject == "" || o.Viewers != 3 {
		t.Errorf("other team's campaign: %+v", o)
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "@") || strings.Contains(raw, "B1") || strings.Contains(raw, "Hidden source") {
		t.Errorf("the answer leaks addresses or another team's list data: %s", raw)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, fmt.Sprintf("/api/campaigns/%d", other)},
		{http.MethodGet, fmt.Sprintf("/api/campaigns/analytics/views?id=%d&from=2026-01-01&to=2027-01-01", other)},
		{http.MethodPost, fmt.Sprintf("/api/campaigns/%d/reaction-list/preview", other)},
		{http.MethodPost, fmt.Sprintf("/api/campaigns/%d/reaction-list", other)},
	} {
		if rec := request(member, tc.method, tc.path); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/api/stats/campaigns", "/api/stats/recipients?by=age"} {
		if rec := request(restricted, http.MethodGet, path); rec.Code != http.StatusForbidden {
			t.Errorf("missing analytics permission on %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	analytics := request(analyticsOnly, http.MethodGet, "/api/stats/campaigns")
	if analytics.Code != http.StatusOK || !strings.Contains(analytics.Body.String(), "Own team") ||
		strings.Contains(analytics.Body.String(), "Webinar") || strings.Contains(analytics.Body.String(), `"own":true`) {
		t.Errorf("analytics without campaign read permission: %d %s", analytics.Code, analytics.Body)
	}
	// The operator with lists:manage_all can open all dashboard campaign details.
	_, adm, _ := e.statsFor(e.admin)
	if !adm.Campaigns[0].Own {
		t.Error("the operator cannot see campaign details")
	}

	// All time includes the campaign older than 800 days.
	rec = request(member, http.MethodGet, "/api/stats/campaigns?from=all")
	response.Data = statsOut{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(response.Data.Campaigns) != len(out.Campaigns)+1 {
		t.Errorf("all time: %d %s", rec.Code, rec.Body)
	}

	// Adjacent periods own opposite sides of exactly the same boundary.
	e.db.MustExec(`UPDATE campaigns SET started_at = '2026-03-02T05:30:00Z' WHERE id = $1`, own)
	core.ResetStatsCache()
	for _, tc := range []struct {
		from, to string
		count    int
	}{
		{"2026-03-01T05:30:00Z", "2026-03-02T05:30:00Z", 0},
		{"2026-03-02T05:30:00Z", "2026-03-03T05:30:00Z", 1},
	} {
		rec = request(member, http.MethodGet, "/api/stats/campaigns?from="+tc.from+"&to="+tc.to)
		response.Data = statsOut{}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		got := response.Data
		if rec.Code != http.StatusOK || got.From != tc.from || got.To != tc.to || len(got.Campaigns) != tc.count {
			t.Errorf("period: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestStatsTeamOwnershipWithCompanyRead(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	own := e.statsCampaign("Own", 1, e.aList)
	shared := e.statsCampaign("Shared list", 2, e.bothList)
	foreign := e.statsCampaign("Foreign", 3, e.bList)
	mixed := e.statsCampaign("Mixed lists", 4, e.aList, e.bList)
	unassigned := e.statsCampaign("Unassigned", 5, e.plainList)

	for _, perms := range [][]string{
		{"campaigns:get", "lists:get_all"},
		{"campaigns:get_all"},
		{"campaigns:get_all", "lists:get_all"},
	} {
		t.Run(strings.Join(perms, "+"), func(t *testing.T) {
			e.db.MustExec(`UPDATE roles SET permissions = $1 WHERE id = $2`, pq.Array(append([]string{"campaigns:get_analytics"}, perms...)), e.memberRole)
			code, data, raw := e.statsFor(e.as(e.ua))
			want := map[int]bool{own: true, shared: true, foreign: false, mixed: false, unassigned: false}
			if code != http.StatusOK || len(data.Campaigns) != len(want) {
				t.Fatalf("status %d, campaigns %s", code, raw)
			}
			for _, row := range data.Campaigns {
				isOwn, ok := want[row.ID]
				if !ok || row.Own != isOwn || (len(row.Lists) > 0) != isOwn || row.Sent != 100 || row.Viewers != 3 {
					t.Errorf("unexpected dashboard row: %+v", row)
				}
			}
			if strings.Contains(raw, `"lists":[{"id":`+fmt.Sprint(e.bList)+`,`) || strings.Contains(raw, `"name":"B1"`) {
				t.Errorf("foreign list was included: %s", raw)
			}
		})
	}
}

func TestStatsTrackingOffAndFollowup(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	a := e.statsCampaign("Tracked", 20, e.aList)
	b := e.statsCampaign("Untracked", 10, e.aList)
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, b)
	e.db.MustExec(`DELETE FROM link_clicks WHERE campaign_id = $1`, b)
	e.db.MustExec(`INSERT INTO axm_tracking_off(started_at, ended_at) VALUES (NOW() - INTERVAL '12 days', NOW() - INTERVAL '8 days')`)
	_, out, _ := e.statsFor(e.member)
	off := map[int]bool{}
	for _, c := range out.Campaigns {
		off[c.ID] = c.TrackingOff
	}
	if off[a] || !off[b] {
		t.Errorf("tracking off: %v", off)
	}
	child := e.statsCampaign("Follow-up", 5, e.aList)
	e.db.MustExec(`INSERT INTO axm_campaign_followups(campaign_id,parent_campaign_id,list_uuid,condition)
		VALUES ($1,$2,gen_random_uuid(),'{"open":"any","click":"no"}')`, child, a)
	e.db.MustExec(`UPDATE link_clicks SET subscriber_id = (SELECT MIN(subscriber_id) FROM link_clicks WHERE campaign_id = $1)
		WHERE campaign_id = $2 AND subscriber_id IS NOT NULL`, a, child)
	_, out, _ = e.statsFor(e.member)
	for _, c := range out.Campaigns {
		if c.ID == child {
			if c.FollowupOf != a || c.FollowupParentSent != 100 || c.FollowupCondition["click"] != "no" ||
				c.CombinedClickers != 2 || c.CombinedViewers != 5 {
				t.Errorf("follow-up result: %+v", c)
			}
			return
		}
	}
	t.Fatal("follow-up not returned")
}

// The recipient groups come from the send records, by team, and a group under 30 people is merged.
func TestStatsRecipientsByCountryAndAge(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	id := e.statsCampaign("Recipients", 10, e.plainList)
	e.db.MustExec(`UPDATE subscribers SET attribs = '{"country":"jp"}' WHERE id IN
		(SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1)`, id)
	// Each of the same three recipients receives twelve campaigns, totaling 36 sends.
	for i := 0; i < 11; i++ {
		again := e.statsCampaign(fmt.Sprintf("Repeat %d", i), 10, e.plainList)
		e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, again)
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id, subscriber_id, opened_at)
			SELECT $1, subscriber_id, NOW() FROM axm_campaign_sends WHERE campaign_id = $2`, again, id)
	}
	large := e.statsCampaign("Thirty recipients", 10, e.aList)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, large)
	for i := 0; i < 30; i++ {
		sub := e.addSub("enabled", "confirmed")
		e.db.MustExec(`UPDATE subscribers SET attribs = '{"country":"kr"}', created_at = NOW() - INTERVAL '60 days' WHERE id = $1`, sub)
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES ($1,$2)`, large, sub)
	}
	core.ResetStatsCache()
	for by, public := range map[string]string{"country": "KR", "age": "older"} {
		code, data := e.call(e.a.GetStatsRecipients, e.member, http.MethodGet, "/api/stats/recipients?by="+by, "", "")
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", by, code)
		}
		var groups []models.StatsRecipientGroup
		if err := json.Unmarshal(data, &groups); err != nil {
			t.Fatal(err)
		}
		foundOther, foundPublic := 0, 0
		for _, g := range groups {
			switch g.Key {
			case models.StatsOtherKey:
				foundOther++
				if (g.Team != "" && g.Team != models.StatsNoTeamKey) || g.Sent != 36 || g.Recipients != 3 || g.Opened != 33 {
					t.Errorf("%s suppressed group: %+v", by, g)
				}
			case public:
				foundPublic++
				if (g.Team != "" && g.Team != "팀:가") || g.Sent != 30 || g.Recipients != 30 {
					t.Errorf("%s public group: %+v", by, g)
				}
			default:
				t.Errorf("%s exposed a small group: %+v", by, g)
			}
		}
		if foundOther != 2 || foundPublic != 2 {
			t.Errorf("%s groups: %+v", by, groups)
		}
	}
	if code, _ := e.call(e.a.GetStatsRecipients, e.member, http.MethodGet, "/api/stats/recipients?by=x", "", ""); code != http.StatusBadRequest {
		t.Errorf("unknown grouping: %d", code)
	}
}

// A campaign needs 95% coverage. The newest strictly earlier send controls both windows.
func TestStatsOverlapByCampaign(t *testing.T) {
	e := newReactionEnv(t)
	current := e.statsCampaign("Current overlap", 10, e.aList)
	prior := e.statsCampaign("Prior overlap", 50, e.aList)
	e.db.MustExec(`UPDATE campaigns SET sent = 3 WHERE id = $1`, current)
	e.db.MustExec(`UPDATE axm_campaign_sends SET sent_at = (SELECT started_at FROM campaigns WHERE id = $1) WHERE campaign_id = $1`, current)
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id, subscriber_id, sent_at)
		SELECT $2, subscriber_id, sent_at - (CASE ROW_NUMBER() OVER (ORDER BY subscriber_id)
			WHEN 1 THEN INTERVAL '3 days' WHEN 2 THEN INTERVAL '20 days' ELSE INTERVAL '35 days' END)
		FROM axm_campaign_sends WHERE campaign_id = $1`, current, prior)
	// A simultaneous send should not count toward either earlier window.
	sameTime := e.statsCampaign("Same time", 10, e.aList)
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id, subscriber_id, sent_at)
		SELECT $2, subscriber_id, sent_at FROM axm_campaign_sends WHERE campaign_id = $1`, current, sameTime)
	core.ResetStatsCache()
	code, data := e.call(e.a.GetStatsCampaigns, e.member, http.MethodGet, fmt.Sprintf("/api/stats/campaigns?overlap=true&id=%d", current), "", "")
	var out []models.StatsOverlap
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("status %d: %v", code, err)
	}
	want := models.StatsOverlap{ID: current, LedgerRows: 3, Overlap7: 1, Overlap30: 2}
	if code != http.StatusOK || len(out) != 1 || out[0] != want {
		t.Errorf("overlap: status %d, got %+v, want %+v", code, out, want)
	}
	code, data = e.call(e.a.GetStatsCampaigns, e.member, http.MethodGet, fmt.Sprintf("/api/stats/campaigns?overlap=true&id=%d", prior), "", "")
	if code != http.StatusOK || string(data) != "[]" {
		t.Errorf("insufficient ledger coverage: status %d, data %s", code, data)
	}
}

// A named SMTP participates even without engagement, and repeated recipients count once per scope.
func TestStatsNamedSMTPTrackingAndDistinctOverlap(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	named := e.statsCampaign("Named SMTP", 10, e.aList)
	e.db.MustExec(`UPDATE campaigns SET sent = 200, messenger = 'email-transactional' WHERE id = $1`, named)
	e.db.MustExec(`DELETE FROM campaign_views WHERE campaign_id = $1`, named)
	e.db.MustExec(`DELETE FROM link_clicks WHERE campaign_id = $1`, named)
	e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, named)
	e.db.MustExec(`WITH people AS (
		INSERT INTO subscribers(uuid,name,email,attribs,created_at)
		SELECT gen_random_uuid(),'Synthetic','stats-' || n || '@example.test','{"country":"kr"}',NOW()-INTERVAL '60 days'
		FROM GENERATE_SERIES(1,200) AS n RETURNING id
	) INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at)
	SELECT c.id, p.id, c.started_at FROM campaigns c CROSS JOIN people p WHERE c.id = $1`, named)
	e.db.MustExec(`INSERT INTO bounces(campaign_id, subscriber_id, type, source)
		SELECT campaign_id, subscriber_id, 'hard', 'api' FROM axm_campaign_sends WHERE campaign_id = $1 LIMIT 1`, named)
	code, campaigns, _ := e.statsFor(e.member)
	if code != http.StatusOK || len(campaigns.Campaigns) != 1 || campaigns.Campaigns[0].ID != named {
		t.Fatalf("named SMTP campaign missing: %d %+v", code, campaigns)
	}
	checkGroup := func(by string, sent, tracked, trackedPeople, opened int) {
		t.Helper()
		code, data := e.call(e.a.GetStatsRecipients, e.member, http.MethodGet, "/api/stats/recipients?by="+by, "", "")
		var groups []models.StatsRecipientGroup
		if code != http.StatusOK || json.Unmarshal(data, &groups) != nil {
			t.Fatalf("%s: %d %s", by, code, data)
		}
		for _, g := range groups {
			if g.Team != "" {
				continue
			}
			if g.Sent != sent || g.Recipients != 200 || g.TrackedSent != tracked || g.TrackedRecipients != trackedPeople ||
				g.Opened != opened || g.Clicked != 0 || g.Bounced != 1 {
				t.Errorf("%s: %+v", by, g)
			}
			return
		}
		t.Fatalf("%s: no company group", by)
	}
	checkGroup("country", 200, 0, 0, 0)
	checkGroup("age", 200, 0, 0, 0)
	for i, list := range []int{e.aList, e.bList} {
		id := e.statsCampaign(fmt.Sprintf("Repeat audience %d", i), 7-i*3, list)
		e.db.MustExec(`UPDATE campaigns SET sent = 200 WHERE id = $1`, id)
		e.db.MustExec(`DELETE FROM axm_campaign_sends WHERE campaign_id = $1`, id)
		e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id,sent_at,opened_at)
			SELECT c.id, s.subscriber_id, c.started_at, c.started_at FROM campaigns c
			CROSS JOIN axm_campaign_sends s WHERE c.id = $1 AND s.campaign_id = $2`, id, named)
	}
	core.ResetStatsCache()
	checkGroup("country", 600, 400, 200, 400)
	checkGroup("age", 600, 400, 200, 400)
	e.a.cfg.Privacy.DisableTracking = true
	checkGroup("country", 600, 0, 0, 0)

	code, data := e.call(e.a.GetStatsCampaigns, e.member, http.MethodGet, "/api/stats/campaigns?overlap=true", "", "")
	var overlaps []models.StatsOverlap
	if code != http.StatusOK || json.Unmarshal(data, &overlaps) != nil {
		t.Fatalf("overlap: %d %s", code, data)
	}
	campaignRows, periodRows := 0, 0
	for _, row := range overlaps {
		if row.ID != 0 {
			campaignRows++
			if row.LedgerRows != 200 || (row.ID == named && row.Overlap30 != 0) || (row.ID != named && row.Overlap30 != 200) {
				t.Errorf("campaign overlap: %+v", row)
			}
			continue
		}
		periodRows++
		if row.Recipients != 200 || row.Overlap7 != 200 || row.Overlap30 != 200 {
			t.Errorf("deduplicated overlap: %+v", row)
		}
	}
	if campaignRows != 3 || periodRows != 3 {
		t.Errorf("expected three campaigns plus the company and two teams: %+v", overlaps)
	}
}
