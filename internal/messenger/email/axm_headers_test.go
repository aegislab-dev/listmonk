package email

import (
	"reflect"
	"testing"
)

func TestServerHeaders(t *testing.T) {
	a := &Server{Name: "a", EmailHeaders: map[string]string{"subject": "A subject", "X-Other": "1"}, FromAddresses: []string{"news@x.test"}}
	b := &Server{Name: "b", EmailHeaders: map[string]string{"Subject": "A subject", "Reply-To": "r@x.test"}}
	c := &Server{Name: "c", EmailHeaders: map[string]string{"Subject": "C subject"}}
	names := []string{"From", "Subject", "Reply-To"}

	// One server: its headers, in any letter case.
	e := &Emailer{pools: map[string][]*Server{"": {a}}}
	got, varies := e.ServerHeaders("Team <news@x.test>", names...)
	if varies || !reflect.DeepEqual(got, map[string]string{"Subject": "A subject"}) {
		t.Fatalf("one server: %v %v", got, varies)
	}

	// Two servers that agree about Subject but only one sets Reply-To: it depends on the server.
	e = &Emailer{pools: map[string][]*Server{"": {a, b}}}
	if _, varies = e.ServerHeaders("x@y.test", names...); !varies {
		t.Fatal("a header that only one of two servers sets must vary")
	}
	if got, varies = e.ServerHeaders("x@y.test", "Subject"); varies || got["Subject"] != "A subject" {
		t.Fatalf("agreeing servers: %v %v", got, varies)
	}

	// Disagreeing servers vary.
	e = &Emailer{pools: map[string][]*Server{"": {a, c}}}
	if _, varies = e.ServerHeaders("x@y.test", "Subject"); !varies {
		t.Fatal("different subjects must vary")
	}

	// A from-address pool is used when the mail is sent from that address, the rest otherwise.
	e = &Emailer{pools: map[string][]*Server{"": {a, c}, "news@x.test": {a}}}
	if got, varies = e.ServerHeaders("Team <news@x.test>", "Subject"); varies || got["Subject"] != "A subject" {
		t.Fatalf("from-address pool: %v %v", got, varies)
	}
	if _, varies = e.ServerHeaders("other@x.test", "Subject"); !varies {
		t.Fatal("any other address uses all servers")
	}
}
