package addrcheck

import "strings"

// roleLocals are mailbox names that usually belong to a team or a function, not to one
// person. They bounce and complain more often, but many are real contact points, so the
// screens only warn about them.
var roleLocals = func() map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range strings.Fields(`
		info admin admins administrator contact contacts sales support help helpdesk
		office marketing booking bookings reservation reservations hello team hr
		webmaster postmaster hostmaster abuse noreply no-reply no_reply donotreply
		do-not-reply mailer-daemon root billing careers jobs recruit press pr newsletter
		service cs customerservice enquiry enquiries inquiry inquiries security privacy
		legal accounts accounting finance media partners partnership`) {
		out[name] = struct{}{}
	}
	return out
}()

// isRole reports whether the local part, without a +tag, is a role name.
func isRole(local string) bool {
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	_, ok := roleLocals[strings.ToLower(local)]
	return ok
}
