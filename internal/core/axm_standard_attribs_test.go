package core

import (
	"reflect"
	"testing"
)

func TestStandardAttribs(t *testing.T) {
	var keys []string
	for _, a := range StandardAttribs {
		keys = append(keys, a.Key)
	}
	if want := []string{"country", "follower", "company", "title", "industry"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v", keys)
	}
	if !IsStandardAttrib("follower") || IsStandardAttrib("followers") || IsStandardAttrib("region") {
		t.Fatal("IsStandardAttrib")
	}
	// The table columns are standard items, so the screens that read the dictionary know them.
	for _, p := range ColumnPresets {
		for _, c := range p.Columns {
			if !IsStandardAttrib(c) {
				t.Fatalf("preset %s shows %q, which is not a standard attribute", p.ID, c)
			}
		}
	}
}

func TestParseStandardAttribs(t *testing.T) {
	for name, raw := range map[string]string{
		"not JSON":       `[`,
		"key":            `[{"key":"Country","kind":"text","aliases":[]}]`,
		"kind":           `[{"key":"country","kind":"date","aliases":[]}]`,
		"empty alias":    `[{"key":"country","kind":"text","aliases":[" _ "]}]`,
		"shared alias":   `[{"key":"country","kind":"text","aliases":["Job Title"]},{"key":"title","kind":"text","aliases":["job_title"]}]`,
		"alias is a key": `[{"key":"country","kind":"text","aliases":["title"]},{"key":"title","kind":"text","aliases":[]}]`,
	} {
		if _, err := parseStandardAttribs([]byte(raw)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if got := NormalizeAttribName("  Follower_Count "); got != "followercount" {
		t.Fatal(got)
	}
	if NormalizeAttribName("팔로워 수") != NormalizeAttribName("팔로워수") {
		t.Fatal("spaces")
	}
}
