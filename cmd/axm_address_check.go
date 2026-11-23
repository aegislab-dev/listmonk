package main

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/labstack/echo/v4"
)

// Address check: shows the addresses that are likely to bounce (malformed, dead or
// null-MX domains, disposable and role addresses) before an import is uploaded and
// before a campaign is sent. It only reads and never blocks a send; people fix what it
// shows with the ordinary, audited subscriber APIs. See FORK.md.
const (
	// addrCheckMaxEmails is the most addresses one import check takes: the row limit of
	// the column mapping screen (frontend/src/lib/csv-import.js LIMITS.rows).
	addrCheckMaxEmails = 50000
	// addrCheckMaxLists is the most lists one check takes.
	addrCheckMaxLists = 1000
)

// Variables so that tests can lower them.
var (
	// addrCheckMaxAudience is the largest audience that is checked. A bigger one is
	// answered with too_large and no check.
	addrCheckMaxAudience = 500000
	// addrCheckBudget is how long one request waits for DNS. What is not ready by then
	// is reported as pending and looked up in the background.
	addrCheckBudget = 8 * time.Second
)

type addrCheckImportReq struct {
	Emails []string `json:"emails"`
}

type addrCheckImportResp struct {
	addrcheck.Summary
	// Issues maps the position of an address in the request to its classes. Addresses
	// with nothing to report are left out. "pending" marks a lookup that has not finished.
	Issues map[int][]string `json:"issues"`
	// Suggest maps the position of an address with a misspelled domain to the corrected
	// address. It is a suggestion only; the import refuses the row and does not change it.
	Suggest map[int]string `json:"suggest"`
	// Gate is how strict the server's address gate is (full, nodns, basic). The column mapping
	// leaves out the rows the server would refuse, so it needs to know: with basic a disposable
	// domain is only a warning.
	Gate string `json:"gate"`
}

type addrCheckListReq struct {
	ListIDs []int `json:"list_ids"`
	// Optin: check the audience of an opt-in campaign (the unconfirmed subscribers of the
	// double opt-in lists) instead of a regular one.
	Optin bool `json:"optin"`
}

type addrCheckListResp struct {
	addrcheck.Summary
	// TooLarge: the audience is over Limit, so nothing was checked.
	TooLarge bool `json:"too_large"`
	Limit    int  `json:"limit"`
	// Detail: the classes carry example addresses and subscriber IDs.
	Detail bool `json:"detail"`
	// Linked are the other lists that unsubscribing someone from the chosen ordinary lists
	// also reaches, because a draw ties them. LinkedHidden counts those the user cannot see.
	Linked       []string `json:"linked"`
	LinkedHidden int      `json:"linked_hidden"`
	// CheckedLists is how many of the asked lists the user may see and were checked. With
	// none, the zero counts say nothing about the lists.
	CheckedLists int `json:"checked_lists"`
	// UncheckedLists is how many of the asked lists were left out because the user cannot
	// see them. Their recipients are not in the counts, so the counts are not the whole
	// audience and must not be shown as "no risky addresses".
	UncheckedLists int `json:"unchecked_lists"`
	// Targets are the chosen ordinary lists the user may manage: where an unsubscribe from
	// this screen can change something. The direct marks and Linked are about these lists.
	Targets []int `json:"targets"`
}

