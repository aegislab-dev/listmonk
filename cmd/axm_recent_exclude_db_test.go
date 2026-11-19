package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/migrations"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

func TestRecentExclusionPeriodsAndScopes(t *testing.T) {
	e := newSendsEnv(t, 9)
	e.db.MustExec(`UPDATE lists SET tags = '{팀:가}' WHERE id = $1`, e.listID)
	var otherList int
	e.get(&otherList, `INSERT INTO lists(uuid,name,type,optin,tags)
		VALUES (gen_random_uuid(),'Other team','private','single','{팀:나}') RETURNING id`)
	same, _ := e.addCampaign("finished")
	other, _ := e.addCampaign("finished")
	optin, _ := e.addCampaign("finished")
	e.db.MustExec(`UPDATE campaign_lists SET list_id = $2 WHERE campaign_id = $1`, other, otherList)
	e.db.MustExec(`UPDATE campaigns SET type = 'optin' WHERE id = $1`, optin)
	for i, row := range []struct {
		campaign int
		age      string
	}{
		{same, "6 days 23 hours"}, {other, "7 days 1 hour"},
		{same, "13 days 23 hours"}, {other, "14 days 1 hour"},
		{same, "29 days 23 hours"}, {same, "30 days 1 hour"},
		{optin, "1 day"}, {e.campID, "1 day"}, {other, "2 days"},
	} {
		e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id, sent_at)
			VALUES ($1,$2,NOW() - $3::interval)`, row.campaign, e.eligible[i], row.age)
	}
	e.db.MustExec(`INSERT INTO axm_campaign_sends (campaign_id, subscriber_id) VALUES ($1,$2)`, other, e.eligible[0])

	for _, test := range []struct {
		days, excluded int
		scope          string
		campaigns      pq.Int64Array
	}{
		{7, 2, models.RecentScopeAll, nil},
		{14, 4, models.RecentScopeAll, nil},
		{30, 6, models.RecentScopeAll, nil},
		{7, 1, models.RecentScopeTeam, nil},
		{14, 2, models.RecentScopeTeam, nil},
		{30, 3, models.RecentScopeTeam, nil},
		{7, 2, models.RecentScopeCampaigns, pq.Int64Array{int64(other)}},
		{14, 3, models.RecentScopeCampaigns, pq.Int64Array{int64(other), int64(optin), int64(e.campID)}},
		{30, 4, models.RecentScopeCampaigns, pq.Int64Array{int64(other)}},
	} {
		t.Run(fmt.Sprintf("%s/%d", test.scope, test.days), func(t *testing.T) {
			r := models.RecentRule{Days: test.days, Scope: test.scope, CampaignIDs: test.campaigns}
			out, err := e.core.RecentExcludeCounts(context.Background(), []int{e.listID, e.listID}, r, e.campID)
			if err != nil || out.Target != 9 || out.Excluded != test.excluded || out.WillSend != 9-test.excluded {
				t.Fatalf("counts %+v: %v", out, err)
			}
			if !out.RecordsFrom.Valid || out.AsOf.Sub(out.RecordsFrom.Time) < 30*24*time.Hour {
				t.Fatalf("invalid coverage: %+v", out)
			}
			if len(out.TeamTags) != 1 || out.TeamTags[0] != "팀:가" {
				t.Fatalf("unexpected team tags %v", out.TeamTags)
			}
		})
	}

	for _, lists := range [][]int{nil, {otherList}} {
		out, err := e.core.RecentExcludeCounts(context.Background(), lists, models.RecentRule{Days: 7, Scope: models.RecentScopeAll}, 0)
		if err != nil || out.Target != 0 || out.Excluded != 0 || out.WillSend != 0 || out.AsOf.IsZero() {
			t.Fatalf("empty audience %+v: %v", out, err)
		}
	}
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES ($1,$2,'confirmed')`, e.eligible[8], otherList)
	out, err := e.core.RecentExcludeCounts(context.Background(), []int{otherList}, models.RecentRule{Days: 7, Scope: models.RecentScopeAll}, 0)
	if err != nil || out.Target != 1 || out.Excluded != 1 || out.WillSend != 0 {
		t.Fatalf("all excluded %+v: %v", out, err)
	}
}

