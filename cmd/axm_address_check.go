package main

import (
	"math"
	"net/http"
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
}

type addrCheckListReq struct {
	ListIDs []int `json:"list_ids"`
}

type addrCheckListResp struct {
	addrcheck.Summary
	// TooLarge: the audience is over Limit, so nothing was checked.
	TooLarge bool `json:"too_large"`
	Limit    int  `json:"limit"`
	// Detail: the classes carry example addresses and subscriber IDs.
	Detail bool `json:"detail"`
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
	out := addrCheckImportResp{Summary: addrcheck.Summarize(verdicts, nil, false), Issues: map[int][]string{}}
	for i, v := range verdicts {
		classes := v.Classes()
		if v.Status == addrcheck.StatusPending {
			classes = append(classes, string(addrcheck.StatusPending))
		}
		if len(classes) > 0 {
			out.Issues[i] = classes
		}
	}
	return c.JSON(http.StatusOK, okResp{out})
}

// CheckListAddresses checks the addresses a campaign to the given lists would reach.
// Lists the user cannot see are ignored. Addresses and subscriber IDs are included only
// for a user who can read subscribers.
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
	lists := user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, req.ListIDs)
	out := addrCheckListResp{
		Limit:  addrCheckMaxAudience,
		Detail: user.HasPerm(auth.PermSubscribersGetAll) || user.HasPerm(auth.PermSubscribersGet),
	}
	if len(lists) == 0 {
		out.Summary = addrcheck.Summarize(nil, nil, false)
		return c.JSON(http.StatusOK, okResp{out})
	}

	total, rows, err := a.core.AddressCheckAudience(c.Request().Context(), lists, addrCheckMaxAudience)
	if err != nil {
		return err
	}
	if total > addrCheckMaxAudience {
		out.Summary = addrcheck.Summarize(nil, nil, false)
		out.Total, out.TooLarge = total, true
		return c.JSON(http.StatusOK, okResp{out})
	}

	emails, ids := make([]string, len(rows)), make([]int, len(rows))
	for i, r := range rows {
		emails[i], ids[i] = r.Email, r.ID
	}
	verdicts := a.addrCheck.Check(c.Request().Context(), emails, addrCheckBudget)
	out.Summary = addrcheck.Summarize(verdicts, ids, out.Detail)
	return c.JSON(http.StatusOK, okResp{out})
}
