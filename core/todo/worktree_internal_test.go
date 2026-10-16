package todo

import "testing"

// movedFromWorktree's guards: a scope without the disk's functions, and a path none of whose
// directories exist (every store's disk has a root, so only a scope like this one reaches it).
func TestMovedFromWorktreeGuards(t *testing.T) {
	if to, ok := (scope{}).movedFromWorktree("/repo/feat/a.md"); ok {
		t.Errorf("a scope without the disk moved a ref to %q", to)
	}
	none := scope{exists: func(string) bool { return false }, read: func(string) ([]byte, bool) { return nil, false },
		list: func(string) []string { return nil }}
	for _, p := range []string{"/repo/feat/a.md", "relative/a.md"} {
		if to, ok := none.movedFromWorktree(p); ok {
			t.Errorf("%s moved to %q on a disk with nothing", p, to)
		}
	}
}
