//go:build !windows

package todo_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// A workspace whose root is a volume's root lists its store under that root: todos.stores answers
// "/" for /.todo-list, never "", which no client can send back as a root.
func TestAStoreAtAVolumesRootListsThatRoot(t *testing.T) {
	m := todo.New(todotest.Roots{"vol": "/"}, todotest.NewDisk(), todotest.NewJournal())
	t.Cleanup(func() { m.Close() })
	vol := todo.At{Workspace: "vol"}
	if err := m.Claim(ctx, vol); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Scaffold(ctx, vol); err != nil {
		t.Fatal(err)
	}
	stores, err := m.Stores(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stores {
		if s.Path == "/.todo-list" {
			if len(s.Roots) != 1 || s.Roots[0] != "/" {
				t.Errorf("the store at /.todo-list lists roots %q, want [/]", s.Roots)
			}
			return
		}
	}
	t.Errorf("no store at /.todo-list among %+v", stores)
}