func TestRecentExclusionMatchesSendAudienceAndDoesNotChangeSending(t *testing.T) {
	e := newSendsEnv(t, 3)
	e.setStatus("draft")
	var doubleID, baseID, dynamicID int
	e.get(&doubleID, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Double','private','double') RETURNING id`)
	e.get(&baseID, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Dynamic base','private','single') RETURNING id`)
	doubleOnly := e.addSub("enabled", "unsubscribed")
	dynamicOnly := e.addSub("enabled", "unsubscribed")
	notConfirmed := e.addSub("enabled", "unsubscribed")
	blocked := e.addSub("blocklisted", "unconfirmed")
	unsubscribed := e.addSub("enabled", "unsubscribed")
	e.db.MustExec(`INSERT INTO subscriber_lists(subscriber_id,list_id,status) VALUES
		($1,$2,'confirmed'), ($3,$2,'unconfirmed'), ($4,$2,'confirmed'), ($5,$2,'confirmed'),
		($6,$7,'confirmed'), ($8,$7,'confirmed'), ($3,$7,'unsubscribed'), ($9,$7,'unsubscribed')`,
		doubleOnly, doubleID, notConfirmed, blocked, e.eligible[0], dynamicOnly, baseID, e.eligible[1], unsubscribed)
	filter := fmt.Sprintf(`{"version":2,"base":{"type":"lists","lists":[%d]},"match":"all","conditions":[]}`, baseID)
	e.get(&dynamicID, `INSERT INTO lists(uuid,name,type,optin,axm_filter)
		VALUES (gen_random_uuid(),'Dynamic','private','single',$1::jsonb) RETURNING id`, filter)
	prior, _ := e.addCampaign("finished")
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES ($1,$2)`, prior, e.eligible[0])
	control, _ := e.addCampaign("draft")
	for _, id := range []int{e.campID, control} {
		e.db.MustExec(`INSERT INTO campaign_lists(campaign_id,list_id,list_name) VALUES ($1,$2,'Double'),($1,$3,'Dynamic')`,
			id, doubleID, dynamicID)
	}
	rule := models.RecentRule{Enabled: true, Days: 7, Scope: models.RecentScopeAll}
	count := func(lists ...int) models.RecentCounts {
		t.Helper()
		out, err := e.core.RecentExcludeCounts(context.Background(), lists, rule, e.campID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := count(dynamicID); got.Target != 2 || got.Excluded != 0 {
		t.Fatalf("dynamic only: %+v", got)
	}
	if got := count(doubleID); got.Target != 2 || got.Excluded != 1 {
		t.Fatalf("double opt-in only: %+v", got)
	}
	got := count(e.listID, doubleID, dynamicID)
	if got.Target != 5 || got.Excluded != 1 || got.WillSend != 4 {
		t.Fatalf("mixed audience: %+v", got)
	}
	if _, err := e.core.SaveRecentRule(context.Background(), e.campID, rule, 0); err != nil {
		t.Fatal(err)
	}

	e.db.MustExec(`UPDATE campaigns SET status='running' WHERE id IN ($1,$2)`, e.campID, control)
	msgr, logs := &gateMessenger{}, &syncBuffer{}
	m := e.startManager(e.store, msgr, logs)
	t.Cleanup(m.Close)
	waitFor(t, "both campaign send records", func() bool {
		return strings.Count(logs.String(), "send records:") == 2
	})
	want := append(append([]int{}, e.eligible...), doubleOnly, dynamicOnly)
	for _, id := range []int{e.campID, control} {
		delivered := e.ids(`SELECT subscriber_id FROM axm_campaign_sends WHERE campaign_id=$1 ORDER BY subscriber_id`, id)
		wantSame(t, fmt.Sprintf("campaign %d", id), delivered, want)
		if len(delivered) != got.Target {
			t.Fatalf("campaign %d sent to %d people, preview target was %d", id, len(delivered), got.Target)
		}
	}
}

func TestRecentRulePermissionsAndPersistence(t *testing.T) {
	e := newTeamsEnv(t)
	member := e.as(e.ua)
	readOnly := e.as(e.ua)
	delete(readOnly.PermissionsMap, auth.PermCampaignsManage)
	admin := e.as(e.superAdmin)

	target := e.addSub("enabled", "confirmed")
	foreign := e.addSub("enabled", "confirmed")
	e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id,list_id,status) VALUES ($1,$2,'confirmed'),($3,$4,'confirmed')`,
		target, e.aList, foreign, e.bList)
	draft, _ := e.addCampaign("draft")
	plain, _ := e.addCampaign("draft")
	previous, _ := e.addCampaign("finished")
	own, _ := e.addCampaign("finished")
	optin, _ := e.addCampaign("draft")
	e.db.MustExec(`UPDATE campaigns SET type='optin' WHERE id=$1`, optin)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$1 WHERE campaign_id IN ($2,$3,$4)`, e.aList, draft, own, optin)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, previous, e.bList)
	e.db.MustExec(`UPDATE campaign_lists SET list_id=$2 WHERE campaign_id=$1`, plain, e.plainList)
	e.db.MustExec(`INSERT INTO axm_campaign_sends(campaign_id,subscriber_id) VALUES ($1,$2)`, previous, target)

	preview := e.a.auth.Perm(e.a.PreviewRecentExclusion, auth.PermCampaignsManageAll, auth.PermCampaignsManage)
	get := e.a.auth.Perm(e.a.GetRecentRule, auth.PermCampaignsGetAll, auth.PermCampaignsGet)
	put := e.a.auth.Perm(e.a.PutRecentRule, auth.PermCampaignsManageAll, auth.PermCampaignsManage)
	const route = "/api/campaigns/recent-exclude/preview"
	previewBody := fmt.Sprintf(`{"list_ids":[%d,%d],"days":7,"scope":"all","campaign_id":%d}`, e.aList, e.bList, draft)
	code, data := e.call(preview, member, "POST", route, "", previewBody)
	var counts models.RecentCounts
	if err := json.Unmarshal(data, &counts); err != nil || code != 200 || counts.Target != 1 || counts.Excluded != 1 {
		t.Fatalf("preview %d %s: %v", code, data, err)
	}
	if code, _ = e.call(preview, readOnly, "POST", route, "", previewBody); code != http.StatusForbidden {
		t.Fatalf("read-only preview status %d", code)
	}
	if code, _ = e.call(preview, member, "POST", route, "", fmt.Sprintf(`{"list_ids":[%d],"days":7,"scope":"campaigns","campaign_ids":[%d]}`, e.aList, previous)); code != http.StatusForbidden {
		t.Fatalf("foreign campaign preview status %d", code)
	}
	if code, _ = e.call(preview, admin, "POST", route, "", fmt.Sprintf(`{"list_ids":[%d],"days":7,"scope":"team"}`, e.plainList)); code != http.StatusBadRequest {
		t.Fatalf("no team preview status %d", code)
	}
	if code, _ = e.call(preview, member, "POST", route, "", fmt.Sprintf(`{"campaign_id":%d,"days":7,"scope":"all"}`, optin)); code != http.StatusBadRequest {
		t.Fatalf("opt-in preview status %d", code)
	}
	if code, data = e.call(get, readOnly, "GET", "/x", itoa(draft), ""); code != 200 || string(data) != `{"enabled":false}` {
		t.Fatalf("initial rule %d %s", code, data)
	}

	const enabled = `{"enabled":true,"days":14,"scope":"team"}`
	for _, row := range []struct {
		user   auth.User
		id     int
		body   string
		status int
	}{
		{readOnly, draft, enabled, http.StatusForbidden},
		{member, previous, enabled, http.StatusForbidden},
		{member, own, enabled, http.StatusBadRequest},
		{member, optin, enabled, http.StatusBadRequest},
		{admin, plain, enabled, http.StatusBadRequest},
		{member, draft, `{"enabled":true,"days":8,"scope":"all"}`, http.StatusBadRequest},
		{member, draft, `{"enabled":true,"days":14,"scope":"invalid"}`, http.StatusBadRequest},
		{member, draft, `{"enabled":true,"days":14,"scope":"campaigns"}`, http.StatusBadRequest},
		{member, draft, fmt.Sprintf(`{"enabled":true,"days":14,"scope":"campaigns","campaign_ids":[%d]}`, previous), http.StatusForbidden},
	} {
		if code, _ := e.call(put, row.user, "PUT", "/x", itoa(row.id), row.body); code != row.status {
			t.Fatalf("PUT campaign %d %s: status %d, want %d", row.id, row.body, code, row.status)
		}
	}
	if code, data = e.call(put, member, "PUT", "/x", itoa(draft), enabled); code != 200 {
		t.Fatalf("save %d %s", code, data)
	}
	code, data = e.call(get, readOnly, "GET", "/x", itoa(draft), "")
	var rule models.RecentRule
	if err := json.Unmarshal(data, &rule); err != nil || code != 200 || !rule.Enabled || rule.Days != 14 || rule.Scope != "team" ||
		rule.UpdatedBy == nil || *rule.UpdatedBy != member.ID || rule.UpdatedAt == nil {
		t.Fatalf("saved rule %d %s: %v", code, data, err)
	}
	if code, _ = e.call(get, e.as(e.ub), "GET", "/x", itoa(draft), ""); code != http.StatusForbidden {
		t.Fatalf("foreign rule status %d", code)
	}
	if code, data = e.call(put, member, "PUT", "/x", itoa(draft), `{"enabled":false}`); code != 200 || string(data) != `{"enabled":false}` {
		t.Fatalf("disable %d %s", code, data)
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_campaign_recent_rules WHERE campaign_id=$1`, draft)
	if n != 0 {
		t.Fatalf("disabled rule still stored")
	}
	e.get(&n, `SELECT COUNT(*) FROM axm_audit_log WHERE action='campaign.recent_rule' AND target_id=$1
		AND detail->>'enabled' IN ('true','false')`, draft)
	if n != 2 {
		t.Fatalf("audit rows = %d, want 2", n)
	}

	if code, _ = e.call(put, member, "PUT", "/x", itoa(draft), enabled); code != 200 {
		t.Fatalf("re-enable status %d", code)
	}
	e.db.MustExec(`DELETE FROM campaigns WHERE id=$1`, draft)
	e.get(&n, `SELECT COUNT(*) FROM axm_campaign_recent_rules WHERE campaign_id=$1`, draft)
	if n != 0 {
		t.Fatal("rule survived campaign deletion")
	}
}

