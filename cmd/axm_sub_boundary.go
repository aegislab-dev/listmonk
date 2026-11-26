package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// The team boundary on subscribers (LST-42).
//
// A subscriber is one row for the whole company, and upstream lets a user write to it when it
// is on any one list the user may read. So a member of one team could rename, blocklist or
// delete a person who is on another team's list too. The rule here:
//
// A user who may not manage all lists (no lists:manage_all, not Super Admin) changes the
// subscriber itself (name, e-mail, attributes, status, blocklist, delete, bounce records) only
// when every subscription row of that subscriber, whatever its status, is on a list the user
// may manage. Any other subscriber is "locked" for that user. Reading stays as it was, and so
// does changing the subscriptions on the user's own lists, which reach nobody else.

// lockedIDsMax is how many subscriber IDs the "locked" error names.
const lockedIDsMax = 20

// lockedSubs returns the IDs among subIDs that are locked for the user, in order. It is empty
// for a user who may manage all lists.
func (a *App) lockedSubs(u auth.User, subIDs []int) ([]int, error) {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all || len(subIDs) == 0 {
		return nil, nil
	}
	m, err := a.core.LockedSubscribers(subIDs, manage)
	if err != nil {
		return nil, err
	}
	out := []int{}
	for id, locked := range m {
		if locked {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out, nil
}

// errSharedLocked is the 403 for a write to locked subscribers. It names their IDs.
func (a *App) errSharedLocked(ids []int) error {
	parts := []string{}
	for i, id := range ids {
		if i == lockedIDsMax {
			parts = append(parts, "+"+strconv.Itoa(len(ids)-lockedIDsMax))
			break
		}
		parts = append(parts, strconv.Itoa(id))
	}
	return echo.NewHTTPError(http.StatusForbidden, a.i18n.Ts("teams.sharedLocked", "ids", strings.Join(parts, ", ")))
}

// errBounceLocked is the 403 for deleting bounce records of locked subscribers by ID (LST-73).
func (a *App) errBounceLocked(ids []int) error {
	parts := []string{}
	for i, id := range ids {
		if i == lockedIDsMax {
			parts = append(parts, "+"+strconv.Itoa(len(ids)-lockedIDsMax))
			break
		}
		parts = append(parts, strconv.Itoa(id))
	}
	return echo.NewHTTPError(http.StatusForbidden, a.i18n.Ts("teams.bounceLocked", "ids", strings.Join(parts, ", ")))
}

// refuseLocked turns the locked IDs a bounded write gave back into the 403.
func (a *App) refuseLocked(locked []int, err error) error {
	if err != nil {
		return err
	}
	if len(locked) > 0 {
		return a.errSharedLocked(locked)
	}
	return nil
}

// deleteSubs deletes subscribers for the user. A user who may not manage all lists deletes
// them only when none is locked; the check and the delete are one transaction (core).
func (a *App) deleteSubs(u auth.User, ids []int) error {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		return a.core.DeleteSubscribers(ids, nil)
	}
	return a.refuseLocked(a.core.DeleteSubscribersBounded(ids, manage))
}

// blocklistSubs blocklists subscribers for the user, by the same rule as deleteSubs.
func (a *App) blocklistSubs(u auth.User, ids []int) error {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		return a.core.BlocklistSubscribers(ids)
	}
	return a.refuseLocked(a.core.BlocklistSubscribersBounded(ids, manage))
}

// deleteSubBounces deletes a subscriber's bounce records for the user, by the same rule.
func (a *App) deleteSubBounces(u auth.User, id int) error {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		return a.core.DeleteSubscriberBounces(id, "")
	}
	return a.refuseLocked(a.core.DeleteSubscriberBouncesBounded(id, manage))
}

// updateSub saves a subscriber (PUT, PATCH) for the user. For a user who may not manage all
// lists a locked subscriber keeps their profile: a save that changes it is refused, and a
// save that leaves it as it is (the edit form changing only the lists) changes the
// subscriptions on the user's own lists without writing the subscriber row.
func (a *App) updateSub(u auth.User, id int, sub models.Subscriber, listIDs []int, preconfirm, deleteLists bool) (models.Subscriber, error) {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		out, _, err := a.core.UpdateSubscriberWithLists(id, sub, listIDs, nil, preconfirm, deleteLists, false, []int{}, false)
		return out, err
	}
	out, locked, err := a.core.UpdateSubscriberBounded(id, sub, listIDs, preconfirm, deleteLists, ownListsOnly(false, manage), manage,
		func(cur models.Subscriber) bool { return profileChanged(profileOf(cur), sub) })
	return out, a.refuseLocked(locked, err)
}

