package addrcheck

import (
	_ "embed"
	"strings"
)

// The list is pinned to one commit of disposable-email-domains (CC0). Its source and
// commit are in the header of the file and in FORK.md.
//
//go:embed disposable_domains.txt
var disposableRaw string

var disposableDomains = func() map[string]struct{} {
	out := make(map[string]struct{}, 11000)
	for _, line := range strings.Split(disposableRaw, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" || line[0] == '#' {
			continue
		}
		out[line] = struct{}{}
	}
	return out
}()

// isDisposable reports whether the domain or one of its parent domains is on the list
// (so mail.example-temp.com matches example-temp.com).
func isDisposable(domain string) bool {
	for {
		if _, ok := disposableDomains[domain]; ok {
			return true
		}
		i := strings.IndexByte(domain, '.')
		if i < 0 {
			return false
		}
		domain = domain[i+1:]
		// A bare top-level label is never a list entry.
		if !strings.Contains(domain, ".") {
			return false
		}
	}
}

// ListedDisposable reports whether the domain itself is on the list. The gate (LST-56) refuses
// only these: a subdomain of a listed domain is classified disposable (isDisposable) for the
// review panels, but the list does not say that it is one.
func ListedDisposable(domain string) bool {
	_, ok := disposableDomains[strings.ToLower(strings.TrimSpace(domain))]
	return ok
}
