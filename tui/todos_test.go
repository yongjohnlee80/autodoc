package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

var kbTodos = todo.At{Workspace: "kb"}

// seedTodos claims kb's store, adds tasks, and leaves it claimed or not.
func seedTodos(t *testing.T, d *daemon, claimed bool, specs ...todo.Spec) []todo.Task {
	t.Helper()
	ctx := context.Background()
	if err := d.todos.Claim(ctx, kbTodos); err != nil {
		t.Fatal(err)
	}
	if _, err := d.todos.Scaffold(ctx, kbTodos); err != nil {
		t.Fatal(err)
	}
	var out []todo.Task
	for _, s := range specs {
		task, err := d.todos.Add(ctx, kbTodos, s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, task)
	}
	if !claimed {
		if err := d.todos.Release(ctx, kbTodos); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// openTodos opens the todos panel and the workspace's store, and puts the cursor on its section n
// (1–5), opened: its tasks read.
func openTodos(t *testing.T, r *running, root string, n rune) {
	t.Helper()
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, root)
	r.keys(t, key('l')) // the store open: its sections
	r.s.WaitForText(t, "In progress")
	r.keys(t, key(n), key('l')) // the section, opened: its tasks read
}

// TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover: SPC x opens the panel, the workspace's
// store its first root, marked read-only while the daemon has not claimed it; its open tasks are
// numbered, and a store not claimed takes no writes here. SPC x again closes the panel.
func TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, false, todo.Spec{Title: "Write the view", Priority: "high"})
	r := attached(t, d)
	openTodos(t, r, "kb  (read-only", '1') // the label is cut to the panel's width
	r.s.WaitForText(t, "1  Write the view")
	r.s.WaitForText(t, "1 tasks")
	r.keys(t, key('a'))
	r.waitNotice(t, "read-only in AutoDoc until the cutover")
	if strings.Contains(r.s.String(), "new task") {
		t.Error("a read-only store opened the new task's title")
	}
	r.leader(t, 'x')
	r.s.WaitFor(t, "the panel closed", func(sc string) bool { return !strings.Contains(sc, "Write the view") })
}

// TestTodosAddsCyclesAndRemoves: on a claimed store, marked ●, a adds, s cycles the status, d
// removes after asking; each lands in the store, and the sections follow it. d is the panel's
// alone: with the keyboard in the page it asks nothing.
func TestTodosAddsCyclesAndRemoves(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "● kb", '1')
	r.s.WaitForText(t, "0 tasks")
	r.keys(t, key('a'))
	r.s.WaitForText(t, "new task")
	r.keys(t, decltest.Type("Ship the todos")...)
	r.keys(t, enter())
	r.s.WaitForText(t, "1  Ship the todos")
	listed := func(bucket string) []todo.Task {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{bucket}})
		if err != nil {
			t.Fatal(err)
		}
		return l.Tasks
	}
	if got := listed("open"); len(got) != 1 || got[0].Title != "Ship the todos" {
		t.Fatalf("open after a: %+v", got)
	}
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status:") // the task under the cursor: the details show it (a key and the move are not ordered)
	r.keys(t, key('s'))           // open → completed
	r.s.WaitFor(t, "completed", func(string) bool { return len(listed("completed")) == 1 })
	r.s.WaitForText(t, "0 tasks")
	r.keys(t, key('4'), key('l')) // Completed, opened
	r.s.WaitForText(t, "Ship the todos")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status:   completed")
	// d with the keyboard in the page is not the panel's
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editor", "forceActiveFocus")); return true })
	r.keys(t, key('d'))
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(r.s.String(), "remove the task?") {
		t.Fatal("d in the page asked to remove a task")
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoTree", "forceActiveFocus")); return true })
	r.keys(t, key('d'))
	r.s.WaitForText(t, "remove the task?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "removed", func(string) bool { return len(listed("completed")) == 0 })
}

// TestTodosArchiveIsReadByMonth: Archived lists its months, and a month's tasks are read only once
// it is opened.
func TestTodosArchiveIsReadByMonth(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Old and done", Status: "archived"}, todo.Spec{Title: "Still open"})
	r := attached(t, d)
	openTodos(t, r, "● kb", '5')
	month := time.Now().Format("2006/01")
	r.s.WaitForText(t, month)
	if strings.Contains(r.s.String(), "Old and done") {
		t.Fatal("an archived task was read before its month was opened")
	}
	r.keys(t, key('j'), key('l')) // the month, opened
	r.s.WaitForText(t, "Old and done")
}

// TestTodosRefreshesOnAnotherClientsChange: todos.changed from another client reads the open
// section again, so the panel never shows a stale list.
func TestTodosRefreshesOnAnotherClientsChange(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "● kb", '1')
	r.s.WaitForText(t, "0 tasks")
	// another client's add arrives as an event, as sys.events carries it
	if _, err := d.todos.Add(context.Background(), kbTodos, todo.Spec{Title: "From an agent"}); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { return r.h.reg.event("todos.changed", "kb", "x") })
	r.s.WaitForText(t, "From an agent")
}
