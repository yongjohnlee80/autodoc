//go:build windows

package todotest_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// On Windows a store at a drive's root, as core/todo opens it (the slash form, C:/kb), is claimed,
// scaffolded and given a task on the in-memory disk, and the task's file is under that root.
func TestAStoreAtADrivesRootIsClaimedOnTheDisk(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("C:/kb")
	m := todo.New(todotest.Roots{"kb": "C:/kb"}, disk, todotest.NewJournal())
	kb := todo.At{Workspace: "kb"}
	if err := m.Claim(ctx, kb); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := m.Scaffold(ctx, kb); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	task, err := m.Add(ctx, kb, todo.Spec{Title: "Ship"}, "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := readBack(t, disk, "C:/kb/.todo-list", task.Path); got == "" {
		t.Errorf("the task's file %s is not under C:/kb/.todo-list", task.Path)
	}
}
