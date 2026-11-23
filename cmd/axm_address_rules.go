package main

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/labstack/echo/v4"
)

// Address rules (LST-53, step 1): an address that cannot receive mail is refused where a
// subscriber is added, so it never reaches a campaign. The rules need no network and no
// stored state: the mailbox name rules of the provider (Gmail) and a table of misspelled
// domains (gmail.con). DNS, the blocklist and bounce history come in a later step. Blocklisting
// takes any address. See FORK.md.

// addressRule checks an address and returns the 400 for one that breaks a rule (nil when it
// passes), with the rule and the suggested address. The address was sanitized by the caller.
func (a *App) addressRule(email string) (err error, rule, suggest string) {
	rule, suggest = addrcheck.Rules(email)
	switch rule {
	case addrcheck.ClassFormat:
		// A domain that breaks the label syntax (an underscore, a hyphen at the end) gets here.
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("subscribers.invalidEmail")), rule, ""
	case addrcheck.ClassProvider:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("subscribers.addr.provider")), rule, ""
	case addrcheck.ClassTypo:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("subscribers.addr.typo", "suggest", suggest)), rule, suggest
	}
	return nil, "", ""
}

// markAddressRule tells the form that a 400 is an address rule and belongs under the address
// field (X-Axm-Address-Rule), and sends the corrected address of a misspelled domain
// (X-Axm-Suggest) so that the form can offer a button without reading it out of the message.
// The suggestion is percent-encoded (headerEscape): a header is read as Latin-1, so a name
// such as 리뷰 or réview would come back to the browser as other characters.
func markAddressRule(c echo.Context, rule, suggest string) {
	if rule != addrcheck.ClassProvider && rule != addrcheck.ClassTypo {
		return
	}
	c.Response().Header().Set("X-Axm-Address-Rule", rule)
	if suggest != "" {
		c.Response().Header().Set("X-Axm-Suggest", headerEscape(suggest))
	}
}

// headerEscape percent-encodes the bytes of s that a header cannot carry as they are (UTF-8,
// controls) and the percent sign itself, so that decodeURIComponent gives s back. An ASCII
// address without a percent sign is sent unchanged.
func headerEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f || c == '%' {
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+0x100, 16)[1:]))
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// addressRuleError returns the 400 for an address that breaks a rule, or nil.
func (a *App) addressRuleError(c echo.Context, email string) error {
	err, rule, suggest := a.addressRule(email)
	if err != nil {
		markAddressRule(c, rule, suggest)
	}
	return err
}

// addressChangeError is addressRuleError for a save of the subscriber with the given id:
// an address the person did not change is not checked, so that an old subscriber can still
// be edited, and an error that has nothing to do with the address carries no address header.
// current is the address the subscriber has now, or "" to read it.
func (a *App) addressChangeError(c echo.Context, id int, email, current string) error {
	err, rule, suggest := a.addressRule(email)
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
	markAddressRule(c, rule, suggest)
	return err
}

// GetImportRejected sends the rows of the last import that the address rules refused, as
// CSV, so that a person can correct them and upload them again.
//
// The rows are the last import's, asked for by its number (?id=, from the import status),
// and only the user who started it, or one who reads every subscriber, gets them: they hold
// the addresses of the file.
func (a *App) GetImportRejected(c echo.Context) error {
	user := auth.GetUser(c)
	id, _ := strconv.Atoi(c.QueryParam("id"))
	rows, err := a.importer.GetRejected(id, user.ID, user.HasPerm(auth.PermSubscribersGetAll))
	switch err {
	case nil:
	case subimporter.ErrRejectedDenied:
		return echo.NewHTTPError(http.StatusForbidden, a.i18n.Ts("globals.messages.permissionDenied", "name", "import"))
	default:
		return echo.NewHTTPError(http.StatusGone, a.i18n.T("import.rejected.gone"))
	}

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
