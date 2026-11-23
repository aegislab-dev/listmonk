package addrcheck

// The two classes that come from the database, not from the address or DNS (LST-56).
const (
	ClassSuppressed = "suppressed" // blocklisted before: the address is in axm_suppressed_emails
	ClassBounced    = "bounced"    // a hard bounce or a complaint is on record for the address
)

// Facts are what the database knows about an address.
type Facts struct {
	Suppressed bool
	Bounced    bool
}

// Gate is the table of what happens to an address where a subscriber is added (LST-56).
// It returns the class that refuses the address ("" when it passes) and, for an address that
// passes, the class it passes with a warning ("" when there is none: "role" or "unknown").
//
// An address that falls into several classes is refused for the first of these:
//
//	format, provider, typo, suppressed, bounced, disposable, nullmx, dead
//
// The database facts come before DNS so that a blocklisted address is never waited for. With
// dns false the DNS classes (nullmx, dead) and the "unknown" warning are left out, for a gate
// that does not look domains up. DNS that did not answer in time (unknown, pending) or an
// address literal never refuses an address: it is let in with the "unknown" warning.
func Gate(v Verdict, f Facts, dns bool) (reject, warn string) {
	switch {
	case v.Format:
		return ClassFormat, ""
	case v.Provider:
		return ClassProvider, ""
	case v.Typo:
		return ClassTypo, ""
	case f.Suppressed:
		return ClassSuppressed, ""
	case f.Bounced:
		return ClassBounced, ""
	case v.Disposable:
		return ClassDisposable, ""
	}
	if dns {
		switch v.Status {
		case StatusNullMX:
			return ClassNullMX, ""
		case StatusDead:
			return ClassDead, ""
		case StatusUnknown, StatusPending:
			return "", ClassUnknown
		}
		if v.Literal {
			return "", ClassUnknown
		}
	}
	if v.Role {
		return "", ClassRole
	}
	return "", ""
}
