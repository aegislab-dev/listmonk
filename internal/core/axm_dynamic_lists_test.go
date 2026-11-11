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
