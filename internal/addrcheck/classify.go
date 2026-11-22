// Package addrcheck finds e-mail addresses that are likely to bounce before a campaign
// is sent: malformed addresses, domains that cannot receive mail (looked up in DNS and
// cached in memory), disposable domains and role addresses such as info@.
//
// Nothing here runs in the send loop or changes what is accepted as an address. The
// screens ask for a check, and the answer is only shown to a person.
package addrcheck

import (
	"strings"

	"github.com/knadh/listmonk/internal/utils"
	"golang.org/x/net/idna"
)

// Result is what can be said about one address without the network.
type Result struct {
	// Email is the cleaned address, or the trimmed input when it is malformed.
	Email string
	// Domain is the lowercase ASCII (punycode) domain. Empty when Format is set.
	Domain     string
	Format     bool // malformed: listmonk would refuse it
	Role       bool // a shared mailbox name such as info@ or support@
	Disposable bool // a throwaway mailbox provider
	// Literal: the domain is an address literal such as [192.0.2.17] (RFC 5321 §4.1.3).
	// Mail to it goes to that host without DNS, so DNS says nothing about it.
	Literal bool
	// Provider: the mailbox name breaks the rules of the provider, e.g. a Gmail name with
	// an underscore or under 6 characters. No such mailbox can exist.
	Provider bool
	// Typo: the domain is a known misspelling such as gmail.con. Suggest is the address
	// with the domain corrected. It is a suggestion only: it can be somebody else's mailbox.
	Typo    bool
	Suggest string
}

// Classify cleans an address the way listmonk does when it stores one and reports
// whether it is malformed, breaks a provider rule, has a misspelled domain, is a role
// address or is on a disposable domain.
func Classify(email string) Result {
	clean, err := utils.SanitizeEmail(email)
	if err != nil {
		return Result{Email: strings.TrimSpace(email), Format: true}
	}
	at := strings.LastIndexByte(clean, '@')
	local, host := clean[:at], clean[at+1:]
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return Result{Email: clean, Domain: strings.ToLower(host), Role: isRole(local), Literal: true}
	}
	domain := asciiDomain(host)
	r := Result{
		Email:      clean,
		Domain:     domain,
		Role:       isRole(local),
		Disposable: isDisposable(domain),
		Provider:   providerIssue(local, domain),
	}
	if s := typoSuggestion(local, domain); s != "" {
		r.Typo, r.Suggest = true, s
	}
	return r
}

// asciiDomain turns an internationalised domain into the name that is looked up in DNS.
// A name that cannot be converted is kept as it is, and the lookup decides.
func asciiDomain(domain string) string {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	if ascii, err := idna.Lookup.ToASCII(domain); err == nil && ascii != "" {
		return ascii
	}
	return domain
}
