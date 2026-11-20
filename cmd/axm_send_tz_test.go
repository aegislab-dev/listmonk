package main

import (
	"testing"

	"github.com/knadh/listmonk/models"
)

func TestValidSendTZ(t *testing.T) {
	tests := []struct {
		name    string
		attribs models.JSON
		want    bool
	}{
		{"no attribs", nil, true},
		{"no key", models.JSON{"a": 1}, true},
		{"KST", models.JSON{sendTZKey: "Asia/Seoul"}, true},
		{"New York", models.JSON{sendTZKey: "America/New_York"}, true},
		{"three parts", models.JSON{sendTZKey: "America/Argentina/Buenos_Aires"}, true},
		{"UTC", models.JSON{sendTZKey: "UTC"}, true},
		{"other keys kept", models.JSON{"a": "b", sendTZKey: "Europe/London"}, true},
		{"unknown", models.JSON{sendTZKey: "Mars/Phobos"}, false},
		{"empty", models.JSON{sendTZKey: ""}, false},
		{"not a string", models.JSON{sendTZKey: 9}, false},
		{"null", models.JSON{sendTZKey: nil}, false},
		{"line break", models.JSON{sendTZKey: "Asia/Seoul\n"}, false},
		{"space", models.JSON{sendTZKey: "Asia/ Seoul"}, false},
		{"Local", models.JSON{sendTZKey: "Local"}, false},
		{"path", models.JSON{sendTZKey: "../../etc/passwd"}, false},
		{"too long", models.JSON{sendTZKey: "Asia/" + string(make([]byte, 100))}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validSendTZ(tc.attribs); got != tc.want {
				t.Fatalf("validSendTZ(%v) = %v, want %v", tc.attribs, got, tc.want)
			}
		})
	}
}
