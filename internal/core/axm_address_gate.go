package core

import (
	"context"
	"strings"

	"github.com/knadh/listmonk/internal/addrcheck"
	"github.com/lib/pq"
)

// AddressFacts says, for each address, whether it was blocklisted before and whether a hard
// bounce or a complaint is on record for it (LST-56). The keys are the addresses trimmed and
// in lowercase; an address with no fact is in the map with both false. One query per call.
// Only a hash of the address is stored, so a subscriber that was deleted still counts.
func (c *Core) AddressFacts(ctx context.Context, emails []string) (map[string]addrcheck.Facts, error) {
	out := make(map[string]addrcheck.Facts, len(emails))
	keys := make([]string, 0, len(emails))
	for _, e := range emails {
		k := strings.ToLower(strings.TrimSpace(e))
		if _, ok := out[k]; ok {
			continue
		}
		out[k] = addrcheck.Facts{}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := c.q.AxmAddressFacts.QueryContext(ctx, pq.Array(keys))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			email string
			f     addrcheck.Facts
		)
		if err := rows.Scan(&email, &f.Suppressed, &f.Bounced); err != nil {
			return nil, err
		}
		out[email] = f
	}
	return out, rows.Err()
}
