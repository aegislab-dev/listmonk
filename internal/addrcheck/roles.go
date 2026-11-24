package addrcheck

import (
	_ "embed"
	"strings"
)

// roleLocals are mailbox names that usually belong to a team or a function, not to one
// person. They bounce and complain more often, but many are real contact points, so they
// are let in with a warning. The names are in role_locals.txt, with their source and how to
// change them.
//
//go:embed role_locals.txt
var roleRaw string

var roleLocals = func() map[string]struct{} {
	out := map[string]struct{}{}
	for _, line := range strings.Split(roleRaw, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" || line[0] == '#' {
			continue
		}
		out[line] = struct{}{}
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