// CheckImportAddresses checks the addresses of a file that is about to be imported.
// Nothing is stored.
func (a *App) CheckImportAddresses(c echo.Context) error {
	var req addrCheckImportReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("addressCheck.invalid"))
	}
	if len(req.Emails) > addrCheckMaxEmails {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("addressCheck.invalid"))
	}

	verdicts := a.addrCheck.Check(c.Request().Context(), req.Emails, addrCheckBudget)
	out := addrCheckImportResp{Summary: addrcheck.Summarize(verdicts, nil, nil, false), Issues: map[int][]string{}, Suggest: map[int]string{}, Gate: a.gateStrength()}
	// What the database knows (LST-56): the column mapping screen shows the rows the server
	// would refuse for these reasons, and the numbers of Summary are not changed by them.
	var facts map[string]addrcheck.Facts
	if a.gateStrength() != gateBasic {
		emails := make([]string, 0, len(verdicts))
		for _, v := range verdicts {
			if !v.Format {
				emails = append(emails, v.Email)
			}
		}
		var err error
		if facts, err = a.core.AddressFacts(c.Request().Context(), emails); err != nil {
			a.log.Printf("error reading the address facts: %v", err)
			facts = nil
		}
	}
	for i, v := range verdicts {
		classes := v.Classes()
		if f := facts[strings.ToLower(strings.TrimSpace(v.Email))]; !v.Format {
			if f.Suppressed {
				classes = append(classes, addrcheck.ClassSuppressed)
			}
			if f.Bounced {
				classes = append(classes, addrcheck.ClassBounced)
			}
		}
		if v.Status == addrcheck.StatusPending {
			classes = append(classes, string(addrcheck.StatusPending))
		}
		if len(classes) > 0 {
			out.Issues[i] = classes
		}
		if v.Typo {
			out.Suggest[i] = v.Suggest
		}
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// CheckListAddresses checks the addresses a campaign to the given lists would reach.
// Lists the user cannot see are ignored. Addresses and subscriber IDs are included only
// for a user who can read those subscribers: with a dynamic list among them that is the
// rule of its preview (read access to its base lists, and subscribers:get_all when the
// base is every list).
func (a *App) CheckListAddresses(c echo.Context) error {
	var req addrCheckListReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("addressCheck.invalid"))
	}
	if len(req.ListIDs) > addrCheckMaxLists {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("addressCheck.invalid"))
	}
	for _, id := range req.ListIDs {
		if id <= 0 || id > math.MaxInt32 {
			return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("globals.messages.invalidID"))
		}
	}

	user := auth.GetUser(c)
	asked := slices.Clone(req.ListIDs)
	slices.Sort(asked)
	asked = slices.Compact(asked)
	lists := slices.Clone(user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, asked))
	slices.Sort(lists)
	lists = slices.Compact(lists)
	out := addrCheckListResp{
		Limit:   addrCheckMaxAudience,
		Detail:  user.HasPerm(auth.PermSubscribersGetAll) || user.HasPerm(auth.PermSubscribersGet),
		Linked:  []string{},
		Targets: []int{},

		CheckedLists:   len(lists),
		UncheckedLists: len(asked) - len(lists),
	}
	if len(lists) == 0 {
		out.Summary = addrcheck.Summarize(nil, nil, nil, false)
		return c.JSON(http.StatusOK, okResp{out})
	}

	ctx := c.Request().Context()
	dynamic, err := a.core.AddressCheckDynamicFilters(ctx, lists)
	if err != nil {
		return err
	}
	ordinary := make([]int, 0, len(lists))
	for _, id := range lists {
		f, ok := dynamic[id]
		if !ok {
			ordinary = append(ordinary, id)
		} else if a.checkDynamicBasePerm(c, f) != nil || !canSampleDynamic(user, f) {
			out.Detail = false
		}
	}
	out.Targets = append(out.Targets, user.FilterListsByPerm(auth.PermTypeManage, ordinary)...)
	linked, err := a.core.AddressCheckLinked(ctx, out.Targets)
	if err != nil {
		return err
	}
	for _, l := range linked {
		if len(user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, []int{l.ID})) == 1 {
			out.Linked = append(out.Linked, l.Name)
		} else {
			out.LinkedHidden++
		}
	}

	total, rows, err := a.core.AddressCheckAudience(ctx, lists, out.Targets, addrCheckMaxAudience, req.Optin)
	if err != nil {
		return err
	}
	if total > addrCheckMaxAudience {
		out.Summary = addrcheck.Summarize(nil, nil, nil, false)
		out.Total, out.TooLarge = total, true
		return c.JSON(http.StatusOK, okResp{out})
	}

	emails, ids, direct := make([]string, len(rows)), make([]int, len(rows)), make([]bool, len(rows))
	for i, r := range rows {
		emails[i], ids[i], direct[i] = r.Email, r.ID, r.Direct
	}
	verdicts := a.addrCheck.Check(ctx, emails, addrCheckBudget)
	// The campaign and list panels show what they showed before LST-53: the address rules
	// (Gmail names, misspelled domains) are for the places a subscriber is added, and their
	// screens come in a later step.
	for i := range verdicts {
		verdicts[i].Provider, verdicts[i].Typo, verdicts[i].Suggest = false, false, ""
	}
	out.Summary = addrcheck.Summarize(verdicts, ids, direct, out.Detail)
	return c.JSON(http.StatusOK, okResp{out})
}
