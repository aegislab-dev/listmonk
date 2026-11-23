package addrcheck

import (
	"os"
	"strings"
	"testing"
)

func verdict(email string, st DomainStatus) Verdict { return Verdict{Result: Classify(email), Status: st} }

func TestGateTable(t *testing.T) {
	both := Facts{Suppressed: true, Bounced: true}
	cases := []struct {
		name         string
		v            Verdict
		f            Facts
		dns          bool
		reject, warn string
	}{
		{"ok", verdict("kim.minsu@example.com", StatusOK), Facts{}, true, "", ""},
		{"format", verdict("not-an-address", ""), both, true, ClassFormat, ""},
		{"provider", verdict("a_b@gmail.com", StatusDead), both, true, ClassProvider, ""},
		{"typo", verdict("someone@gmail.con", StatusDead), both, true, ClassTypo, ""},
		{"suppressed", verdict("kim.minsu@example.com", StatusOK), both, true, ClassSuppressed, ""},
		{"bounced", verdict("kim.minsu@example.com", StatusOK), Facts{Bounced: true}, true, ClassBounced, ""},
		{"bounced before disposable", verdict("kim@mailinator.com", StatusOK), Facts{Bounced: true}, true, ClassBounced, ""},
		{"disposable", verdict("kim@mailinator.com", StatusOK), Facts{}, true, ClassDisposable, ""},
		{"disposable before dead", verdict("kim@mailinator.com", StatusDead), Facts{}, true, ClassDisposable, ""},
		{"nullmx", verdict("kim@example.com", StatusNullMX), Facts{}, true, ClassNullMX, ""},
		{"dead", verdict("kim@example.com", StatusDead), Facts{}, true, ClassDead, ""},
		{"dead beats role", verdict("info@example.com", StatusDead), Facts{}, true, ClassDead, ""},
		{"role", verdict("info@example.com", StatusOK), Facts{}, true, "", ClassRole},
		{"unknown", verdict("kim@example.com", StatusUnknown), Facts{}, true, "", ClassUnknown},
		{"pending", verdict("kim@example.com", StatusPending), Facts{}, true, "", ClassUnknown},
		{"unknown before role", verdict("info@example.com", StatusUnknown), Facts{}, true, "", ClassUnknown},
		{"literal", verdict("kim@[192.0.2.1]", ""), Facts{}, true, "", ClassUnknown},
		// Without DNS the DNS classes and the unknown warning are left out.
		{"no dns: dead passes", verdict("kim@example.com", StatusDead), Facts{}, false, "", ""},
		{"no dns: unknown passes", verdict("kim@example.com", StatusUnknown), Facts{}, false, "", ""},
		{"no dns: literal passes", verdict("kim@[192.0.2.1]", ""), Facts{}, false, "", ""},
		{"no dns: role still warns", verdict("info@example.com", StatusDead), Facts{}, false, "", ClassRole},
		{"no dns: disposable still refused", verdict("kim@mailinator.com", ""), Facts{}, false, ClassDisposable, ""},
		{"no dns: suppressed still refused", verdict("kim@example.com", ""), Facts{Suppressed: true}, false, ClassSuppressed, ""},
	}
	for _, c := range cases {
		reject, warn := Gate(c.v, c.f, c.dns)
		if reject != c.reject || warn != c.warn {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, reject, warn, c.reject, c.warn)
		}
	}
}

// The gate must not change what the screens of the campaign and list panels count.
func TestGateLeavesVerdictClassesAlone(t *testing.T) {
	v := verdict("kim@mailinator.com", StatusOK)
	if v.Confirmed() || !v.Warning() {
		t.Fatalf("disposable is still a warning for the panels: confirmed=%v warning=%v", v.Confirmed(), v.Warning())
	}
}

// Both list files say where they come from and how to update them, and hold entries.
func TestListFilesHaveHeaders(t *testing.T) {
	for _, f := range []struct {
		name   string
		raw    string
		fields []string
	}{
		{"disposable_domains.txt", disposableRaw, []string{"# Source:", "# Commit:", "# License:", "# To update:"}},
		{"role_locals.txt", roleRaw, []string{"# Source:", "# To update:", "# Updated:"}},
	} {
		head := ""
		entries := 0
		for _, line := range strings.Split(f.raw, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "#"):
				head += line + "\n"
			case line != "":
				entries++
			}
		}
		for _, want := range f.fields {
			if !strings.Contains(head, want) {
				t.Errorf("%s: header has no %q line", f.name, want)
			}
		}
		if entries < 1 {
			t.Errorf("%s: no entries", f.name)
		}
	}
	if _, err := os.Stat("role_locals.txt"); err != nil {
		t.Error(err)
	}
}

func TestRoleNamesAreTheOldTable(t *testing.T) {
	for _, n := range []string{"info", "admin", "no-reply", "mailer-daemon", "partnership", "bookings"} {
		if !isRole(n) || !isRole(strings.ToUpper(n)+"+tag") {
			t.Errorf("%s is not a role", n)
		}
	}
	if isRole("kim") {
		t.Error("kim is a role")
	}
}
