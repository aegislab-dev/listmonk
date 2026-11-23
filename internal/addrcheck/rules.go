package addrcheck

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// RulesVersion is raised when provider.go or typo_domains.json changes, so an answer
// stored under older rules can be told from one made under the current rules.
const RulesVersion = 1

// Rule classes: addresses that are certain to bounce, found without the network.
const (
	ClassProvider = "provider" // breaks the mailbox name rules of the provider (Gmail)
	ClassTypo     = "typo"     // the domain is a known misspelling of a popular one
)

//go:embed typo_domains.json
var typoRaw []byte

var typoTable = func() struct {
	domains   map[string]string
	tldLabels map[string]struct{}
	tldTo     string
} {
	var f struct {
		Domains map[string]string `json:"domains"`
		TLD     struct {
			Labels []string `json:"labels"`
			To     string   `json:"to"`
		} `json:"tld"`
	}
	if err := json.Unmarshal(typoRaw, &f); err != nil {
		panic("addrcheck: typo_domains.json: " + err.Error())
	}
	out := struct {
		domains   map[string]string
		tldLabels map[string]struct{}
		tldTo     string
	}{domains: f.Domains, tldLabels: map[string]struct{}{}, tldTo: f.TLD.To}
	for _, l := range f.TLD.Labels {
		out.tldLabels[l] = struct{}{}
	}
	return out
}()

// Gmail mailbox names, from Google's "Create a username" help
// (https://support.google.com/mail/answer/9211434): letters, digits and periods, no
// period at the start or the end, no two periods in a row, 6 to 30 characters. The help
// does not say whether periods count towards the length, so only a name that is too
// short even with its periods or too long even without them is refused.
const (
	gmailMin = 6
	gmailMax = 30
)

// gmailDomains are the domains whose mailboxes follow the Gmail name rules. Google
// Workspace domains are not here: they set their own rules.
var gmailDomains = map[string]struct{}{"gmail.com": {}, "googlemail.com": {}}

// providerIssue reports whether the mailbox name cannot exist at the provider of the
// domain. The part after + is not part of the name.
func providerIssue(local, domain string) bool {
	if _, ok := gmailDomains[domain]; !ok {
		return false
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	if len(local) < gmailMin {
		return true
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return true
	}
	for i := 0; i < len(local); i++ {
		c := local[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.') {
			return true
		}
	}
	return len(strings.ReplaceAll(local, ".", "")) > gmailMax
}

// typoSuggestion returns the address with the domain corrected when the domain is a known
// misspelling, or "" when it is not.
func typoSuggestion(local, domain string) string {
	if fixed, ok := typoTable.domains[domain]; ok {
		return local + "@" + fixed
	}
	if i := strings.LastIndexByte(domain, '.'); i > 0 {
		if _, ok := typoTable.tldLabels[domain[i+1:]]; ok {
			return local + "@" + domain[:i+1] + typoTable.tldTo
		}
	}
	return ""
}

// Rules checks an address against the rules that need no network: the address format,
// the mailbox name rules of the provider and the table of misspelled domains. It returns
// the code of the first rule the address breaks ("" when none) and, for a misspelled
// domain, the address with the domain corrected.
func Rules(email string) (reason, suggest string) {
	r := Classify(email)
	switch {
	case r.Format:
		return ClassFormat, ""
	case r.Provider:
		return ClassProvider, ""
	case r.Typo:
		return ClassTypo, r.Suggest
	}
	return "", ""
}
