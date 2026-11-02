//go:build linux

package crew

// ReuseObservedChild makes the next descendant walk see parent's child from as process to: as if
// from ended, and its pid went to to, between the walk's observation and its opens. to keeps
// from's observed start, parent and user, as a reused pid would.
func ReuseObservedChild(t interface{ Cleanup(func()) }, parent, from, to int) {
	descendantsObserved = func(children map[int][]observed) {
		for i, o := range children[parent] {
			if o.pid == from {
				children[parent][i].pid = to
			}
		}
	}
	t.Cleanup(func() { descendantsObserved = func(map[int][]observed) {} })
}
