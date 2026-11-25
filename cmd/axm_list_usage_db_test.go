package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// The campaigns that used a list (LST-63): sent, sending or scheduled ones count, drafts
// and campaigns that never sent anything do not.
func TestListCampaignsWhatCounts(t *testing.T) {
	e := newSendsEnv(t, 3)
	e.db.MustExec(`DELETE FROM campaign_lists`)
	e.db.MustExec(`DELETE FROM campaigns`)
	list := e.listID
	mk := func(status string, sent int, lists ...int) int {
		id := e.pfCampaign("regular", status, lists...)
		e.db.MustExec(`UPDATE campaigns SET sent = $2, to_send = $2, started_at = now() WHERE id = $1`, id, sent)
		return id
	}
	mk("draft", 0, list)
	cancelled0 := mk("cancelled", 0, list)
	finished := mk("finished", 5, list)
	cancelled := mk("cancelled", 2, list)
	scheduled := mk("scheduled", 0, list)
	other := e.pfList("single", "")
	mk("finished", 9, other)

	rows, err := e.core.GetListCampaigns([]int{list}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]models.ListCampaignUse{}
	for _, r := range rows {
		got[r.CampaignID] = r
	}
	if len(rows) != 3 || got[finished].Sent != 5 || got[cancelled].Status != "cancelled" || got[scheduled].Status != "scheduled" {
		t.Fatalf("%+v", rows)
	}
	if _, ok := got[cancelled0]; ok {
		t.Fatal("a cancelled campaign that sent nothing is not a use")
	}
	for _, r := range rows {
		if r.Total != 3 || r.ListsN != 1 || r.ListID != list {
			t.Fatalf("%+v", r)
		}
	}
	if rows, err := e.core.GetListCampaigns(nil, false); err != nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}

// A list shows its newest five and the full count; a campaign on several lists says so.
func TestListCampaignsLimitAndManyLists(t *testing.T) {
	e := newSendsEnv(t, 3)
	e.db.MustExec(`DELETE FROM campaign_lists`)
	e.db.MustExec(`DELETE FROM campaigns`)
	list, other := e.listID, e.pfList("single", "")
	var ids []int
	for i := 0; i < 7; i++ {
		id := e.pfCampaign("regular", "finished", list)
		e.db.MustExec(`UPDATE campaigns SET sent = 10, to_send = 10, started_at = $2 WHERE id = $1`, id, time.Now().Add(-time.Duration(7-i)*time.Hour))
		ids = append(ids, id)
	}
	both := e.pfCampaign("regular", "finished", list, other)
	e.db.MustExec(`UPDATE campaigns SET sent = 40, to_send = 40, started_at = now() WHERE id = $1`, both)

	rows, err := e.core.GetListCampaigns([]int{list, other}, false)
	if err != nil {
		t.Fatal(err)
	}
	var onList, onOther []models.ListCampaignUse
	for _, r := range rows {
		if r.ListID == list {
			onList = append(onList, r)
		} else {
			onOther = append(onOther, r)
		}
	}
	if len(onList) != models.ListUsePerList || onList[0].Total != 8 || onList[0].CampaignID != both || onList[0].ListsN != 2 {
		t.Fatalf("%+v", onList)
	}
	if onList[1].CampaignID != ids[6] {
		t.Fatalf("newest first: %+v", onList)
	}
	if len(onOther) != 1 || onOther[0].CampaignID != both || onOther[0].Total != 1 {
		t.Fatalf("%+v", onOther)
	}
}

func TestListCampaignsCountsCurrentMembership(t *testing.T) {
	e := newSendsEnv(t, 1) // Three memberships, even if some cannot receive a campaign.
	e.db.MustExec(`UPDATE campaigns SET status = 'finished', sent = 1 WHERE id = $1`, e.campID)
	e.db.MustExec(`REFRESH MATERIALIZED VIEW mat_list_subscriber_stats`)
	e.addSub("enabled", "confirmed")

	rows, err := e.core.GetListCampaigns([]int{e.listID}, true)
	if err != nil || len(rows) != 1 || rows[0].SubscriberCount == nil || *rows[0].SubscriberCount != 4 || rows[0].Sent != 1 {
		t.Fatalf("live count (historical sent unchanged): %+v, %v", rows, err)
	}
	rows, err = e.core.GetListCampaigns([]int{e.listID}, false)
	if err != nil || len(rows) != 1 || rows[0].SubscriberCount != nil {
		t.Fatalf("default history read should omit live counts: %+v, %v", rows, err)
	}
}

// The handler leaves out lists the user may not read, and hides the name from a user who
// may not read campaigns.
func TestGetListCampaignsPermissions(t *testing.T) {
	e := newSendsEnv(t, 3)
	e.db.MustExec(`DELETE FROM campaign_lists`)
	e.db.MustExec(`DELETE FROM campaigns`)
	a := &App{core: e.core, i18n: &i18n.I18n{}}
	mine := e.listID
	secret := e.pfList("single", "")
	for _, l := range []int{mine, secret} {
		id := e.pfCampaign("regular", "finished", l)
		e.db.MustExec(`UPDATE campaigns SET sent = 7, to_send = 7, started_at = now() WHERE id = $1`, id)
	}
	call := func(user auth.User, query string) (int, []models.ListCampaignUse) {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest("GET", "/api/lists/campaigns"+query, nil), rec)
		c.Set(auth.UserHTTPCtxKey, user)
		if err := a.GetListCampaigns(c); err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return he.Code, nil
			}
			t.Fatal(err)
		}
		var out struct {
			Data []models.ListCampaignUse `json:"data"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Data
	}
	q := "?list_id=" + itoa(mine) + "&list_id=" + itoa(secret)

	admin := auth.User{UserRoleID: auth.SuperAdminRoleID}
	if code, rows := call(admin, q); code != 200 || len(rows) != 2 || rows[0].Name == "" {
		t.Fatalf("%d %+v", code, rows)
	}

	// One list, and no campaign permission: the row is there, the name is not.
	importer := auth.User{
		PermissionsMap:     map[string]struct{}{auth.PermSubscribersImport: {}},
		ListPermissionsMap: map[int]map[string]struct{}{mine: {auth.PermListGet: {}}},
	}
	code, rows := call(importer, q+"&include_count=true")
	if code != 200 || len(rows) != 1 || rows[0].ListID != mine || rows[0].Name != "" || rows[0].CampaignID == 0 || rows[0].Sent != 7 ||
		rows[0].SubscriberCount == nil || *rows[0].SubscriberCount != 5 {
		t.Fatalf("%d %+v", code, rows)
	}
	// With the campaign permission the name shows.
	importer.PermissionsMap[auth.PermCampaignsGet] = struct{}{}
	if _, rows := call(importer, q); len(rows) != 1 || rows[0].Name == "" {
		t.Fatalf("%+v", rows)
	}
	// A user with no list access sees nothing; bad input is a 400.
	if code, rows := call(auth.User{}, q); code != 200 || len(rows) != 0 {
		t.Fatalf("%d %+v", code, rows)
	}
	for _, bad := range []string{"?list_id=0", "?list_id=abc", "?list_id=-2"} {
		if code, _ := call(admin, bad); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
}
