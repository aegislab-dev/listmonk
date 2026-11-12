package core

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// StandardAttrib is one entry of the standard attribute dictionary: an attribute key every
// screen offers under the same name, with the way its values are stored.
type StandardAttrib struct {
	Key string `json:"key"`
	// Kind is the value format: "country" (ISO 3166-1 alpha-2 code, two capitals),
	// "integer" (a whole number of 0 or more, stored as a JSON number) or "text" (as written).
	Kind string `json:"kind"`
	// Aliases are the column names collected under this key. They are compared without
	// letter case, spaces, underscores and hyphens, so "Job Title" covers job_title too.
	Aliases []string `json:"aliases"`
}

// The dictionary is one JSON file, so that the frontend's tests read the very same entries
// the server sends to the screens in /api/config (axm_standard_attribs). The import column
// mapper, the dynamic list conditions, the subscribers table and the personalization menu
// all take the keys, formats and aliases from there; their labels are the i18n strings
// "subscribers.standard.<key>". To add or rename a standard item, edit the JSON file (and
// the labels); the column presets below it must only use keys from this dictionary.
//
//go:embed axm_standard_attribs.json
var standardAttribsJSON []byte

// StandardAttribs is the standard attribute dictionary in display order.
var StandardAttribs = mustStandardAttribs(standardAttribsJSON)

var standardKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// NormalizeAttribName folds a column name or key for alias comparison.
func NormalizeAttribName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '_' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(s)))
}

func parseStandardAttribs(b []byte) ([]StandardAttrib, error) {
	var out []StandardAttrib
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range out {
		if !standardKey.MatchString(a.Key) {
			return nil, fmt.Errorf("standard attribute key %q is not snake_case", a.Key)
		}
		if a.Kind != "country" && a.Kind != "integer" && a.Kind != "text" {
			return nil, fmt.Errorf("standard attribute %q has unknown kind %q", a.Key, a.Kind)
		}
		// The key itself is always one of its names; no name may point at two keys.
		for _, name := range append([]string{a.Key}, a.Aliases...) {
			n := NormalizeAttribName(name)
			if n == "" {
				return nil, fmt.Errorf("standard attribute %q has an empty alias", a.Key)
			}
			if other, ok := names[n]; ok && other != a.Key {
				return nil, fmt.Errorf("alias %q belongs to both %q and %q", name, other, a.Key)
			}
			names[n] = a.Key
		}
	}
	return out, nil
}

func mustStandardAttribs(b []byte) []StandardAttrib {
	out, err := parseStandardAttribs(b)
	if err != nil {
		panic("axm_standard_attribs.json: " + err.Error())
	}
	return out
}

// IsStandardAttrib tells whether a key is in the standard dictionary.
func IsStandardAttrib(key string) bool {
	for _, a := range StandardAttribs {
		if a.Key == key {
			return true
		}
	}
	return false
}
