package addrcheck

import (
	"reflect"
	"strings"
	"testing"
)

// The wrong addresses seen in the production bounce review of 2026-11-21 (LST-53).
func TestProviderRules(t *testing.T) {
	bad := []string{
		"abc@gmail.com",                        // 3 characters
		"abcde@gmail.com",                      // 5 characters
		"ab.c@googlemail.com",                  // 4 even with the period
		"john_smith77@gmail.com",               // underscore
		"john-smith77@gmail.com",               // dash
		"-johnsmith@gmail.com",                 // dash first
		"_johnsmith@gmail.com",                 // underscore first
		"abc+tag@gmail.com",                    // the tag is not part of the name
		"john'smith@gmail.com",                 // apostrophe
		strings.Repeat("a", 31) + "@gmail.com", // 31 letters
	}
	for _, e := range bad {
		if r := Classify(e); r.Format || !r.Provider {
			t.Errorf("%s: want a provider issue, got %+v", e, r)
		}
	}

	// A period first or last, or two in a row, is already a malformed address everywhere.
	for _, e := range []string{".johnsmith@gmail.com", "johnsmith.@gmail.com", "john..smith@gmail.com"} {
		if r := Classify(e); !r.Format {
			t.Errorf("%s: want format, got %+v", e, r)
		}
	}
	// providerIssue itself also refuses them, should the address check ever let them through.
	for _, l := range []string{".johnsmith", "johnsmith.", "john..smith"} {
		if !providerIssue(l, "gmail.com") {
			t.Errorf("%s: want a provider issue", l)
		}
	}

	good := []string{
		"abcdef@gmail.com",
		"john.smith@gmail.com",
		"j.o.h.n@gmail.com",                        // 4 letters but 7 with periods: Google does not say, so it passes
		"johnsmith+news@gmail.com",                 // tag
		"johnsmith+a_b-c@gmail.com",                // anything after + is the tag
		strings.Repeat("a", 30) + "@gmail.com",     // 30 letters
		strings.Repeat("ab.", 14) + "ab@gmail.com", // 30 letters with periods
		"john_smith@company.com",                   // other domains: no rule
		"abc@workspace-customer.com",               // Google Workspace domain
		"a_b@yahoo.com",
	}
	for _, e := range good {
		if r := Classify(e); r.Format || r.Provider {
			t.Errorf("%s: want no provider issue, got %+v", e, r)
		}
	}
}

func TestTypoDomains(t *testing.T) {
	cases := map[string]string{
		"kim@gmail.con":         "kim@gmail.com",
		"kim@gmaii.com":         "kim@gmail.com",
		"kim@gmal.com":          "kim@gmail.com",
		"kim@gmail.co":          "kim@gmail.com",
		"kim.lee+x@hotmial.com": "kim.lee+x@hotmail.com",
		"kim@naver.con":         "kim@naver.com",
		"kim@company.con":       "kim@company.com", // a top-level label that does not exist
		"kim@mail.company.cmo":  "kim@mail.company.com",
		"kim@yahooo.com":        "kim@yahoo.com",
	}
	for in, want := range cases {
		r := Classify(in)
		if !r.Typo || r.Suggest != want {
			t.Errorf("%s: want typo → %s, got %+v", in, want, r)
		}
		if !(Verdict{Result: r}).Confirmed() {
			t.Errorf("%s: a typo must count as confirmed", in)
		}
	}
	for _, in := range []string{"kimlee@gmail.com", "kim@company.co", "kim@company.cm", "kim@con.com", "kim@con", "kim@example.org"} {
		if r := Classify(in); r.Typo {
			t.Errorf("%s: not a typo, got %+v", in, r)
		}
	}
}

func TestRuleClasses(t *testing.T) {
	v := Verdict{Result: Classify("abc@gmail.com")}
	if !reflect.DeepEqual(v.Classes(), []string{ClassProvider}) {
		t.Fatalf("got %v", v.Classes())
	}
	v = Verdict{Result: Classify("info@gmail.con")}
	if !reflect.DeepEqual(v.Classes(), []string{ClassTypo, ClassRole}) {
		t.Fatalf("got %v", v.Classes())
	}
	s := Summarize([]Verdict{{Result: Classify("abc@gmail.com")}, {Result: Classify("kimlee@gmail.con")}, {Result: Classify("kimlee@gmail.com")}}, nil, nil, false)
	if s.Confirmed != 2 || s.Classes[ClassProvider].Count != 1 || s.Classes[ClassTypo].Count != 1 {
		t.Fatalf("got confirmed %d, provider %d, typo %d", s.Confirmed, s.Classes[ClassProvider].Count, s.Classes[ClassTypo].Count)
	}
}

// Real company domains next to a misspelling are not misspellings, and a domain that breaks
// the label syntax is malformed (found in review).
func TestRealDomainsAndLabelSyntax(t *testing.T) {
	for _, e := range []string{"naber@naber.com", "kim@nave.com", "kim@hotmal.com"} {
		if r := Classify(e); r.Typo || r.Format {
			t.Errorf("%s: want a plain address, got %+v", e, r)
		}
	}
	for _, e := range []string{"a@bad_domain.example", "a@-bad.example", "a@bad-.example", "a@bad..example", "a@" + strings.Repeat("x", 64) + ".example"} {
		if r := Classify(e); !r.Format {
			t.Errorf("%s: want format, got %+v", e, r)
		}
	}
	for _, e := range []string{"a@xn--bj0bj06e.kr", "a@한글.kr", "a@sub-domain.example.com", "a@1.example"} {
		if r := Classify(e); r.Format {
			t.Errorf("%s: want valid, got %+v", e, r)
		}
	}
}
