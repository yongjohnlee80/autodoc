//go:build !windows

package todo

import "testing"

// storeRootOf is paths.Join(root, todoDir)'s inverse by whole segments, the volume's root included:
// the store at /.todo-list is /'s, never "" (no folder a root can be).
func TestAStoresRootIsItsFolderAVolumesRootIncluded(t *testing.T) {
	for p, want := range map[string]string{"/.todo-list": "/", "/p/.todo-list": "/p", "/a/b/.todo-list": "/a/b"} {
		if got, ok := storeRootOf(p); !ok || got != want {
			t.Errorf("storeRootOf(%q) = %q, %v; want %q", p, got, ok, want)
		}
	}
	for _, p := range []string{"/p/x", "/p/.todo-lists", "p/.todo-list", ".todo-list", "/"} {
		if got, ok := storeRootOf(p); ok {
			t.Errorf("storeRootOf(%q) = %q; want none", p, got)
		}
	}
}
