//go:build !windows

package todo_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// On a Unix a backslash is a name's own character: a ref to a file whose name holds one is written
// as its root's variable with the name as it is, and found.
func TestARefWithABackslashInItsNameIsAsWritten(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/kb/.todo-list/open")
	disk.WriteFile(`/kb/adrs/a\b.md`, []byte("# a\n"))
	m := todo.New(todotest.Roots{"kb": "/kb"}, disk, todotest.NewJournal())
	ws := todo.At{Workspace: "kb"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	task, err := m.Add(ctx, ws, todo.Spec{Title: "t", ADR: []string{`/kb/adrs/a\b.md`}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.ADR) != 1 || task.ADR[0] != `$KB_ROOT/adrs/a\b.md` || len(task.Errors) != 0 {
		t.Errorf("the adr %q, errors %+v: want $KB_ROOT/adrs/a\\b.md, found", task.ADR, task.Errors)
	}
}
