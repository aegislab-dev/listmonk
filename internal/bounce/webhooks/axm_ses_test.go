package webhooks

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The addresses of an SES notification are the recipients it names as bounced or complaining,
// each one, not the first recipient of the mail (LST-56).
func TestSESRecipients(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want []string
	}{
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"SES-Bad@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-bad@ok.test"]}}`, []string{"ses-bad@ok.test"}},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"multi-bad1@ok.test","status":"5.1.1"},{"emailAddress":"multi-bad2@ok.test","status":"5.1.1"}]},"mail":{"destination":["fine@ok.test","multi-bad1@ok.test","multi-bad2@ok.test"]}}`, []string{"multi-bad1@ok.test", "multi-bad2@ok.test"}},
		{`{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"ses-comp@ok.test"}]},"mail":{"destination":["ses-first@ok.test","ses-comp@ok.test"]}}`, []string{"ses-comp@ok.test"}},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"status":"5.1.1"}]},"mail":{"destination":["Only@ok.test"]}}`, []string{"only@ok.test"}},
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
