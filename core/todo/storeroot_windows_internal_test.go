package todo

import "testing"

// On Windows a drive's and a share's root keep their volume: C:/.todo-list is C:/'s (never "C:",
// which names the drive's current folder), //host/share/.todo-list is //host/share/'s.
func TestAStoresRootAtADrivesOrAShareRoot(t *testing.T) {
	for p, want := range map[string]string{"C:/.todo-list": "C:/", "C:/p/.todo-list": "C:/p", "//host/share/.todo-list": "//host/share/",
		"//host/share/p/.todo-list": "//host/share/p"} {
		if got, ok := storeRootOf(p); !ok || got != want {
			t.Errorf("storeRootOf(%q) = %q, %v; want %q", p, got, ok, want)
		}
	}
	for _, p := range []string{"C:.todo-list", "/.todo-list", "C:/p/x"} {
		if got, ok := storeRootOf(p); ok {
			t.Errorf("storeRootOf(%q) = %q; want none", p, got)
		}
	}
}
