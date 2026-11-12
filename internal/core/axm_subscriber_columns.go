package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
)

// attribSortPrefix marks an order_by value that sorts by one attribute: "attribs.<key>".
const attribSortPrefix = "attribs."

// validAttribKey applies the key rule of dynamic list conditions.
func validAttribKey(key string) bool {
	n := utf8.RuneCountInString(key)
	return n >= 1 && n <= 64 && utf8.ValidString(key) && strings.IndexFunc(key, unicode.IsControl) < 0
}

// attribSortExp returns the ORDER BY expression for order_by=attribs.<key>. Values that read
// as numbers (the axm_to_number() rule, so "12,500" and 12500 are the same) come first in
// numeric order, then the other values as text, and subscribers without a value come last in
// both directions. The key only ever enters the statement as a quoted literal.
func attribSortExp(orderBy, order string) (string, bool) {
	key, ok := strings.CutPrefix(orderBy, attribSortPrefix)
	if !ok || !validAttribKey(key) {
		return "", false
	}
	k := pq.QuoteLiteral(key)
	return fmt.Sprintf("axm_to_number(subscribers.attribs->%s) %s NULLS LAST, "+
		"NULLIF(btrim(subscribers.attribs->>%s), '') %s NULLS LAST, subscribers.id %s", k, order, k, order, order), true
}

// AttribFilterExp turns the subscribers table's attribute conditions, a JSON array of dynamic
// list conditions that must all match, into an SQL expression over subscribers.attribs. The
// conditions stay data: they reach the DB as one quoted JSONB literal read by
// axm_filter_match(), the function behind dynamic lists. Empty input returns "".
func AttribFilterExp(raw []byte) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var conds []DynamicCondition
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&conds); err != nil {
		return "", err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("invalid trailing JSON")
	}
	if len(conds) == 0 {
		return "", nil
	}
	if err := validateDynamicConditions(conds); err != nil {
		return "", err
	}
	b, err := json.Marshal(struct {
		Version    int                `json:"version"`
		Match      string             `json:"match"`
		Conditions []DynamicCondition `json:"conditions"`
	}{1, "all", conds})
	if err != nil {
		return "", err
	}
	// The statement is assembled by replacing %query% and %order% placeholders, so the
	// literal must never contain a percent sign.
	lit := strings.ReplaceAll(string(b), "%", `%`)
	return "axm_filter_match(subscribers.attribs, " + pq.QuoteLiteral(lit) + "::JSONB)", nil
}

// AndSQLExp joins two optional SQL conditions.
func AndSQLExp(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return "(" + a + ") AND (" + b + ")"
}

// AttribText shows one attribute value the way the subscribers table does: text as it is,
// numbers and booleans as written, a list as its items joined by ", ", an object as compact
// JSON, and nothing for a missing or null value.
func AttribText(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return ""
	}
	switch v[0] {
	case '"':
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(v, &items) == nil {
			parts := make([]string, 0, len(items))
			for _, it := range items {
				if len(it) > 0 && (it[0] == '[' || it[0] == '{') {
					parts = append(parts, compactJSON(it))
				} else {
					parts = append(parts, AttribText(it))
				}
			}
			return strings.Join(parts, ", ")
		}
	case '{':
		return compactJSON(v)
	}
	return string(v)
}

func compactJSON(v []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
	}
	return buf.String()
}

// CSVSafeCell keeps spreadsheet programs from running an exported attribute value as a
// formula. Values that start with = or @, with a tab or carriage return, or with + or -
// without being a number get a leading apostrophe.
func CSVSafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '@', '\t', '\r':
		return "'" + s
	case '+', '-':
		if _, _, ok := parseDynamicNumber(s); !ok {
			return "'" + s
		}
	}
	return s
}

// ExportAttribColumns validates the attribute columns requested for a CSV export and returns
// the keys with their header names. A key that equals a fixed export column is prefixed.
func ExportAttribColumns(keys []string, fixed []string) ([]string, []string, error) {
	if len(keys) > 20 {
		return nil, nil, fmt.Errorf("select up to 20 attribute columns")
	}
	used := map[string]bool{}
	for _, f := range fixed {
		used[f] = true
	}
	out, headers := []string{}, []string{}
	seen := map[string]bool{}
	for _, k := range keys {
		if !validAttribKey(k) {
			return nil, nil, fmt.Errorf("invalid attribute key")
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		h := k
		if used[h] {
			h = "attributes." + k
		}
		used[h] = true
		out, headers = append(out, k), append(headers, h)
	}
	return out, headers, nil
}
