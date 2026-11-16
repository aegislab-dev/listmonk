package models

import "testing"

func TestDrawSettingsValidate(t *testing.T) {
	for _, tc := range []struct {
		s  DrawSettings
		ok bool
	}{
		{DrawSettings{Unit: "count", Value: 1}, true},
		{DrawSettings{Unit: "count", Value: DrawMaxCount}, true},
		{DrawSettings{Unit: "count", Value: DrawMaxCount + 1}, false},
		{DrawSettings{Unit: "count", Value: 0}, false},
		{DrawSettings{Unit: "percent", Value: 100}, true},
		{DrawSettings{Unit: "percent", Value: 101}, false},
		{DrawSettings{Unit: "percent", Value: 0}, false},
		{DrawSettings{Unit: "", Value: 5}, false},
		{DrawSettings{Unit: "count", Value: 5, ExcludeScope: "all", ExcludeDays: 7}, true},
		{DrawSettings{Unit: "count", Value: 5, ExcludeScope: "other"}, false},
		{DrawSettings{Unit: "count", Value: 5, ExcludeDays: DrawMaxDays}, true},
		{DrawSettings{Unit: "count", Value: 5, ExcludeDays: DrawMaxDays + 1}, false},
		{DrawSettings{Unit: "count", Value: 5, ExcludeDays: -1}, false},
	} {
		if err := tc.s.Validate(); (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc.s, err)
		}
	}
	s := DrawSettings{Unit: "count", Value: 5}
	if s.Validate(); s.ExcludeScope != DrawScopeSameSource {
		t.Fatalf("default scope %q", s.ExcludeScope)
	}
}

func TestDrawSettingsWant(t *testing.T) {
	for _, tc := range []struct {
		s      DrawSettings
		target int
		want   int
	}{
		{DrawSettings{Unit: "count", Value: 10}, 95, 10},
		{DrawSettings{Unit: "count", Value: 10}, 3, 10},
		{DrawSettings{Unit: "percent", Value: 10}, 95, 10},
		{DrawSettings{Unit: "percent", Value: 10}, 91, 10},
		{DrawSettings{Unit: "percent", Value: 33}, 10, 4},
		{DrawSettings{Unit: "percent", Value: 100}, 7, 7},
		{DrawSettings{Unit: "percent", Value: 1}, 1, 1},
		{DrawSettings{Unit: "percent", Value: 50}, 0, 0},
		{DrawSettings{Unit: "percent", Value: 100}, 2147483647, 2147483647},
	} {
		if got := tc.s.Want(tc.target); got != tc.want {
			t.Fatalf("%+v of %d: got %d want %d", tc.s, tc.target, got, tc.want)
		}
	}
}
