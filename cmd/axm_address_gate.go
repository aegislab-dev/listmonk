package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/labstack/echo/v4"
)

// The address gate (LST-56, step 2): where a subscriber is added or an address is changed, the
// server says whether the address may come in, with one table for every way in (addrcheck.Gate).
// Step 1 (LST-53) looked at the address alone: its form, the Gmail name rules and misspelled
// domains. This adds what the database knows (the address was blocklisted before, a hard
// bounce or a complaint is on record), the disposable-domain list, and DNS (a domain that does
// not exist or takes no mail). DNS that does not answer never refuses an address: it comes in
// with a warning. See FORK.md.

// How strict the gate is, from the setting app.axm_address_gate (LISTMONK_app__axm_address_gate).
const (
	gateFull  = "full"  // the whole table; the default
	gateNoDNS = "nodns" // without DNS: the browser tests and `axm check` use it, the sandbox has no DNS
	gateBasic = "basic" // the rules of step 1 only: a way back without rolling the image back
)

// gateOpt says how one call looks at its addresses.
type gateOpt struct {
	// Budget is how long to wait for DNS. What is not answered by then comes in with the
	// "unknown" warning, and the lookup goes on in the background for the next ask.
	Budget time.Duration
	// DBFacts: look at the blocklist and the bounce record. The public form does not, so that
	// nobody can find out from outside which addresses are blocklisted or bounced.
	DBFacts bool
	// RulesOnly: the rules of step 1 only (an address that is added as blocklisted may be anything).
	RulesOnly bool
}

// gateResult is the answer for one address. Reject is the class that refuses it ("" = in),
// Warn the class it comes in with ("role" or "unknown").
type gateResult struct{ Reject, Warn, Suggest string }

// gateStrength is the setting, with an empty or unknown value read as full.
func (a *App) gateStrength() string {
	switch s := strings.ToLower(strings.TrimSpace(a.addrGate)); s {
	case gateNoDNS, gateBasic:
		return s
	}
	return gateFull
}

// gateAddresses decides every address of a batch with one database query and one DNS wait.
// The addresses were sanitized by the caller.
func (a *App) gateAddresses(ctx context.Context, emails []string, opt gateOpt) []gateResult {
	out := make([]gateResult, len(emails))
	strength := a.gateStrength()
	if strength == gateBasic || opt.RulesOnly {
		for i, e := range emails {
			out[i].Reject, out[i].Suggest = addrcheck.Rules(e)
		}
		return out
	}

	verdicts := make([]addrcheck.Verdict, len(emails))
	var open []int // not refused by the address itself yet
	for i, e := range emails {
		verdicts[i].Result = addrcheck.Classify(e)
		if r, _ := addrcheck.Gate(verdicts[i], addrcheck.Facts{}, false); r == addrcheck.ClassFormat ||
			r == addrcheck.ClassProvider || r == addrcheck.ClassTypo {
			out[i].Reject, out[i].Suggest = r, verdicts[i].Suggest
			continue
		}
		open = append(open, i)
	}

	// The database facts first: a blocklisted or bounced address is not waited for in DNS.
	facts := map[string]addrcheck.Facts{}
	if opt.DBFacts && len(open) > 0 {
		list := make([]string, len(open))
		for k, i := range open {
			list[k] = verdicts[i].Email
		}
		var err error
		if facts, err = a.core.AddressFacts(ctx, list); err != nil {
			// A failing lookup must not stop people from adding addresses: the blocklist trigger
			// still holds a blocklisted address behind this gate.
			a.log.Printf("error reading the address facts: %v", err)
			facts = map[string]addrcheck.Facts{}
		}
	}
	factOf := func(i int) addrcheck.Facts {
		return facts[strings.ToLower(strings.TrimSpace(verdicts[i].Email))]
	}

	dns := strength == gateFull && a.addrCheck != nil
	var wait []int
	for _, i := range open {
		if r, _ := addrcheck.Gate(verdicts[i], factOf(i), false); r != "" {
			out[i].Reject = r
			continue
		}
		wait = append(wait, i)
	}
	if dns && len(wait) > 0 {
		list := make([]string, len(wait))
		for k, i := range wait {
			list[k] = verdicts[i].Email
		}
		for k, v := range a.addrCheck.Check(ctx, list, opt.Budget) {
			verdicts[wait[k]] = v
		}
	}
	for _, i := range wait {
		out[i].Reject, out[i].Warn = addrcheck.Gate(verdicts[i], factOf(i), dns)
	}
	return out
}

// addressGateError returns the 400 for an address the gate refuses, or nil. A refused address
// puts its class in the header X-Axm-Address-Rule (and the corrected address of a misspelled
// domain in X-Axm-Suggest) so that the form can show it under the address field. An address
// that comes in with a warning puts it in X-Axm-Address-Warn (the body is not changed). The
// address was sanitized by the caller.
func (a *App) addressGateError(c echo.Context, email string, opt gateOpt) error {
	res := a.gateAddresses(c.Request().Context(), []string{email}, opt)[0]
	if res.Reject == "" {
		if res.Warn != "" {
			c.Response().Header().Set("X-Axm-Address-Warn", res.Warn)
		}
		return nil
	}
	markAddressRule(c, res.Reject, res.Suggest)
	return a.addressRejectError(res)
}

// addressRejectError is the 400 for a refused address, in the words of the class.
func (a *App) addressRejectError(res gateResult) error {
	switch res.Reject {
	case addrcheck.ClassFormat:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("subscribers.invalidEmail"))
	case addrcheck.ClassTypo:
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.Ts("subscribers.addr.typo", "suggest", res.Suggest))
	}
	return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("subscribers.addr."+res.Reject))
}

// addressChangeGateError is addressGateError for a save of the subscriber with the given id:
// an address the person did not change is not looked at (nor looked up in DNS), so that an old
// subscriber can still be edited. current is the address the subscriber has now, or "" to read it.
func (a *App) addressChangeGateError(c echo.Context, id int, email, current string, opt gateOpt) error {
	if current == "" {
		sub, err := a.core.GetSubscriber(id, "", "")
		if err != nil {
			return err
		}
		current = sub.Email
	}
	if strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(email)) {
		return nil
	}
	return a.addressGateError(c, email, opt)
}

// importVerify is the gate for the importer: it decides a batch of rows of a file (the
// importer asks for 500 at a time).
func (a *App) importVerify(emails []string) []subimporter.AxmVerdict {
	res := a.gateAddresses(context.Background(), emails, gateOpt{Budget: importGateBudget, DBFacts: true})
	out := make([]subimporter.AxmVerdict, len(res))
	for i, r := range res {
		out[i] = subimporter.AxmVerdict{Reject: r.Reject, Warn: r.Warn, Suggest: r.Suggest}
	}
	return out
}

// importGateBudget is how long a batch of an import waits for DNS.
const importGateBudget = 10 * time.Second

// singleGateBudget and publicGateBudget are how long adding or editing one subscriber, and the
// public form, wait for DNS.
const (
	singleGateBudget = 4 * time.Second
	publicGateBudget = 3 * time.Second
)
