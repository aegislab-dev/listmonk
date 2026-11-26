package main

import (
	"github.com/knadh/listmonk/internal/auth"
)

// The team boundary on bounce records (LST-73). Reading bounces stays company-wide. A user who
// may not manage all lists (no lists:manage_all, not Super Admin) deletes or blocks only the
// bounces of subscribers that are not locked for them (see axm_sub_boundary.go): everybody whose
// every subscription is on a list the user may manage. A bounce of somebody who is on another
// team's list too is that team's to clear, because deleting it frees the address a hard bounce
// kept out of imports, and "block everyone who bounced" blocklists the person.

// bounceResult is what a bounce delete or "block all" answers a user who may not manage all
// lists: how many were done and how many were left out as locked. Whoever may manage all lists
// gets true, as upstream answers.
type bounceResult struct {
	Done    int `json:"done"`
	Skipped int `json:"skipped"`
}

// deleteBounces deletes bounce records for the user. By ID the request is all or nothing: a bounce of
// a locked subscriber refuses the whole request (403, the subscriber IDs). By filter ("all") the
// bounces of locked subscribers are left out and counted.
func (a *App) deleteBounces(u auth.User, ids []int, all bool, campID int, source string) (any, error) {
	allLists, manage := u.GetPermittedLists(auth.PermTypeManage)
	if allLists {
		if err := a.core.DeleteBouncesFiltered(ids, all, campID, source); err != nil {
			return nil, err
		}
		return true, nil
	}
	done, skipped, locked, err := a.core.DeleteBouncesBounded(ids, all, campID, source, manage)
	if err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		return nil, a.errBounceLocked(locked)
	}
	return bounceResult{Done: done, Skipped: skipped}, nil
}
