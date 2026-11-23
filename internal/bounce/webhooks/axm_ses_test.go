package webhooks

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The addresses of an SES notification are the recipients it names as bounced, each with its own
// type, or the one it names as complaining; not the first recipient of the mail (LST-56).
func TestSESRecipients(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want []sesRecipient
	}{
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"SES-Bad@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-bad@ok.test"]}}`,
			[]sesRecipient{{"ses-bad@ok.test", "hard"}}},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"multi-bad1@ok.test","status":"5.1.1"},{"emailAddress":"multi-bad2@ok.test","status":"5.1.1"}]},"mail":{"destination":["fine@ok.test","multi-bad1@ok.test","multi-bad2@ok.test"]}}`,
			[]sesRecipient{{"multi-bad1@ok.test", "hard"}, {"multi-bad2@ok.test", "hard"}}},
		// A delayed recipient in a Permanent bounce is not a hard bounce.
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"failed@ok.test","action":"failed","status":"5.0.0"},{"emailAddress":"delayed@ok.test","action":"delayed","status":"4.0.0"}]},"mail":{"destination":["failed@ok.test","delayed@ok.test"]}}`,
			[]sesRecipient{{"failed@ok.test", "hard"}, {"delayed@ok.test", "soft"}}},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"nodomain@ok.test","status":"5.4.4"},{"emailAddress":"full@ok.test","status":"4.2.2"}]},"mail":{"destination":["nodomain@ok.test","full@ok.test"]}}`,
			[]sesRecipient{{"nodomain@ok.test", "hard"}, {"full@ok.test", "soft"}}},
		{`{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"ses-comp@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-comp@ok.test"]}}`,
			[]sesRecipient{{"ses-comp@ok.test", "complaint"}}},
		// Several who may have complained: nobody is accused.
		{`{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"maybe1@ok.test"},{"emailAddress":"maybe2@ok.test"}]},"mail":{"destination":["maybe1@ok.test","maybe2@ok.test"]}}`,
			nil},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent"},"mail":{"destination":["Only@ok.test"]}}`,
			[]sesRecipient{{"only@ok.test", "hard"}}},
	} {
		var m sesMail
		if err := json.Unmarshal([]byte(c.msg), &m); err != nil {
			t.Fatal(err)
		}
		if got := sesRecipients(m); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v", c.want, got)
		}
	}
}