func TestRecentRuleMigrationParity(t *testing.T) {
	root := repoRoot(t)
	schema, err := os.ReadFile(filepath.Join(root, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join(root, "internal/migrations/v6.2.1-axm.13.go"))
	if err != nil {
		t.Fatal(err)
	}
	const start = "-- AXM recent-send exclusion. Keep this block identical to V6_2_1_axm_13."
	const end = "-- End of AXM recent-send exclusion."
	_, tail, ok := strings.Cut(string(schema), start)
	block, _, endOK := strings.Cut(tail, end)
	if !ok || !endOK || !strings.Contains(string(migration), "`"+start+block+end+"`") {
		t.Fatal("schema and migration SQL differ")
	}

	e := newSendsEnv(t, 0)
	e.db.MustExec(`DROP FUNCTION axm_recent_exclusion(INT[], INT, TEXT, INT[], INT);
		DROP FUNCTION axm_campaign_audience(INT[]); DROP TABLE axm_campaign_recent_rules`)
	for i := 0; i < 2; i++ {
		if err := migrations.V6_2_1_axm_13(e.db, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.db.MustExec(`INSERT INTO axm_campaign_recent_rules(campaign_id, days, scope) VALUES ($1, 7, 'all')`, e.campID)
	e.reinstall()
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_campaign_recent_rules`)
	if n != 0 {
		t.Fatal("reinstall did not reset recent rules")
	}
	e.get(&n, `SELECT COUNT(*) FROM pg_constraint WHERE conrelid = 'axm_campaign_recent_rules'::regclass AND contype = 'f'`)
	if n != 2 {
		t.Fatalf("after reinstall: %d foreign keys, want 2", n)
	}
}
