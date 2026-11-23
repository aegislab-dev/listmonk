package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

type reactionEnv struct {
	*teamsEnv
	admin, member auth.User
	ids           []int
}

func newReactionEnv(t *testing.T) *reactionEnv {
	t.Helper()
	e := newTeamsEnv(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "i18n", "ko.json"))
	if err != nil {
		t.Fatal(err)
	}
	lang, err := i18n.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.a.cfg = &Config{}
	e.a.cfg.Privacy.IndividualTracking = true
	e.a.log = log.New(io.Discard, "", 0)
	e.a.i18n = lang
	e.a.core = core.New(&core.Opt{DB: e.db, Queries: e.q, Log: e.a.log, I18n: lang}, nil)
	e.a.core.SetIndividualTracking(func() bool { return e.a.cfg.Privacy.IndividualTracking })
	e.db.MustExec(`UPDATE roles SET permissions = array_append(permissions, 'campaigns:get_analytics') WHERE id = $1`, e.memberRole)
	e.db.MustExec(`UPDATE campaigns SET status = 'finished', name = 'Synthetic engagement', started_at = NOW() WHERE id = $1`, e.campID)
	e.db.MustExec(`UPDATE lists SET axm_preset = 'b2b-sales' WHERE id = $1`, e.aList)
	e.db.MustExec(`UPDATE campaign_lists SET list_id = $2 WHERE campaign_id = $1`, e.campID, e.aList)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id) VALUES ($1,$2)`, e.campID, e.bList)

	var dynamic, deleted int
	e.get(&dynamic, `INSERT INTO lists(uuid,name,type,axm_filter) VALUES(gen_random_uuid(),'Dynamic','private','{"version":1}') RETURNING id`)
	e.get(&deleted, `INSERT INTO lists(uuid,name,type) VALUES(gen_random_uuid(),'Deleted','private') RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id) VALUES ($1,$2),($1,$3)`, e.campID, dynamic, deleted)
	e.db.MustExec(`DELETE FROM lists WHERE id = $1`, deleted)

	add := func(name, status, membership string, list int) int {
		id := e.addSub(status, membership)
		e.db.MustExec(`UPDATE subscribers SET name = $2, email = $3 WHERE id = $1`, id, name, name+"@example.test")
		e.db.MustExec(`UPDATE subscriber_lists SET list_id = $2 WHERE subscriber_id = $1`, id, list)
		return id
	}
	opened := add("Alice", "enabled", "confirmed", e.aList)
	both := add("Brenda", "enabled", "confirmed", e.bList)
	clicked := add("Charlie", "enabled", "confirmed", e.aList)
	neither := add("Daniel", "disabled", "unconfirmed", e.aList)
	blocked := add("Eve", "enabled", "confirmed", e.aList)
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = $1`, blocked)
	campaignOptOut := add("Felix", "enabled", "unsubscribed", e.aList)
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES ($1,$2,'confirmed')`, campaignOptOut, e.plainList)
	allOptOut := add("Grace", "enabled", "unsubscribed", e.plainList)
	dynamicOptOut := add("Hannah", "enabled", "confirmed", e.bList)
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES ($1,$2,'unsubscribed')`, dynamicOptOut, dynamic)
	noSend := add("Ian", "enabled", "confirmed", e.aList)

	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id)
		SELECT $1, unnest($2::INT[])`, e.campID, pq.Array([]int{opened, both, clicked, neither, blocked, campaignOptOut, allOptOut, dynamicOptOut}))
	e.db.MustExec(`INSERT INTO campaign_views(campaign_id,subscriber_id)
		SELECT $1, unnest($2::INT[])`, e.campID, pq.Array([]int{opened, opened, both, noSend}))
	var link int
	e.get(&link, `INSERT INTO links (uuid,url) VALUES (gen_random_uuid(),'https://example.test/reaction') RETURNING id`)
	e.db.MustExec(`INSERT INTO link_clicks(campaign_id,link_id,subscriber_id) VALUES ($1,$2,$3),($1,$2,$4)`, e.campID, link, both, clicked)
	return &reactionEnv{e, e.as(e.superAdmin), e.as(e.ua), []int{opened, both, clicked, neither}}
}

func (e *reactionEnv) sendRequest(makeList bool, user auth.User, campaign int, body string) (int, json.RawMessage) {
	e.t.Helper()
	h := e.a.PreviewReactionList
	path := fmt.Sprintf("/api/campaigns/%d/reaction-list", campaign)
	if makeList {
		h = e.a.MakeReactionList
	} else {
		path += "/preview"
	}
	return e.call(e.guarded(h), user, http.MethodPost, path, strconv.Itoa(campaign), body)
}

func (e *reactionEnv) preview(user auth.User, body string) models.ReactionPreview {
	e.t.Helper()
	code, data := e.sendRequest(false, user, e.campID, body)
	var got models.ReactionPreview
	if code != http.StatusOK || json.Unmarshal(data, &got) != nil {
		e.t.Fatalf("preview %s: %d %s", body, code, data)
	}
	return got
}

func (e *reactionEnv) make(user auth.User, body string) models.ReactionResult {
	e.t.Helper()
	code, data := e.sendRequest(true, user, e.campID, body)
	var got models.ReactionResult
	if code != http.StatusOK || json.Unmarshal(data, &got) != nil {
		e.t.Fatalf("make %s: %d %s", body, code, data)
	}
	return got
}

func TestReactionListConditionsAndExclusions(t *testing.T) {
	e := newReactionEnv(t)
	all := e.preview(e.admin, `{}`)
	want := models.ReactionCounts{Matched: 8, Blocklisted: 1, CampaignOptOut: 2, AllOptOut: 1, Eligible: 4}
	if all.Counts == nil || *all.Counts != want || all.Tracking.Sends != 8 || all.Tracking.Viewers != 3 || all.Tracking.Clickers != 2 {
		t.Fatalf("preview: %+v, tracking: %+v", all.Counts, all.Tracking)
	}
	if len(all.Sample) != 4 || all.Sample[0] != (models.ReactionSample{Name: "A*", Email: "Al***@e***.test"}) {
		t.Fatalf("masked sample: %+v", all.Sample)
	}

	for _, tc := range []struct {
		name, open, click string
		want              []int
	}{
		{"received", "any", "any", e.ids},
		{"opened", "yes", "any", e.ids[:2]},
		{"not opened", "no", "any", e.ids[2:]},
		{"clicked", "any", "yes", e.ids[1:3]},
		{"not clicked", "any", "no", []int{e.ids[0], e.ids[3]}},
		{"opened and not clicked", "yes", "no", e.ids[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := fmt.Sprintf(`"open":%q,"click":%q`, tc.open, tc.click)
			preview := e.preview(e.admin, "{"+settings+"}")
			result := e.make(e.admin, "{"+settings+`,"no_team":true}`)
			actual := e.sendsEnv.ids(`SELECT subscriber_id FROM subscriber_lists WHERE list_id = $1 AND status = 'confirmed' ORDER BY subscriber_id`, result.List.ID)
			wantSame(t, "eligible members", actual, tc.want)
			if *preview.Counts != result.Counts || result.List.SubscriberCount != len(tc.want) ||
				result.List.Type != "private" || result.List.Optin != "single" || result.List.AxmPreset != "b2b-sales" ||
				!slices.Equal(result.List.Tags, []string{models.ReactionListTag}) {
				t.Fatalf("created %+v with counts %+v, preview %+v", result.List, result.Counts, preview.Counts)
			}
		})
	}

	// Changing a subscription after the preview changes the create result.
	e.db.MustExec(`UPDATE subscriber_lists SET status = 'unsubscribed' WHERE subscriber_id = $1 AND list_id = $2`, e.ids[0], e.aList)
	result := e.make(e.admin, `{"no_team":true}`)
	if result.Counts.Eligible != 3 || result.Counts.CampaignOptOut != 3 {
		t.Fatalf("did not recheck eligibility: %+v", result.Counts)
	}
}

func TestReactionListTeamAndPermissions(t *testing.T) {
	e := newReactionEnv(t)
	member := e.preview(e.member, `{}`)
	if member.Counts.OutOfScope != 1 || member.Counts.Eligible != 3 || len(member.SourceTeams) != 0 {
		t.Fatalf("member scope: %+v", member)
	}
	for _, sub := range member.Sample {
		if sub.Name == "B*" {
			t.Fatal("member preview included another team's subscriber")
		}
	}
	made := e.make(e.member, `{"team":"팀:나"}`)
	wantSame(t, "team scope", e.sendsEnv.ids(`SELECT subscriber_id FROM subscriber_lists WHERE list_id = $1 ORDER BY subscriber_id`, made.List.ID), []int{e.ids[0], e.ids[2], e.ids[3]})
	if !sameSet(made.List.Tags, []string{models.ReactionListTag, "팀:가"}) {
		t.Fatal(made.List.Tags)
	}
	inherited := e.make(e.admin, `{}`)
	if !sameSet(inherited.List.Tags, []string{models.ReactionListTag, "팀:가", "팀:나"}) {
		t.Fatal(inherited.List.Tags)
	}
	chosen := e.make(e.admin, `{"team":"팀:나"}`)
	if !sameSet(chosen.List.Tags, []string{models.ReactionListTag, "팀:나"}) {
		t.Fatal(chosen.List.Tags)
	}
	var audited int
	e.get(&audited, `SELECT (detail->'made_from_campaign'->'counts'->>'eligible')::INT
		FROM axm_audit_log WHERE target_id = $1 AND action = $2`, chosen.List.ID, auditListCreate)
	if audited != 4 {
		t.Fatal("incorrect audited count", audited)
	}
	withoutAnalytics := e.as(e.ua)
	delete(withoutAnalytics.PermissionsMap, auth.PermCampaignsGetAnalytics)
	withoutCampaigns := e.as(e.ua)
	delete(withoutCampaigns.PermissionsMap, auth.PermCampaignsGet)
	for _, user := range []auth.User{withoutAnalytics, withoutCampaigns, e.as(e.plain)} {
		for _, makeList := range []bool{false, true} {
			if code, _ := e.sendRequest(makeList, user, e.campID, `{}`); code != http.StatusForbidden {
				t.Fatalf("permission: %d", code)
			}
		}
	}
	hidden, _ := e.addCampaign("finished")
	if code, _ := e.sendRequest(false, e.member, hidden, `{}`); code != http.StatusForbidden {
		t.Fatalf("campaign outside member's lists: %d", code)
	}

	// A global campaign reader without list read permissions still gets no people.
	reader := e.as(e.ua)
	reader.PermissionsMap[auth.PermCampaignsGetAll] = struct{}{}
	reader.GetListIDs = nil
	reader.ManageListIDs = nil
	noRead := e.preview(reader, `{}`)
	if noRead.Counts.Eligible != 0 || noRead.Counts.OutOfScope != 4 || len(noRead.Sample) != 0 {
		t.Fatalf("empty permission scope: %+v", noRead)
	}
}

func TestReactionListTrackingAndEmpty(t *testing.T) {
	e := newReactionEnv(t)
	var before int
	e.get(&before, `SELECT COUNT(*) FROM lists`)
	assertBad := func(body string, id int, makeList bool) {
		t.Helper()
		if code, _ := e.sendRequest(makeList, e.admin, id, body); code != http.StatusBadRequest {
			t.Fatalf("%s, make=%t: got %d", body, makeList, code)
		}
	}

	empty := e.preview(e.admin, `{"open":"no","click":"yes"}`)
	if empty.Counts.Eligible != 1 || len(empty.Sample) != 1 {
		t.Fatalf("click without open: %+v", empty)
	}
	e.db.MustExec(`UPDATE subscribers SET status = 'blocklisted' WHERE id = ANY($1::INT[])`, pq.Array(e.ids))
	empty = e.preview(e.admin, `{}`)
	if empty.Counts.Eligible != 0 || len(empty.Sample) != 0 {
		t.Fatalf("zero preview: %+v", empty)
	}
	assertBad(`{"no_team":true}`, e.campID, true)

	e.db.MustExec(`UPDATE campaign_views SET subscriber_id = NULL WHERE campaign_id = $1`, e.campID)
	e.db.MustExec(`UPDATE link_clicks SET subscriber_id = NULL WHERE campaign_id = $1`, e.campID)
	for _, makeList := range []bool{false, true} {
		assertBad(`{"open":"no","no_team":true}`, e.campID, makeList)
		assertBad(`{"click":"yes","no_team":true}`, e.campID, makeList)
		assertBad(`{"open":"invalid","no_team":true}`, e.campID, makeList)
	}
	e.a.cfg.Privacy.IndividualTracking = false
	tracking := e.preview(e.admin, `{"tracking_only":true}`)
	if tracking.Tracking.IndividualTracking || tracking.Tracking.Viewers != 0 || tracking.Tracking.Clickers != 0 || tracking.Counts != nil {
		t.Fatalf("anonymous tracking: %+v", tracking)
	}

	noSends, _ := e.addCampaign("finished")
	code, data := e.sendRequest(false, e.admin, noSends, `{"tracking_only":true}`)
	var noHistory models.ReactionPreview
	if code != http.StatusOK || json.Unmarshal(data, &noHistory) != nil || noHistory.Tracking.Sends != 0 {
		t.Fatalf("no send records metadata: %d %s", code, data)
	}
	for _, makeList := range []bool{false, true} {
		assertBad(`{"no_team":true}`, noSends, makeList)
	}
	var after int
	e.get(&after, `SELECT COUNT(*) FROM lists`)
	if before != after {
		t.Fatal("a failed creation left a list")
	}
}

func TestReactionListAdminRequiresTeamChoice(t *testing.T) {
	e := newReactionEnv(t)
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, e.campID)
	if code, _ := e.sendRequest(true, e.admin, e.campID, `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing team selection: %d", code)
	}
	if code, _ := e.sendRequest(true, e.admin, e.campID, `{"team":"팀:missing"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown team: %d", code)
	}
	e.db.MustExec(`DELETE FROM roles WHERE axm_team_tag IS NOT NULL`)
	if code, _ := e.sendRequest(true, e.admin, e.campID, `{"team":"팀:missing"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown team when no teams exist: %d", code)
	}
	result := e.make(e.admin, `{"no_team":true}`)
	if result.List.AxmPreset != core.DefaultColumnPreset || !sameSet(result.List.Tags, []string{models.ReactionListTag}) {
		t.Fatalf("teamless result: %+v", result.List)
	}
}