// attachLocked marks the subscribers that are locked for the user (edit_locked), so that the
// screens can switch off what the server refuses. Users who manage all lists get no mark.
func (a *App) attachLocked(user auth.User, subs []models.Subscriber) {
	if len(subs) == 0 {
		return
	}
	ids := make([]int, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	locked, err := a.lockedSubs(user, ids)
	if err != nil {
		a.log.Printf("error reading locked subscribers: %v", err)
		return
	}
	is := map[int]bool{}
	for _, id := range locked {
		is[id] = true
	}
	for i := range subs {
		subs[i].EditLocked = is[subs[i].ID]
	}
}

// subProfile is the part of a subscriber that belongs to the subscriber itself.
type subProfile struct {
	Email, Name, Status string
	Attribs             models.JSON
}

// profileOf copies the profile of a subscriber. The attributes are copied through JSON so
// that a later change to the subscriber's map does not reach the copy.
func profileOf(s models.Subscriber) subProfile {
	p := subProfile{Email: s.Email, Name: s.Name, Status: s.Status, Attribs: models.JSON{}}
	if b, err := json.Marshal(s.Attribs); err == nil {
		_ = json.Unmarshal(b, &p.Attribs)
	}
	return p
}

// profileChanged tells whether saving req would change the profile cur, the way the update
// query reads the fields: an empty e-mail, name or status keeps the present value, and the
// attributes are always written (missing attributes are an empty object).
func profileChanged(cur subProfile, req models.Subscriber) bool {
	if req.Email != "" && !strings.EqualFold(strings.TrimSpace(req.Email), strings.TrimSpace(cur.Email)) {
		return true
	}
	if name := strings.TrimSpace(req.Name); name != "" && name != strings.TrimSpace(cur.Name) {
		return true
	}
	if req.Status != "" && req.Status != cur.Status {
		return true
	}
	return !sameJSON(cur.Attribs, req.Attribs)
}

// sameJSON compares two attribute maps by value (nil and empty are the same).
func sameJSON(a, b models.JSON) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	norm := func(m models.JSON) any {
		var out any
		b, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		_ = json.Unmarshal(b, &out)
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// ownListsOnly returns the lists whose subscriptions a subscriber save may remove, for the
// update query, where an empty array means "every list". A user who manages no list at all
// must not reach any subscription, so the array gets an ID no list has.
func ownListsOnly(allPerm bool, permitted []int) []int {
	if allPerm {
		return []int{}
	}
	if len(permitted) == 0 {
		return []int{-1}
	}
	return permitted
}

// guardBulkQuery prepares a bulk delete or blocklist by query for the user. It returns the
// condition to run and what the audit row says. For a user who may not manage all lists the
// condition leaves out the locked subscribers, the count is the number the action reaches
// without them, and detail.skipped_shared is how many were left out. cond is the user's own
// condition (already checked for subscribers:sql_query); the added part is not theirs.
func (a *App) guardBulkQuery(u auth.User, req subQueryReq, cond string, listIDs []int) (string, bulkQuery) {
	bq := a.countBulkQuery(req, cond, listIDs)
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		return cond, bq
	}
	guarded := core.AndSQLExp(cond, core.SharedSubscriberExclusion(manage))
	// Both numbers are counted now and exactly. With no condition at all the first count
	// above is the cached total, which may be older than the people it is compared with.
	exact := cond
	if exact == "" {
		exact = "TRUE"
	}
	total, err := a.core.CountSubscribersMatching(req.Search, exact, listIDs, req.SubscriptionStatus)
	n, err2 := a.core.CountSubscribersMatching(req.Search, guarded, listIDs, req.SubscriptionStatus)
	if err != nil || err2 != nil {
		a.log.Printf("error counting subscribers for the audit log: %v %v", err, err2)
		bq.counted = false
		return guarded, bq
	}
	skipped := total - n
	if skipped < 0 {
		// Somebody was added between the two counts.
		skipped = 0
	}
	bq.detail["skipped_shared"] = skipped
	bq.count, bq.counted = n, true
	return guarded, bq
}

// restrictImportOverwrite switches off "overwrite name and attributes" in an import by a user
// who may not manage all lists: an address that already exists may be a subscriber of another
// team. New people are still added with their name and attributes, and the subscription
// status on the user's own lists is still overwritten when that was asked for. The old
// "overwrite" flag means both, so it is taken apart (the importer would switch both on again).
func restrictImportOverwrite(u auth.User, opt *subimporter.SessionOpt) {
	if all, _ := u.GetPermittedLists(auth.PermTypeManage); all {
		return
	}
	if opt.Overwrite {
		opt.Overwrite = false
		opt.OverwriteSubStatus = true
	}
	opt.OverwriteUserInfo = false
}

// bulkByQuery runs a bulk delete or blocklist by query for the user. cond and bq come from
// guardBulkQuery. Whoever may manage all lists runs the upstream statement. Everybody else
// goes through core.BulkByQueryBounded, which locks the people and looks again at who is on
// another list before it changes them; the audit numbers then say what was done.
func (a *App) bulkByQuery(u auth.User, blocklist bool, req subQueryReq, cond string, listIDs []int, bq *bulkQuery) error {
	all, manage := u.GetPermittedLists(auth.PermTypeManage)
	if all {
		if blocklist {
			return a.core.BlocklistSubscribersByQuery(req.Search, cond, listIDs, req.SubscriptionStatus)
		}
		return a.core.DeleteSubscribersByQuery(req.Search, cond, listIDs, req.SubscriptionStatus)
	}
	done, late, err := a.core.BulkByQueryBounded(req.Search, cond, listIDs, req.SubscriptionStatus, manage, blocklist)
	if err != nil {
		return err
	}
	skipped, _ := bq.detail["skipped_shared"].(int)
	bq.detail["skipped_shared"] = skipped + late
	bq.detail["counted"] = "done"
	bq.count, bq.counted = done, true
	return nil
}

// bulkByQueryResp is the answer of a bulk delete or blocklist by query. Whoever may manage all
// lists gets true, as upstream answers (its statement does not say how many it changed). Everybody
// else went through core.BulkByQueryBounded, which knows how many it changed and how many it left
// out because they are on another team's list too, so the page can say so (0 included).
func bulkByQueryResp(u auth.User, bq bulkQuery) any {
	if all, _ := u.GetPermittedLists(auth.PermTypeManage); all {
		return true
	}
	skipped, _ := bq.detail["skipped_shared"].(int)
	return map[string]int{"done": bq.count, "skipped": skipped}
}
