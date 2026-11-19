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

// hasSubWritePerm checks that the user may change the subscribers themselves: they must be
// readable (hasSubPerm) and none of them locked. One locked subscriber refuses the whole
// request, the way one unreadable subscriber does.
func (a *App) hasSubWritePerm(u auth.User, subIDs []int) error {
	if err := a.hasSubPerm(u, subIDs); err != nil {
		return err
	}
	locked, err := a.lockedSubs(u, subIDs)
	if err != nil {
		return err
	}
	if len(locked) > 0 {
		return a.errSharedLocked(locked)
	}
	return nil
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

// keepLockedProfile guards a subscriber save (PUT, PATCH) by a user for whom the subscriber is
// locked. A save that changes the profile is refused. A save that leaves it as it is (the
// edit form changing only the lists) goes on, with the present values put in the request so
// that the row is written back unchanged. before is the profile read ahead of the request
// (PATCH); nil reads it now.
func (a *App) keepLockedProfile(u auth.User, id int, req *models.Subscriber, before *subProfile) error {
	locked, err := a.lockedSubs(u, []int{id})
	if err != nil {
		return err
	}
	if len(locked) == 0 {
		return nil
	}
	if before == nil {
		cur, err := a.core.GetSubscriber(id, "", "")
		if err != nil {
			return err
		}
		p := profileOf(cur)
		before = &p
	}
	if profileChanged(*before, *req) {
		return a.errSharedLocked(locked)
	}
	req.Email, req.Name, req.Status, req.Attribs = before.Email, before.Name, before.Status, before.Attribs
	return nil
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
	n, err := a.core.CountSubscribersMatching(req.Search, guarded, listIDs, req.SubscriptionStatus)
	if err != nil || !bq.counted {
		if err != nil {
			a.log.Printf("error counting subscribers for the audit log: %v", err)
		}
		bq.counted = false
		return guarded, bq
	}
	bq.detail["skipped_shared"] = bq.count - n
	bq.count = n
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
