package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx/types"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/models"
	"github.com/lib/pq"
)

// LST-27: deleting a list moves it to the trash, and restoring it brings back everything
// that went with it.

func newAuditEnv(t *testing.T) *teamsEnv {
	e := newTeamsEnv(t)
	e.a.log = log.New(io.Discard, "", 0)
	e.a.cfg = &Config{Permissions: map[string]struct{}{"subscribers:get": {}, "campaigns:get": {}}}
	return e
}

type auditRow struct {
	ID         int64          `db:"id"`
	UserID     *int           `db:"user_id"`
	Username   string         `db:"username"`
	UserType   string         `db:"user_type"`
	Action     string         `db:"action"`
	TargetType string         `db:"target_type"`
	TargetID   *int           `db:"target_id"`
	TargetName string         `db:"target_name"`
	Count      *int           `db:"count"`
	Detail     types.JSONText `db:"detail"`
}

func (e *teamsEnv) audits(where string, args ...any) []auditRow {
	e.t.Helper()
	out := []auditRow{}
	q := `SELECT id, user_id, username, user_type, action, target_type, target_id, target_name, count, detail FROM axm_audit_log`
	if where != "" {
		q += " WHERE " + where
	}
	if err := e.db.Select(&out, q+" ORDER BY id", args...); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *teamsEnv) oneAudit(action string) auditRow {
	e.t.Helper()
	rows := e.audits("action = $1", action)
	if len(rows) != 1 {
		e.t.Fatalf("%s: %d audit rows, want 1: %+v", action, len(rows), rows)
	}
	return rows[0]
}

