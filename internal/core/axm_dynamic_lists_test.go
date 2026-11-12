package core

import (
	"encoding/json"
	"strings"
	"testing"
)

const validFilter = `{"version":1,"base":{"type":"lists","lists":[1,2,1]},"match":"all","conditions":[{"key":" plan ","op":"eq","value":" pro "}]}`

func TestValidateDynamicFilter(t *testing.T) {
	f, err := ValidateDynamicFilter([]byte(validFilter))
	if err != nil || len(f.Base.Lists) != 2 || f.Conditions[0].Key != "plan" || *f.Conditions[0].Value != "pro" {
		t.Fatalf("normalization: %+v, %v", f, err)
	}
	cases := map[string]string{
		"unknown":          strings.Replace(validFilter, `"version":1`, `"unknown":true,"version":1`, 1),
		"version":          strings.Replace(validFilter, `"version":1`, `"version":2`, 1),
		"no version":       strings.Replace(validFilter, `"version":1,`, "", 1),
		"match":            strings.Replace(validFilter, `"match":"all"`, `"match":"and"`, 1),
		"base":             strings.Replace(validFilter, `"type":"lists"`, `"type":"sql"`, 1),
		"negative":         strings.Replace(validFilter, `[1,2,1]`, `[-1]`, 1),
		"no lists":         strings.Replace(validFilter, `[1,2,1]`, `[]`, 1),
		"all with lists":   strings.Replace(validFilter, `"type":"lists"`, `"type":"all"`, 1),
		"op":               strings.Replace(validFilter, `"op":"eq"`, `"op":"sql"`, 1),
		"empty with value": strings.Replace(validFilter, `"op":"eq"`, `"op":"empty"`, 1),
		"missing value":    strings.Replace(validFilter, `,"value":" pro "`, "", 1),
		"long key":         strings.Replace(validFilter, " plan ", strings.Repeat("가", 65), 1),
		"control key":      strings.Replace(validFilter, " plan ", `ab\ncd`, 1),
		"blank value":      strings.Replace(validFilter, " pro ", " ", 1),
		"long value":       strings.Replace(validFilter, " pro ", strings.Repeat("가", 201), 1),
		"trailing":         validFilter + " {}",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateDynamicFilter([]byte(raw)); err == nil {
				t.Fatal("accepted", raw)
			}
		})
	}
	for _, n := range []int{0, 21} {
		f.Conditions = make([]DynamicCondition, n)
		b, _ := json.Marshal(f)
		if _, err := ValidateDynamicFilter(b); err == nil {
			t.Fatalf("accepted %d conditions", n)
		}
	}
	for _, op := range []string{"eq", "contains", "has", "empty"} {
		raw := strings.Replace(validFilter, `"op":"eq"`, `"op":"`+op+`"`, 1)
		if op == "empty" {
			raw = strings.Replace(raw, `,"value":" pro "`, "", 1)
		}
		if _, err := ValidateDynamicFilter([]byte(raw)); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	raw := strings.Replace(validFilter, `[1,2,1]`, `[`+strings.TrimSuffix(strings.Repeat("1,", 21), ",")+`]`, 1)
	if _, err := ValidateDynamicFilter([]byte(raw)); err == nil {
		t.Fatal("accepted 21 base IDs")
	}
}

