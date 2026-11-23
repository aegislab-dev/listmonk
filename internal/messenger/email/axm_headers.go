package email

import "net/textproto"

// ServerHeaders tells which of the named headers the SMTP servers set on a mail that is
// sent from the address from, and with what (AXM, LST-48: the pre-send confirmation shows
// what the recipients get). The servers set these first, so a header of the campaign still
// replaces them. The servers that may send the mail are the ones of the from-address pool
// (Push picks one of them at random, or any server when there is no such pool).
//
// varies is true when those servers disagree about one of the headers, so which value a
// recipient gets depends on the server the mail happens to take.
func (e *Emailer) ServerHeaders(from string, names ...string) (values map[string]string, varies bool) {
	pool := e.pools[""]
	if len(e.pools) > 1 {
		if srvs := e.getPool(from); srvs != nil {
			pool = srvs
		}
	}

	values = map[string]string{}
	for _, name := range names {
		name = textproto.CanonicalMIMEHeaderKey(name)
		var (
			val   string
			set   bool
			first = true
		)
		for _, srv := range pool {
			v, ok := "", false
			for k, hv := range srv.EmailHeaders {
				if textproto.CanonicalMIMEHeaderKey(k) == name {
					v, ok = hv, true
				}
			}
			if first {
				val, set, first = v, ok, false
			} else if ok != set || v != val {
				varies = true
			}
		}
		if set {
			values[name] = val
		}
	}
	return values, varies
}
