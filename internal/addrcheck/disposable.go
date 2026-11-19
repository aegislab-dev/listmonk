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