func TestValidateDynamicNumbers(t *testing.T) {
	filter := func(cond string) []byte {
		return []byte(`{"version":1,"base":{"type":"all"},"match":"all","conditions":[` + cond + `]}`)
	}
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	// Condition values follow the DB rule for attribute values and are stored as plain decimals.
	for raw, want := range map[string]string{
		"100000": "100000", " 100,000 ": "100000", "1e5": "100000", "1E+05": "100000", "+1,234.50": "1234.5",
		"-0.50": "-0.5", ".5": "0.5", "5.": "5", "-0": "0", "1e-2": "0.01", "12,345,678": "12345678",
	} {
		for _, op := range []string{"gte", "gt", "lte", "lt"} {
			f, err := ValidateDynamicFilter(filter(`{"key":"amount","op":"` + op + `","value":` + quote(raw) + `}`))
			if err != nil || *f.Conditions[0].Value != want || f.Conditions[0].Value2 != nil {
				t.Fatalf("%s %q: %+v, %v", op, raw, f.Conditions, err)
			}
		}
	}
	for _, raw := range []string{
		"100,000원", "₩100,000", "10만", "1,00", "12,34", "1,2345", "abc", "1e999", "１００", "1 000", "NaN",
		"Infinity", "0x10", "--1", "1e", ".", "", "50%", strings.Repeat("1", 65),
	} {
		if _, err := ValidateDynamicFilter(filter(`{"key":"amount","op":"gte","value":` + quote(raw) + `}`)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	f, err := ValidateDynamicFilter(filter(`{"key":"amount","op":"between","value":" 50,000 ","value2":"1e5"}`))
	if err != nil || *f.Conditions[0].Value != "50000" || *f.Conditions[0].Value2 != "100000" {
		t.Fatalf("between: %+v, %v", f.Conditions, err)
	}
	for name, cond := range map[string]string{
		"between without upper": `{"key":"amount","op":"between","value":"1"}`,
		"between without lower": `{"key":"amount","op":"between","value2":"1"}`,
		"between equal":         `{"key":"amount","op":"between","value":"100000","value2":"100,000"}`,
		"between reversed":      `{"key":"amount","op":"between","value":"2","value2":"1"}`,
		"between text upper":    `{"key":"amount","op":"between","value":"1","value2":"many"}`,
		"number without value":  `{"key":"amount","op":"lt"}`,
		"second value on gte":   `{"key":"amount","op":"gte","value":"1","value2":"2"}`,
		"second value on eq":    `{"key":"amount","op":"eq","value":"1","value2":"2"}`,
		"second value on empty": `{"key":"amount","op":"empty","value2":"2"}`,
		"diagnostic operator":   `{"key":"amount","op":"not_number"}`,
	} {
		if _, err := ValidateDynamicFilter(filter(cond)); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	// The preview diagnostic covers each numeric key once and nothing else.
	f, err = ValidateDynamicFilter(filter(`{"key":"amount","op":"gte","value":"1"},{"key":"plan","op":"eq","value":"pro"},` +
		`{"key":"amount","op":"lt","value":"9"},{"key":"age","op":"between","value":"20","value2":"30"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.notNumberFilter())
	if string(raw) != `{"version":1,"base":{"type":"all"},"match":"any","conditions":[{"key":"amount","op":"not_number"},{"key":"age","op":"not_number"}]}` {
		t.Fatal(string(raw))
	}
	f, _ = ValidateDynamicFilter([]byte(validFilter))
	if f.notNumberFilter() != nil {
		t.Fatal("diagnostic filter without numeric conditions")
	}
}

func TestDynamicListQueryContainsOnlyIDs(t *testing.T) {
	for _, payload := range []string{"' ; -- $1", `x\\y`, "회사명"} {
		f := DynamicFilter{Version: 1, Base: DynamicBase{Type: "all"}, Match: "all", Conditions: []DynamicCondition{{Key: payload, Op: "eq", Value: &payload}}}
		raw, _ := json.Marshal(f)
		if _, err := ValidateDynamicFilter(raw); err != nil {
			t.Fatal(err)
		}
		ids, q := dynamicListQuery([]int{1, 2, 3}, []int{2, 3}, "")
		if len(ids) != 0 || strings.Contains(q, payload) || strings.Count(q, "axm_dynamic_list_members(") != 2 || !strings.Contains(q, "IN (1)") {
			t.Fatal(q)
		}
	}
	_, q := dynamicListQuery([]int{2}, []int{2}, "name = 'test'")
	if !strings.HasPrefix(q, "(name = 'test') AND (") {
		t.Fatal(q)
	}
	ids, q := dynamicListQuery([]int{1}, nil, "x")
	if len(ids) != 1 || q != "x" {
		t.Fatal(ids, q)
	}
}
