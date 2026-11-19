package core

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
)

const derivedListNameMax = 200

func listNameWithSuffix(name, suffix string) string {
	room := derivedListNameMax - utf8.RuneCountInString(suffix)
	return truncateListName(name, room) + suffix
}

func truncateListName(name string, limit int) string {
	if utf8.RuneCountInString(name) <= limit {
		return name
	}
	return string([]rune(name)[:limit])
}

func groupThousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func uniqueDrawName(taken map[string]bool, name string) string {
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if next := listNameWithSuffix(name, fmt.Sprintf(" (%d)", i)); !taken[next] {
			return next
		}
	}
}

// A suffix can cut characters from a maximum-length base name. Search a prefix wide
// enough to find those variants, then check the candidate names for exact matches.
func (c *Core) uniqueDerivedListName(ctx context.Context, tx *sqlx.Tx, name string) (string, error) {
	escape := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	prefix := truncateListName(name, derivedListNameMax-23)
	pattern := escape.Replace(prefix) + "%"
	if prefix == name {
		pattern = escape.Replace(name) + " (%"
	}
	var names []string
	if err := tx.Stmtx(c.q.AxmDrawNames).SelectContext(ctx, &names, name, pattern); err != nil {
		return "", err
	}
	taken := make(map[string]bool, len(names))
	for _, n := range names {
		taken[n] = true
	}
	return uniqueDrawName(taken, name), nil
}
