package core

import "testing"

// LST-42: the condition that leaves out the subscribers who are on other lists too carries
// integer IDs only, and an empty set of lists leaves out everybody who has a subscription.
func TestSharedSubscriberExclusion(t *testing.T) {
	const head = "NOT EXISTS (SELECT 1 FROM subscriber_lists axm_lk WHERE axm_lk.subscriber_id = subscribers.id AND NOT (axm_lk.list_id = ANY('"
	for _, c := range []struct {
		ids  []int
		want string
	}{
		{nil, head + "{}'::INT[])))"},
		{[]int{}, head + "{}'::INT[])))"},
		{[]int{7}, head + "{7}'::INT[])))"},
		{[]int{1, 2, -1}, head + "{1,2,-1}'::INT[])))"},
	} {
		if got := SharedSubscriberExclusion(c.ids); got != c.want {
			t.Fatalf("%v: %s", c.ids, got)
		}
	}
	if got := AndSQLExp("a = 1 OR b = 2", SharedSubscriberExclusion([]int{3})); got != "(a = 1 OR b = 2) AND ("+head+"{3}'::INT[]))))" {
		t.Fatalf("%s", got)
	}
}