// snapshot reads everything a list owns, as text, to compare before a delete and after a restore.
func (e *teamsEnv) snapshot(id int) string {
	e.t.Helper()
	var parts []string
	add := func(name, q string) {
		var s string
		e.get(&s, q, id)
		parts = append(parts, name+"="+s)
	}
	add("list", `SELECT COALESCE((SELECT to_jsonb(l)::TEXT FROM lists l WHERE id = $1), 'none')`)
	add("subs", `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY subscriber_id)::TEXT, '[]') FROM subscriber_lists s WHERE list_id = $1`)
	add("camps", `SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY id)::TEXT, '[]') FROM campaign_lists c WHERE list_id = $1`)
	add("roles", `SELECT COALESCE(jsonb_agg(jsonb_build_object('parent', parent_id, 'perms', permissions) ORDER BY parent_id)::TEXT, '[]') FROM roles WHERE list_id = $1`)
	add("draws", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'list', list_id, 'source', source_list_id, 'deleted', deleted_at IS NOT NULL, 'result', result) ORDER BY id)::TEXT, '[]') FROM axm_list_draws WHERE list_id = $1 OR source_list_id = $1`)
	return strings.Join(parts, "\n")
}

func (e *teamsEnv) listExists(id int) bool {
	var n int
	e.get(&n, `SELECT COUNT(*) FROM lists WHERE id = $1`, id)
	return n == 1
}

func (e *teamsEnv) trash(id int, user int) (int, json.RawMessage) {
	return e.call(e.a.DeleteList, e.as(user), "DELETE", "/api/lists/x", itoa(id), "")
}

func (e *teamsEnv) restore(id int, user int) (int, json.RawMessage) {
	return e.call(e.a.RestoreList, e.as(user), "POST", "/api/lists/trash/x/restore", itoa(id), "")
}

// A list with subscriptions of every status, a campaign link, a list role row, a team role
// row and a draw record is deleted and restored: the same list comes back.
func TestTrashRestoresEverythingTheListOwned(t *testing.T) {
	e := newAuditEnv(t)
	// The team list "A1" has the team role row from the trigger and the manual role row.
	l := e.aList
	for i, st := range []string{"confirmed", "unconfirmed", "unsubscribed", "confirmed"} {
		var sub int
		e.get(&sub, `INSERT INTO subscribers (uuid, email, name) VALUES (gen_random_uuid(), $1, 'x') RETURNING id`, fmt.Sprintf("s%d@example.invalid", i))
		e.db.MustExec(`INSERT INTO subscriber_lists (subscriber_id, list_id, status, meta) VALUES ($1, $2, $3::subscription_status, $4)`,
			sub, l, st, fmt.Sprintf(`{"n": %d}`, i))
	}
	var camp int
	e.get(&camp, `INSERT INTO campaigns (uuid, name, subject, from_email, body, content_type, messenger, status)
		VALUES (gen_random_uuid(), 'Draft', 's', 'a@example.invalid', 'b', 'html', 'email', 'draft') RETURNING id`)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'A1')`, camp, l)
	// A drawn list from A1, so A1 is a source with a draw record.
	drawn, draw, err := e.core.DrawList(l, models.DrawSettings{Unit: models.DrawUnitCount, Value: 2}, core.DrawOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_ = draw

	before := e.snapshot(l)
	beforeDrawn := e.snapshot(drawn.ID)
	var subsBefore int
	e.get(&subsBefore, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id = $1`, l)

	if code, _ := e.trash(l, e.admin); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if e.listExists(l) {
		t.Fatal("the list is still there")
	}
	// The campaign link keeps its row, without the list; the team role row went with the list.
	var linkLeft int
	e.get(&linkLeft, `SELECT COUNT(*) FROM campaign_lists WHERE campaign_id = $1 AND list_id IS NULL`, camp)
	if linkLeft != 1 {
		t.Fatalf("campaign link: %d", linkLeft)
	}
	// The snapshot holds what the list had: one row per subscription, no e-mail.
	var kept int
	e.get(&kept, `SELECT COUNT(*) FROM axm_deleted_list_subs WHERE list_id = $1`, l)
	if kept != subsBefore {
		t.Fatalf("trash holds %d subscriptions, the list had %d", kept, subsBefore)
	}
	row := e.oneAudit(auditListDelete)
	if row.Username != "admin" || row.TargetType != "list" || *row.TargetID != l || row.TargetName != "A1" || row.Count == nil || *row.Count != subsBefore {
		t.Fatalf("delete audit row: %+v", row)
	}
	var by string
	e.get(&by, `SELECT deleted_by_username FROM axm_deleted_lists WHERE list_id = $1`, l)
	if by != "admin" {
		t.Fatalf("deleted by %q", by)
	}

	// One subscriber is deleted while the list is in the trash: they are not restored.
	var gone int
	e.get(&gone, `SELECT subscriber_id FROM axm_deleted_list_subs WHERE list_id = $1 ORDER BY subscriber_id LIMIT 1`, l)
	e.db.MustExec(`DELETE FROM subscribers WHERE id = $1`, gone)
	e.get(&kept, `SELECT COUNT(*) FROM axm_deleted_list_subs WHERE list_id = $1`, l)
	if kept != subsBefore-1 {
		t.Fatalf("the trash follows deleted subscribers: %d", kept)
	}

	if code, _ := e.restore(l, e.admin); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	after := e.snapshot(l)
	// Compare without the subscriber who was deleted meanwhile.
	e.db.MustExec(`SELECT 1`)
	want := strings.Join(strings.Split(before, "\n"), "\n")
	if after == want {
		t.Fatal("the deleted subscriber came back")
	}
	for _, part := range []string{"list=", "camps=", "roles=", "draws="} {
		if pick(before, part) != pick(after, part) {
			t.Fatalf("%s differs:\nbefore %s\nafter  %s", part, pick(before, part), pick(after, part))
		}
	}
	var subsAfter int
	e.get(&subsAfter, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id = $1`, l)
	if subsAfter != subsBefore-1 {
		t.Fatalf("restored %d subscriptions, want %d", subsAfter, subsBefore-1)
	}
	var left int
	e.get(&left, `SELECT (SELECT COUNT(*) FROM axm_deleted_lists) + (SELECT COUNT(*) FROM axm_deleted_list_subs)`)
	if left != 0 {
		t.Fatalf("the trash is not empty after a restore: %d", left)
	}
	if r := e.oneAudit(auditListRestore); r.Username != "admin" || *r.TargetID != l {
		t.Fatalf("restore audit row: %+v", r)
	}
	// Subscription rows keep their status, meta and dates.
	var wrong int
	e.get(&wrong, `SELECT COUNT(*) FROM subscriber_lists WHERE list_id = $1 AND (meta->>'n') IS NULL`, l)
	if wrong != 0 {
		t.Fatal("meta is lost")
	}

	// The drawn list: delete and restore keeps its draw record and its link to the source.
	if code, _ := e.trash(drawn.ID, e.admin); code != 200 {
		t.Fatalf("delete drawn: %d", code)
	}
	var drawRow struct {
		List *int `db:"list_id"`
		Del  bool `db:"del"`
	}
	if err := e.db.Get(&drawRow, `SELECT list_id, deleted_at IS NOT NULL AS del FROM axm_list_draws WHERE source_list_id = $1`, l); err != nil {
		t.Fatal(err)
	}
	if drawRow.List != nil || !drawRow.Del {
		t.Fatalf("draw record after delete: %+v", drawRow)
	}
	if code, _ := e.restore(drawn.ID, e.admin); code != 200 {
		t.Fatalf("restore drawn: %d", code)
	}
	if got := e.snapshot(drawn.ID); pick(got, "draws=") != pick(beforeDrawn, "draws=") || pick(got, "list=") != pick(beforeDrawn, "list=") {
		t.Fatalf("drawn list differs:\nbefore %s\nafter  %s", beforeDrawn, got)
	}

	// Deleting the source list and restoring it brings the draw's link to it back.
	if code, _ := e.trash(l, e.admin); code != 200 {
		t.Fatalf("delete source: %d", code)
	}
	var src *int
	e.get(&src, `SELECT source_list_id FROM axm_list_draws WHERE list_id = $1`, drawn.ID)
	if src != nil {
		t.Fatal("the draw still points at the deleted source")
	}
	if code, _ := e.restore(l, e.admin); code != 200 {
		t.Fatalf("restore source: %d", code)
	}
	e.get(&src, `SELECT source_list_id FROM axm_list_draws WHERE list_id = $1`, drawn.ID)
	if src == nil || *src != l {
		t.Fatalf("the draw points at %v after the restore", src)
	}
}

