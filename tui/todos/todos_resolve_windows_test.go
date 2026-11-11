//go:build windows

package todos

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// On Windows a store at a drive's or a share's root has that root, kept a root (C:/, //host/share/),
// and a home at a drive's root names a folder under it ~/notes.
//
// Moved from tui/todos_resolve_windows_test.go with its name and asserts unchanged.
func TestTheRootsAndHomeOfAStoreAtADrivesRoot(t *testing.T) {
	for p, want := range map[string]string{"C:/.todo-list": "C:/", "//host/share/.todo-list": "//host/share/", "C:/p/.todo-list": "C:/p"} {
		if got := storeRoot(p); got != want {
			t.Errorf("storeRoot(%q) = %q, want %q", p, got, want)
		}
	}
	if got := tildePath(procenv.Env(nil).With("USERPROFILE=C:\\"), "C:/notes"); got != "~/notes" {
		t.Errorf("tildePath under a home at C:\\: %q", got)
	}
}
