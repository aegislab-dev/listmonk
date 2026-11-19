package addrcheck

import (
	"context"
	"time"
)

// The classes an address can fall into. One address can be in several.
const (
	ClassFormat     = "format"     // malformed
	ClassDead       = "dead"       // no such domain, or no mail server
	ClassNullMX     = "nullmx"     // the domain accepts no mail
	ClassDisposable = "disposable" // throwaway mailbox
	ClassRole       = "role"       // shared mailbox such as info@
	ClassUnknown    = "unknown"    // DNS did not answer
)

// Classes lists every class in the order the screens show them.
var Classes = []string{ClassFormat, ClassDead, ClassNullMX, ClassDisposable, ClassRole, ClassUnknown}

const (
	// MaxSamples is how many example addresses a class carries.
	MaxSamples = 20
	// MaxDetail is how many addresses of a class are listed in full.
	MaxDetail = 5000
)

// Verdict is one address with what DNS said about its domain.
type Verdict struct {
	Result
	Status DomainStatus
}

// Confirmed reports whether mail to the address is certain to bounce.
func (v Verdict) Confirmed() bool {
	return v.Format || v.Status == StatusDead || v.Status == StatusNullMX
}

// Warning reports whether the address is only a warning (disposable or role).
func (v Verdict) Warning() bool { return v.Disposable || v.Role }

// Classes returns the classes of the address, in the order of Classes.
func (v Verdict) Classes() []string {
	var out []string
	if v.Format {
		return append(out, ClassFormat)
	}
	switch v.Status {
	case StatusDead:
		out = append(out, ClassDead)
	case StatusNullMX:
		out = append(out, ClassNullMX)
	}
	if v.Disposable {
		out = append(out, ClassDisposable)
	}
	if v.Role {
		out = append(out, ClassRole)
	}
	if v.Status == StatusUnknown {
		out = append(out, ClassUnknown)
	}
	return out
}

// Check classifies the addresses and looks their domains up, waiting no longer than budget.
func (c *Checker) Check(ctx context.Context, emails []string, budget time.Duration) []Verdict {
	out := make([]Verdict, len(emails))
	seen := map[string]struct{}{}
	var domains []string
	for i, email := range emails {
		out[i].Result = Classify(email)
		d := out[i].Domain
		if d == "" {
			continue
		}
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			domains = append(domains, d)
		}
	}
	statuses := c.CheckDomains(ctx, domains, budget)
	for i := range out {
		if out[i].Domain != "" {
			out[i].Status = statuses[out[i].Domain]
		}
	}
	return out
}

// ClassSummary is the count of one class and, when detail is asked for, its addresses.
type ClassSummary struct {
	Count int `json:"count"`
	// Samples are the first MaxSamples addresses.
	Samples []string `json:"samples,omitempty"`
	// IDs and Emails are the first MaxDetail addresses, in the same order.
	IDs    []int    `json:"ids,omitempty"`
	Emails []string `json:"emails,omitempty"`
	// Truncated says the class has more addresses than IDs lists.
	Truncated bool `json:"truncated"`
}

// Summary counts a set of checked addresses.
type Summary struct {
	Total int `json:"total"`
	// Risky = Confirmed + Warning; an address in several classes counts once.
	Risky int `json:"risky"`
	// Confirmed: malformed, dead domain or null MX.
	Confirmed int `json:"confirmed"`
	// Warning: disposable or role, and not confirmed.
	Warning int `json:"warning"`
	// Unknown: DNS failed. Not part of Risky.
	Unknown int `json:"unknown"`
	// Pending: the lookup has not finished yet. Ask again.
	Pending int                      `json:"pending"`
	Classes map[string]*ClassSummary `json:"classes"`
}

// Summarize counts the verdicts by class. ids[i] is the subscriber of verdicts[i]; it can
// be nil. With detail, each class also carries example addresses and the listed IDs.
func Summarize(verdicts []Verdict, ids []int, detail bool) Summary {
	out := Summary{Total: len(verdicts), Classes: make(map[string]*ClassSummary, len(Classes))}
	for _, c := range Classes {
		out.Classes[c] = &ClassSummary{}
	}
	for i, v := range verdicts {
		switch {
		case v.Confirmed():
			out.Confirmed++
		case v.Warning():
			out.Warning++
		}
		switch v.Status {
		case StatusUnknown:
			out.Unknown++
		case StatusPending:
			out.Pending++
		}
		for _, c := range v.Classes() {
			cs := out.Classes[c]
			cs.Count++
			if !detail {
				continue
			}
			if len(cs.Samples) < MaxSamples {
				cs.Samples = append(cs.Samples, v.Email)
			}
			if ids == nil {
				continue
			}
			if len(cs.IDs) < MaxDetail {
				cs.IDs = append(cs.IDs, ids[i])
				cs.Emails = append(cs.Emails, v.Email)
			} else {
				cs.Truncated = true
			}
		}
	}
	out.Risky = out.Confirmed + out.Warning
	return out
}