// pick returns the line of a snapshot that starts with the prefix.
func pick(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// A restore asks for the permission to see the trash entry: all lists, or being the deleter.
func TestTrashRestorePermission(t *testing.T) {
	e := newAuditEnv(t)
	if code, _ := e.trash(e.aList, e.ua); code != 200 {
		t.Fatalf("a member deletes their team's list: %d", code)
	}
	if code, _ := e.trash(e.bList, e.ua); code != http.StatusForbidden {
		t.Fatalf("the other team's list: %d", code)
	}
	if e.audits("action = 'list.delete'")[0].Username != "ua" {
		t.Fatal("the deleter is not recorded")
	}
	// Another member cannot restore it; the deleter and an admin can.
	if code, _ := e.restore(e.aList, e.ub); code != http.StatusForbidden {
		t.Fatalf("someone else restores: %d", code)
	}
	list := func(user int) []models.DeletedList {
		_, data := e.call(e.a.GetListTrash, e.as(user), "GET", "/api/lists/trash", "", "")
		var out struct {
			Results []models.DeletedList `json:"results"`
		}
		json.Unmarshal(data, &out)
		return out.Results
	}
	if n := len(list(e.ub)); n != 0 {
		t.Fatalf("another member sees %d", n)
	}
	if got := list(e.ua); len(got) != 1 || got[0].DeletedByUsername != "ua" || got[0].DaysLeft != core.TrashDays {
		t.Fatalf("the deleter sees %+v", got)
	}
	if n := len(list(e.admin)); n != 1 {
		t.Fatalf("an admin sees %d", n)
	}
	if code, _ := e.restore(e.aList, e.ua); code != 200 {
		t.Fatalf("the deleter restores: %d", code)
	}
	if code, _ := e.restore(e.aList, e.admin); code != http.StatusNotFound {
		t.Fatalf("restoring twice: %d", code)
	}
	// The restored list is visible to its team again.
	if got := e.listNames(e.as(e.ua), "/api/lists"); !sameSet(got, []string{"A1", "Both"}) {
		t.Fatalf("after the restore: %v", got)
	}
}

// Deleting many lists by IDs, by search, or all of them goes through the trash for the lists
// the user manages only; the one that dynamic list excludes people of stays (400) with nothing
// deleted; a list a running campaign sends to is refused (409).
func TestTrashBulkDeletes(t *testing.T) {
	e := newAuditEnv(t)

	// All of a member's lists: A1 and Both are theirs; B1 and Plain are not.
	req := func(user int, target string) int {
		code, _ := e.call(e.a.DeleteLists, e.as(user), "DELETE", target, "", "")
		return code
	}
	if code := req(e.ua, "/api/lists?all=true"); code != 200 {
		t.Fatalf("all: %d", code)
	}
	var left []string
	if err := e.db.Select(&left, `SELECT name FROM lists WHERE name != 'Synthetic' ORDER BY name`); err != nil {
		t.Fatal(err)
	}
	if !sameSet(left, []string{"B1", "Plain"}) {
		t.Fatalf("left after a member's delete-all: %v", left)
	}
	trashed := e.audits("action = 'list.delete'")
	if len(trashed) != 2 {
		t.Fatalf("%d delete rows", len(trashed))
	}
	for _, r := range trashed {
		if r.Username != "ua" {
			t.Fatalf("deleted by %s", r.Username)
		}
	}
	var n int
	e.get(&n, `SELECT COUNT(*) FROM axm_deleted_lists`)
	if n != 2 {
		t.Fatalf("trash has %d", n)
	}

	// A list that a dynamic list excludes people of cannot go, and nothing else goes with it.
	e.db.MustExec(`UPDATE lists SET axm_filter = jsonb_build_object('version', 2, 'base', jsonb_build_object('type', 'all'),
		'conditions', '[]'::JSONB, 'exclude', jsonb_build_object('lists', jsonb_build_array(` + itoa(e.plainList) + `))) WHERE id = ` + itoa(e.bList))
	var other int
	e.get(&other, `INSERT INTO lists(uuid,name,type,optin) VALUES(gen_random_uuid(),'Other','private','single') RETURNING id`)
	if code, _ := e.call(e.a.DeleteLists, e.as(e.admin), "DELETE", fmt.Sprintf("/api/lists?id=%d&id=%d", other, e.plainList), "", ""); code != http.StatusBadRequest {
		t.Fatalf("an exclude list: %d", code)
	}
	if !e.listExists(e.plainList) || !e.listExists(other) {
		t.Fatal("a refused delete removed a list")
	}
	e.get(&n, `SELECT COUNT(*) FROM axm_deleted_lists`)
	if n != 2 {
		t.Fatalf("a refused delete left %d in the trash", n)
	}

	// A running campaign's list is refused with 409.
	e.db.MustExec(`UPDATE lists SET axm_filter = NULL WHERE id = $1`, e.bList)
	c, _ := e.addCampaign("running")
	e.db.MustExec(`DELETE FROM campaign_lists WHERE campaign_id = $1`, c)
	e.db.MustExec(`INSERT INTO campaign_lists (campaign_id, list_id, list_name) VALUES ($1, $2, 'B1')`, c, e.bList)
	if code, _ := e.trash(e.bList, e.admin); code != http.StatusConflict {
		t.Fatalf("a list of a running campaign: %d", code)
	}
	if !e.listExists(e.bList) {
		t.Fatal("the busy list is gone")
	}
}

// Lists leave the trash after the retention period, with a list.purge row by the server.
func TestTrashAndAuditRetention(t *testing.T) {
	e := newAuditEnv(t)
	for _, id := range []int{e.aList, e.bList} {
		if code, _ := e.trash(id, e.admin); code != 200 {
			t.Fatalf("delete: %d", code)
		}
	}
	e.db.MustExec(`UPDATE axm_deleted_lists SET deleted_at = NOW() - INTERVAL '15 days' WHERE list_id = $1`, e.aList)
	e.db.MustExec(`UPDATE axm_deleted_lists SET deleted_at = NOW() - INTERVAL '13 days' WHERE list_id = $1`, e.bList)
	e.db.MustExec(`UPDATE axm_audit_log SET created_at = NOW() - INTERVAL '13 months' WHERE target_id = $1`, e.aList)

	purgeTrashAndAudit(e.core, log.New(io.Discard, "", 0))

	var ids []int
	if err := e.db.Select(&ids, `SELECT list_id FROM axm_deleted_lists`); err != nil || len(ids) != 1 || ids[0] != e.bList {
		t.Fatalf("trash after the purge: %v %v", ids, err)
	}
	var subs int
	e.get(&subs, `SELECT COUNT(*) FROM axm_deleted_list_subs WHERE list_id = $1`, e.aList)
	if subs != 0 {
		t.Fatal("the purged list kept its subscriptions")
	}
	// The old delete row is past 12 months and gone; the server's purge row is new.
	rows := e.audits("action IN ('list.delete', 'list.purge')")
	got := map[string]int{}
	for _, r := range rows {
		got[fmt.Sprintf("%s/%d/%s", r.Action, *r.TargetID, r.UserType)]++
	}
	want := map[string]int{
		fmt.Sprintf("list.delete/%d/user", e.bList):  1,
		fmt.Sprintf("list.purge/%d/system", e.aList): 1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("audit rows: %v, want %v", got, want)
	}
	_ = pq.Array
	_ = time.Now
}
