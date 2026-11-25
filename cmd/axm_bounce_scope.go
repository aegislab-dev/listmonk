package main

import (
	"net/http"
	"sort"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// The team boundary on bounce records (LST-68). A user who may read all lists (Super Admin,
// lists:get_all, lists:manage_all) gets the upstream behaviour. Everybody else sees the bounces
// of people who are on a list they may read, and deletes or blocklists only people they may
// change by the LST-42 rule (axm_sub_boundary.go). See core/axm_bounce_scope.go.

// bounceScope is what a user reaches in the bounce records.
type bounceScope struct {
	// all: the user reads all lists, the upstream code path runs.
	all bool
	// read: the lists the user may read (get or manage), manage: the lists they may manage.
	read, manage []int
	// allCampaigns: the user may read every campaign (campaigns:get_all).
	allCampaigns bool
}

func (a *App) bounceScopeOf(u auth.User) bounceScope {
	if all, _ := u.GetPermittedLists(auth.PermTypeGet | auth.PermTypeManage); all {
		return bounceScope{all: true}
	}
	_, get := u.GetPermittedLists(auth.PermTypeGet)
	_, manage := u.GetPermittedLists(auth.PermTypeManage)
	seen := map[int]bool{}
	read := []int{}
	for _, id := range append(append([]int{}, get...), manage...) {
		if !seen[id] {
			seen[id] = true
			read = append(read, id)
		}
	}
	sort.Ints(read)
	return bounceScope{read: read, manage: manage, allCampaigns: u.HasPerm(auth.PermCampaignsGetAll)}
}

// maskBounceCampaigns hides the campaigns of the bounces that the user may not read.
func (a *App) maskBounceCampaigns(sc bounceScope, bs []models.Bounce) error {
	if sc.all || sc.allCampaigns {
		return nil
	}
	return a.core.MaskBounceCampaigns(bs, sc.read)
}

func (a *App) getBounceScoped(c echo.Context, sc bounceScope, id int) error {
	out, err := a.core.GetBounceScoped(id, sc.read)
	if err != nil {
		return err
	}
	one := []models.Bounce{out}
	if err := a.maskBounceCampaigns(sc, one); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{one[0]})
}

func (a *App) getBouncesScoped(c echo.Context, sc bounceScope, campID int, source, orderBy, order string, offset, limit, page, perPage int) error {
	res, total, err := a.core.QueryBouncesScoped(0, campID, 0, source, orderBy, order, offset, limit, sc.read)
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return c.JSON(http.StatusOK, okResp{models.PageResults{Results: []models.Bounce{}}})
	}
	if err := a.maskBounceCampaigns(sc, res); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{models.PageResults{Results: res, Total: total, Page: page, PerPage: perPage}})
}

// bounceDenied is the 403 for bounces out of the user's reach: nothing was deleted.
func (a *App) bounceDenied() error {
	return echo.NewHTTPError(http.StatusForbidden, a.i18n.Ts("globals.messages.permissionDenied", "name", "bounces"))
}

// deleteBouncesScoped deletes bounces for a user who does not read all lists. With all, the
// answer says how many were deleted and how many were left (locked subscribers).
func (a *App) deleteBouncesScoped(c echo.Context, sc bounceScope, ids []int, all bool) error {
	if all {
		deleted, skipped, err := a.core.DeleteAllBouncesScoped(sc.read, sc.manage)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, okResp{map[string]int{"deleted": deleted, "skipped": skipped}})
	}
	outside, locked, err := a.core.DeleteBouncesScoped(ids, sc.read, sc.manage)
	if err != nil {
		return err
	}
	if outside > 0 {
		return a.bounceDenied()
	}
	if err := a.refuseLocked(locked, nil); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, okResp{true})
}

// blocklistBouncedScoped blocklists the bounced subscribers a user who does not read all lists
// manages, except the locked ones.
func (a *App) blocklistBouncedScoped(c echo.Context, sc bounceScope) error {
	done, skipped, err := a.core.BlocklistBouncedScoped(sc.manage)
	if err != nil {
		return err
	}
	a.audit(c, auditSubBlocklist, auditTarget{Type: auditTargetSubscriber}, done, map[string]any{"via": "bounces", "skipped_shared": skipped})
	return c.JSON(http.StatusOK, okResp{map[string]int{"blocklisted": done, "skipped": skipped}})
}
