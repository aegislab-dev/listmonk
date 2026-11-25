package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// LST-64: the statistics dashboard. Results are public across teams; lists, members and
// addresses keep the permissions they had.

type statsOut struct {
	MyTeam    string `json:"my_team"`
	Teams     []models.StatsTeam
	Campaigns []struct {
		ID          int      `json:"id"`
		Name        string   `json:"name"`
		Subject     string   `json:"subject"`
		Sent        int      `json:"sent"`
		TeamTags    []string `json:"team_tags"`
		Viewers     int      `json:"viewers"`
		Clickers    int      `json:"clickers"`
		Hard        int      `json:"hard"`
		Soft        int      `json:"soft"`
		Complaint   int      `json:"complaint"`
		Own         bool     `json:"own"`
		ListsN      int      `json:"lists_n"`
		Lists       []struct{ ID int }
		TrackingOff bool `json:"tracking_off"`
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
	old := e.statsCampaign("Old", 400, e.aList)

	// Numbers: sent, unique viewers/clickers (a row without a subscriber is one person), bounce kinds.
	code, out, _ := e.statsFor(e.member)
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
	if !c.Own || len(c.Lists) != 1 {
		t.Errorf("own campaign: %+v", c)
	}
	o := out.Campaigns[by[other]]
	if o.Own || len(o.Lists) != 0 || o.Name != "Other team" || o.Subject == "" || o.Viewers != 3 {
		t.Errorf("other team's campaign: %+v", o)
	}
	code, _, raw := e.statsFor(e.member)
	if strings.Contains(raw, "@") || strings.Contains(raw, "B1") || strings.Contains(raw, "list_names") {
		t.Errorf("the answer leaks addresses or the other team's list: %s", raw)
	}
	// ... and the paths that open the campaign stay closed.
	if err := e.a.checkCampaignPerm(auth.PermTypeGet, other, e.ctx(e.member)); err == nil {
		t.Error("a team member can open another team's campaign")
	}
	if err := e.a.checkCampaignPerm(auth.PermTypeGet, own, e.ctx(e.member)); err != nil {
		t.Errorf("a team member cannot open their own campaign: %v", err)
	}
	// An operator with campaigns:get_all reads every list.
	_, adm, _ := e.statsFor(e.admin)
	if !adm.Campaigns[0].Own {
		t.Error("an operator with campaigns:get_all sees no lists")
	}

	// A range reaches back: the old campaign appears with from=1 year ago.
	core.ResetStatsCache()
	code, data := e.call(e.a.GetStatsCampaigns, e.member, http.MethodGet, "/api/stats/campaigns?from="+e.dayStamp(-500)+"&to="+e.dayStamp(1), "", "")
	var wide statsOut
	json.Unmarshal(data, &wide)
	if code != http.StatusOK || len(wide.Campaigns) != len(out.Campaigns)+1 {
		t.Errorf("wide range: %d, %d campaigns", code, len(wide.Campaigns))
	}
	if code, _ := e.call(e.a.GetStatsCampaigns, e.member, http.MethodGet, "/api/stats/campaigns?from=2020-01-01&to=2026-12-01", "", ""); code != http.StatusBadRequest {
		t.Errorf("a range of years: %d", code)
	}
}

func (e *reactionEnv) dayStamp(offset int) string {
	var s string
	e.get(&s, `SELECT TO_CHAR(NOW() + ($1||' days')::INTERVAL, 'YYYY-MM-DD')`, offset)
	return s
}

func TestStatsTrackingOffAndFollowup(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	a := e.statsCampaign("Tracked", 20, e.aList)
	b := e.statsCampaign("Untracked", 10, e.aList)
	e.db.MustExec(`INSERT INTO axm_tracking_off(started_at, ended_at) VALUES (NOW() - INTERVAL '12 days', NOW() - INTERVAL '8 days')`)
	_, out, _ := e.statsFor(e.member)
	off := map[int]bool{}
	for _, c := range out.Campaigns {
		off[c.ID] = c.TrackingOff
	}
	if off[a] || !off[b] {
		t.Errorf("tracking off: %v", off)
	}
}

// The recipient groups come from the send records, by team, and a group under 30 people is merged.
func TestStatsRecipientsByCountryAndAge(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaigns WHERE id = $1`, e.campID)
	id := e.statsCampaign("Recipients", 10, e.aList)
	e.db.MustExec(`UPDATE subscribers SET attribs = '{"country":"kr"}' WHERE id IN (SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id = $1)`, id)
	core.ResetStatsCache()
	for _, by := range []string{"country", "age"} {
		code, data := e.call(e.a.GetStatsRecipients, e.member, http.MethodGet, "/api/stats/recipients?by="+by, "", "")
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", by, code)
		}
		var groups []models.StatsRecipientGroup
		if err := json.Unmarshal(data, &groups); err != nil {
			t.Fatal(err)
		}
		if len(groups) == 0 {
			t.Fatalf("%s: no groups", by)
		}
		if by == "country" {
			// 3 people are below 30: merged into "other", for everyone and for the team.
			for _, g := range groups {
				if g.Key != models.StatsOtherKey || g.Sent != 3 {
					t.Errorf("country group %+v", g)
				}
			}
		}
	}
	if code, _ := e.call(e.a.GetStatsRecipients, e.member, http.MethodGet, "/api/stats/recipients?by=x", "", ""); code != http.StatusBadRequest {
		t.Errorf("unknown grouping: %d", code)
	}
}

func TestMergeRecipientGroups(t *testing.T) {
	rows := []models.StatsRecipientRow{
		{CampaignID: 1, Key: "KR", Sent: 100, Opened: 40, Clicked: 10, Bounced: 1},
		{CampaignID: 1, Key: "JP", Sent: 12, Opened: 5, Clicked: 1},
		{CampaignID: 2, Key: "JP", Sent: 10, Opened: 2},
		{CampaignID: 2, Key: "US", Sent: 5, Clicked: 1},
	}
	got := core.MergeRecipientGroups(rows, map[int][]string{1: {"팀:가"}, 2: {"팀:나"}}, true)
	at := func(team, key string) (models.StatsRecipientGroup, bool) {
		for _, g := range got {
			if g.Team == team && g.Key == key {
				return g, true
			}
		}
		return models.StatsRecipientGroup{}, false
	}
	// Everyone: KR 100, JP 22 + US 5 are below 30 each -> other 27.
	if g, ok := at("", "KR"); !ok || g.Sent != 100 {
		t.Errorf("everyone KR: %+v", g)
	}
	if g, ok := at("", models.StatsOtherKey); !ok || g.Sent != 27 || g.Clicked != 2 {
		t.Errorf("everyone other: %+v", g)
	}
	if _, ok := at("", "JP"); ok {
		t.Error("a group below 30 stays on its own")
	}
	// Team 나 has 15 recipients, all below 30: only "other".
	if g, ok := at("팀:나", models.StatsOtherKey); !ok || g.Sent != 15 {
		t.Errorf("team other: %+v", g)
	}
	// Age groups are not merged.
	age := core.MergeRecipientGroups([]models.StatsRecipientRow{{CampaignID: 1, Key: "new7", Sent: 3}}, nil, false)
	if len(age) != 1 || age[0].Key != "new7" {
		t.Errorf("age: %+v", age)
	}
}
