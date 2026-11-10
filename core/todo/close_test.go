package todo_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// The Manager holds a store's directory open while it uses the store, and Close releases every
// directory it opened: on Windows a directory held open can be neither removed nor renamed, so a
// daemon that stopped, or a test that ended, would leave a workspace's .todo-list undeletable. A
// Manager used after Close opens its stores again.
func TestEveryDirectoryTheManagerOpenedIsReleasedAtClose(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/ws")
	m := todo.New(todotest.Roots{"ws": "/ws"}, disk, todotest.NewJournal())
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, ws, todo.Spec{Title: "t"}, ""); err != nil {
		t.Fatal(err)
	}
	if held := disk.Held(); held["/ws/.todo-list"] == 0 {
		t.Fatalf("held %v: the store in use is not held open, so this cell sees nothing", held)
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if held := disk.Held(); len(held) != 0 {
		t.Errorf("after Close, still held: %v", held)
	}
	if l, err := m.List(ctx, ws, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Errorf("the store used again after Close: %+v, %v", l, err)
	}
	if err := m.Close(); err != nil || len(disk.Held()) != 0 {
		t.Errorf("a second Close: %v, still held %v", err, disk.Held())
	}
}

// A store's own directory is released before it is renamed to its new place: on Windows the rename
// of a directory held open fails. The moved store is then used at its new path.
func TestAStoreIsReleasedBeforeItMoves(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/ws")
	m := todo.New(todotest.Roots{"ws": "/ws"}, disk, todotest.NewJournal())
	t.Cleanup(func() { m.Close() })
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, ws, todo.Spec{Title: "t"}, ""); err != nil {
		t.Fatal(err)
	}
	renames, heldAtRename := 0, -1
	disk.SetFault(func(op, name string) error {
		if op == "rename" && name == "/ws/.todo-list" {
			renames++
			heldAtRename = disk.Held()["/ws/.todo-list"]
		}
		return nil
	})
	r, err := m.MoveStore(ctx, ws, "/ws/tasks")
	disk.SetFault(nil)
	if err != nil {
		t.Fatalf("MoveStore: %v", err)
	}
	if renames == 0 || heldAtRename != 0 {
		t.Errorf("the store's directory as it was renamed (%d renames): held %d times, want 0", renames, heldAtRename)
	}
	if l, err := m.List(ctx, ws, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 || r.Store != "/ws/tasks" {
		t.Errorf("the moved store at %s: %+v, %v", r.Store, l, err)
	}
}
