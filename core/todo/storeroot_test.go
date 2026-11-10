package todo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// A workspace whose root is a volume's root lists its store under that root: todos.stores answers
// "/" for /.todo-list, and on Windows "C:/" for a drive's and "//host/share/" for a share's, never
// "" or "C:", which no client can send back as a root.
func TestAStoreAtAVolumesRootListsThatRoot(t *testing.T) {
	roots := []string{paths.Slash(filepath.VolumeName(os.TempDir()) + string(filepath.Separator))}
	if filepath.Separator == '\\' {
		roots = append(roots, "//host/share/")
	}
	for _, root := range roots {
		m := todo.New(todotest.Roots{"vol": root}, todotest.NewDisk(), todotest.NewJournal())
		t.Cleanup(func() { m.Close() })
		vol := todo.At{Workspace: "vol"}
		if err := m.Claim(ctx, vol); err != nil {
			t.Fatalf("%s: %v", root, err)
		}
		if _, err := m.Scaffold(ctx, vol); err != nil {
			t.Fatalf("%s: %v", root, err)
		}
		stores, err := m.Stores(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range stores {
			if s.Path == paths.Join(root, ".todo-list") {
				found = true
				if len(s.Roots) != 1 || s.Roots[0] != root {
					t.Errorf("the store at %s lists roots %q, want [%s]", s.Path, s.Roots, root)
				}
			}
		}
		if !found {
			t.Errorf("no store at %s among %+v", paths.Join(root, ".todo-list"), stores)
		}
	}
}
