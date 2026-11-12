package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAttribSortExp(t *testing.T) {
	for _, tc := range []struct {
		orderBy string
		ok      bool
	}{
		{"attribs.follower", true}, {"attribs.회사명", true}, {"attribs.a b", true},
		{"attribs.", false}, {"follower", false}, {"email", false}, {"attribs.a\x00b", false},
		{"attribs." + strings.Repeat("k", 64), true}, {"attribs." + strings.Repeat("k", 65), false},
	} {
		exp, ok := attribSortExp(tc.orderBy, "desc")
		if ok != tc.ok {
			t.Fatalf("%q: got %v", tc.orderBy, ok)
		}
		if ok && !strings.HasSuffix(exp, "subscribers.id desc") {
			t.Fatalf("%q: %s", tc.orderBy, exp)
		}
	}
	// A quote in the key stays inside the literal.
	exp, ok := attribSortExp("attribs.k'); DROP TABLE lists; --", "asc")
	if !ok || !strings.Contains(exp, `'k''); DROP TABLE lists; --'`) {
		t.Fatal(exp)
	}
	// A backslash makes pq use an escape string; the key is still one literal.
	exp, _ = attribSortExp(`attribs.k\'`, "asc")
	if !strings.Contains(exp, ` E'k\\'''`) {
		t.Fatal(exp)
	}
}

func TestAttribFilterExp(t *testing.T) {
	for _, raw := range []string{"", "  ", "[]", "null"} {
		exp, err := AttribFilterExp([]byte(raw))
		if err != nil || exp != "" {
			t.Fatalf("%q: %q %v", raw, exp, err)
		}
	}
	exp, err := AttribFilterExp([]byte(`[{"key":" country ","op":"eq","value":" PH "},{"key":"follower","op":"gte","value":"500,000"}]`))
	if err != nil {
		t.Fatal(err)
	}
	// Normalized like a dynamic list filter: trimmed text, numbers without commas.
	want := `axm_filter_match(subscribers.attribs, '{"version":1,"match":"all","conditions":[{"key":"country","op":"eq","value":"PH"},{"key":"follower","op":"gte","value":"500000"}]}'::JSONB)`
	if exp != want {
		t.Fatalf("got  %s\nwant %s", exp, want)
	}
	for _, raw := range []string{
		`{}`, `[{"key":"a","op":"drop"}]`, `[{"key":"","op":"empty"}]`, `[{"key":"a","op":"eq"}]`,
		`[{"key":"a","op":"gte","value":"100,000원"}]`, `[{"key":"a","op":"empty","sql":"1=1"}]`,
		`[{"key":"a","op":"empty"}] x`, `[{"key":"a","op":"eq","value":"b","value2":"c"}]`,
		`[` + strings.TrimSuffix(strings.Repeat(`{"key":"a","op":"empty"},`, 21), ",") + `]`,
	} {
		if exp, err := AttribFilterExp([]byte(raw)); err == nil {
			t.Fatalf("accepted %s as %s", raw, exp)
		}
	}
	// Quotes are doubled and percent signs never appear, so neither a placeholder nor a
	// quote can escape the literal.
	exp, err = AttribFilterExp([]byte(`[{"key":"%order%","op":"eq","value":"%query%' OR 1=1 --"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exp, "%") || !strings.Contains(exp, `'' OR 1=1 --`) || strings.Count(exp, "'")%2 != 0 {
		t.Fatal(exp)
	}
	// The escape is the six characters backslash-u-0-0-2-5, which JSONB reads back as "%".
	if strings.Count(exp, jsonPercent) != 4 || len(jsonPercent) != 6 || jsonPercent[0] != 92 {
		t.Fatal(exp)
	}
}

func TestAndSQLExp(t *testing.T) {
	for _, tc := range [][3]string{{"", "", ""}, {"a", "", "a"}, {"", "b", "b"}, {"a OR b", "c", "(a OR b) AND (c)"}} {
		if got := AndSQLExp(tc[0], tc[1]); got != tc[2] {
			t.Fatalf("%v: %q", tc, got)
		}
	}
}

func TestAttribText(t *testing.T) {
	for _, tc := range [][2]string{
		{``, ""}, {`null`, ""}, {`"PH"`, "PH"}, {`" 12,500 "`, " 12,500 "}, {`12500`, "12500"}, {`1.50`, "1.50"},
		{`true`, "true"}, {`["vip","tech"]`, "vip, tech"}, {`["a",1,null,{"b":1},[2]]`, `a, 1, , {"b":1}, [2]`},
		{`{"city": "Cebu", "zip": "6000"}`, `{"city":"Cebu","zip":"6000"}`}, {`"한\n글"`, "한\n글"},
	} {
		if got := AttribText(json.RawMessage(tc[0])); got != tc[1] {
			t.Fatalf("%s: got %q want %q", tc[0], got, tc[1])
		}
	}
}

func TestCSVSafeCell(t *testing.T) {
	for _, tc := range [][2]string{
		{"", ""}, {"PH", "PH"}, {"=1+1", "'=1+1"}, {"@SUM(A1)", "'@SUM(A1)"}, {"\tx", "'\tx"}, {"\rx", "'\rx"},
		{"-5", "-5"}, {"+1,200", "+1,200"}, {"-cmd|x", "'-cmd|x"}, {"+63 917 000 0000", "'+63 917 000 0000"},
		{"a=b", "a=b"},
	} {
		if got := CSVSafeCell(tc[0]); got != tc[1] {
			t.Fatalf("%q: got %q want %q", tc[0], got, tc[1])
		}
	}
}

func TestExportAttribColumns(t *testing.T) {
	fixed := []string{"uuid", "email", "name", "attributes", "status", "created_at", "updated_at"}
	keys, headers, err := ExportAttribColumns([]string{"country", "name", "country", "attributes.name"}, fixed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []string{"country", "name", "attributes.name"}) {
		t.Fatal(keys)
	}
	// A key that equals a fixed column is prefixed, and headers stay unique.
	if !reflect.DeepEqual(headers, []string{"country", "attributes.name", "attributes.attributes.name"}) {
		t.Fatal(headers)
	}
	if _, _, err := ExportAttribColumns([]string{""}, fixed); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, _, err := ExportAttribColumns(make([]string, 21), fixed); err == nil {
		t.Fatal("21 columns accepted")
	}
	if keys, headers, err := ExportAttribColumns(nil, fixed); err != nil || len(keys) != 0 || len(headers) != 0 {
		t.Fatal(keys, headers, err)
	}
}

func TestColumnPresets(t *testing.T) {
	if !ValidColumnPreset(DefaultColumnPreset) {
		t.Fatal("default preset is not defined")
	}
	if ValidColumnPreset("") || ValidColumnPreset("nope") {
		t.Fatal("unknown preset accepted")
	}
	seen := map[string]bool{}
	for _, p := range ColumnPresets {
		if p.ID == "" || seen[p.ID] || len(p.Columns) == 0 {
			t.Fatalf("bad preset %+v", p)
		}
		seen[p.ID] = true
		for _, k := range p.Columns {
			if !validAttribKey(k) {
				t.Fatalf("bad key %q in %s", k, p.ID)
			}
		}
	}
	// The preset agreed for this release.
	if !reflect.DeepEqual(ColumnPresets[0], ColumnPreset{ID: "creator", Columns: []string{"country", "follower"}}) {
		t.Fatal(ColumnPresets[0])
	}
}
