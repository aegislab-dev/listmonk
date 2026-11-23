package main

import (
	"net/url"
	"testing"
)

// LST-53: the suggestion header is percent-encoded, so a name outside ASCII comes back whole.
func TestAddressRuleHeaderEscape(t *testing.T) {
	for in, want := range map[string]string{
		"kimlee@gmail.com":       "kimlee@gmail.com",
		"a+b@gmail.com":          "a+b@gmail.com",
		"리뷰@dept.example.com":    "%EB%A6%AC%EB%B7%B0@dept.example.com",
		"réview@dept.example.com": "r%C3%A9view@dept.example.com",
		"50%off@dept.example.com": "50%25off@dept.example.com",
	} {
		got := headerEscape(in)
		if got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
		// decodeURIComponent and url.PathUnescape read percent-encoding alike.
		if back, err := url.PathUnescape(got); err != nil || back != in {
			t.Errorf("%q: decodes to %q (%v)", in, back, err)
		}
	}
}
