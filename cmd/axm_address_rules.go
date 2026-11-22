package main

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/labstack/echo/v4"
)

// Address rules (LST-53, step 1): an address that cannot receive mail is refused where a
// subscriber is added, so it never reaches a campaign. The rules need no network and no
// stored state: the mailbox name rules of the provider (Gmail) and a table of misspelled
// domains (gmail.con). DNS, the blocklist and bounce history come in a later step. Blocklisting
// takes any address. See FORK.md.

// addressRuleError returns the 400 for an address that breaks a rule, or nil. A malformed
// address is refused by the sanitizing that runs before this.
func (a *App) addressRuleError(email string) error {
	reason, suggest := addrcheck.Rules(email)
	switch reason {
	case addrcheck.ClassProvider:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("subscribers.addr.provider"))
	case addrcheck.ClassTypo:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("subscribers.addr.typo", "suggest", suggest))
	}
	return nil
}

// addressChangeError is addressRuleError for a save of the subscriber with the given id:
// an address the person did not change is not checked, so that an old subscriber can still
// be edited. current is the address the subscriber has now, or "" to read it.
func (a *App) addressChangeError(id int, email, current string) error {
	err := a.addressRuleError(email)
	if err == nil {
		return nil
	}
	if current == "" {
		sub, gerr := a.core.GetSubscriber(id, "", "")
		if gerr != nil {
			return gerr
		}
		current = sub.Email
	}
	if strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(email)) {
		return nil
	}
	return err
}

// GetImportRejected sends the rows of the last import that the address rules refused, as
// CSV, so that a person can correct them and upload them again.
func (a *App) GetImportRejected(c echo.Context) error {
	rows := a.importer.GetRejected()

	c.Response().Header().Set("Content-Type", "text/csv; charset=utf-8")
	c.Response().Header().Set("Content-Disposition", `attachment; filename="rejected-rows.csv"`)
	c.Response().WriteHeader(http.StatusOK)
	// The byte order mark lets a spreadsheet read the Korean headers as UTF-8.
	_, _ = c.Response().Write([]byte("\xEF\xBB\xBF"))

	wr := csv.NewWriter(c.Response())
	_ = wr.Write([]string{
		a.i18n.T("import.rejected.csvRow"), a.i18n.T("import.rejected.csvEmail"),
		a.i18n.T("import.rejected.csvReason"), a.i18n.T("import.rejected.csvSuggest"),
	})
	for _, r := range rows {
		_ = wr.Write([]string{strconv.Itoa(r.Row), csvSafe(r.Email), a.rejectReason(r.Reason), csvSafe(r.Suggest)})
	}
	wr.Flush()
	return nil
}

// rejectReason is the words for a reason code of the importer.
func (a *App) rejectReason(code string) string {
	if code == subimporter.RejectDomain {
		return a.i18n.T("import.rejected.domain")
	}
	return a.i18n.T("addressCheck.class." + code)
}

// csvSafe keeps a spreadsheet from running a value of the uploaded file as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
