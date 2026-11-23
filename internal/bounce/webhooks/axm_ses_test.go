package webhooks

import (
	"encoding/json"
	"testing"
)

// The address of an SES notification is the recipient it names as bounced or complaining, not
// the first recipient of the mail (LST-56).
func TestSESRecipient(t *testing.T) {
	for _, c := range []struct{ msg, want string }{
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"SES-Bad@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-bad@ok.test"]}}`, "ses-bad@ok.test"},
		{`{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"ses-comp@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-comp@ok.test"]}}`, "ses-comp@ok.test"},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"status":"5.1.1"}]},"mail":{"destination":["Only@ok.test"]}}`, "only@ok.test"},
	} {
		var m sesMail
		if err := json.Unmarshal([]byte(c.msg), &m); err != nil {
			t.Fatal(err)
		}
		if got := sesRecipient(m); got != c.want {
			t.Errorf("%s: got %s", c.want, got)
		}
	}
}
