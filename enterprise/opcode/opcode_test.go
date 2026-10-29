package opcode

import (
	"regexp"
	"slices"
	"testing"
)

// The codes are an operator's: GET /v1/status and the metrics lines carry them, and dashboards
// match on them. So the set is pinned here, each code unique and lower snake_case, None the empty
// one: renaming, removing or adding a code fails this cell first, and an operator is told.
func TestTheCodesAreAClosedStableSet(t *testing.T) {
	all := []Code{None, Database, LockLost, View, Order, ProviderUnreachable, ProviderRefused, ProviderRejected, Other}
	want := []string{"", "database", "lock_lost", "view", "order", "provider_unreachable", "provider_refused", "provider_rejected", "other"}
	var got []string
	for _, c := range all {
		got = append(got, string(c))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the codes are %q; want %q (an operator matches on them: change them on purpose)", got, want)
	}
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	seen := map[Code]bool{}
	for _, c := range all[1:] {
		if !snake.MatchString(string(c)) || seen[c] {
			t.Errorf("code %q: want unique lower snake_case", c)
		}
		seen[c] = true
	}
}
