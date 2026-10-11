package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
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

// TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover: SPC x opens the workspace's store, its
// open tasks listed and numbered; a store the daemon has not claimed takes no writes here.
func TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, false, todo.Spec{Title: "Write the view", Priority: "high"})
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, "todos · kb  (read-only until the cutover)")
	r.s.WaitForText(t, "Write the view")
	r.s.WaitForText(t, "1 tasks")
	r.keys(t, key('a'))
	r.waitNotice(t, "read-only in AutoDoc until the cutover")
	if strings.Contains(r.s.String(), "new task") {
		t.Error("a read-only store opened the new task's title")
	}
}

// TestTodosAddsCyclesAndRemoves: on a claimed store a adds, s cycles the status, d removes after
// asking; each lands in the store, and the tabs follow it.
func TestTodosAddsCyclesAndRemoves(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, "todos · kb")
	r.keys(t, key('a'))
	r.s.WaitForText(t, "new task")
	r.keys(t, decltest.Type("Ship the todos")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitForText(t, "Ship the todos")
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
	r.keys(t, key('s')) // open → completed
	r.s.WaitFor(t, "completed", func(string) bool { return len(listed("completed")) == 1 })
	r.s.WaitForText(t, "0 tasks")
	r.keys(t, key('4'))
	r.s.WaitForText(t, "Ship the todos")
	r.keys(t, key('d'))
	r.s.WaitForText(t, "remove the task?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "removed", func(string) bool { return len(listed("completed")) == 0 })
}

// TestTodosArchiveIsReadByMonth: the Archived tab lists months with their counts and reads a
// month's tasks only once it is opened; Backspace is the months again.
func TestTodosArchiveIsReadByMonth(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	tasks := seedTodos(t, d, true, todo.Spec{Title: "Old and done", Status: "archived"}, todo.Spec{Title: "Still open"})
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, "Still open")
	r.keys(t, key('5'))
	month := time.Now().Format("2006/01")
	r.s.WaitForText(t, month)
	if strings.Contains(r.s.String(), "Old and done") {
		t.Fatal("an archived task was read before its month was opened")
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitForText(t, "Old and done")
	// Backspace is the months again: Enter there opens the month, not a task
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyBackspace})
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitForText(t, "1 tasks")
	if tasks[0].Status != "archived" {
		t.Fatalf("seeded %+v", tasks[0])
	}
}

// TestTodosRefreshesOnAnotherClientsChange: todos.changed from another client reads the store
// again, so the view never shows a stale list.
func TestTodosRefreshesOnAnotherClientsChange(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, "0 tasks")
	// another client's add arrives as an event, as sys.events carries it
	if _, err := d.todos.Add(context.Background(), kbTodos, todo.Spec{Title: "From an agent"}); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { return r.h.reg.event("todos.changed", "kb", "x") })
	r.s.WaitForText(t, "From an agent")
}

// fakeCore records what a feature asks of the host.
type fakeCore struct {
	Core
	opened, notes []string
}

func (f *fakeCore) OpenFile(abs string) { f.opened = append(f.opened, abs) }
func (f *fakeCore) Close(string)        {}
func (f *fakeCore) Notify(msg string)   { f.notes = append(f.notes, msg) }
func (f *fakeCore) Set(string, any)     {}

// TestEnterOpensTheTasksFileInTheStore: Enter opens the task's own file, under the store's
// directory, wherever the store is.
func TestEnterOpensTheTasksFileInTheStore(t *testing.T) {
	f := &fakeCore{}
	v := newTodos(f)
	v.store = "/p/.todo-list"
	v.tasks[0] = []map[string]any{{"id": "t1", "path": "open/t1.md"}}
	v.activated(0)
	v.activated(5) // no such row: nothing
	if len(f.opened) != 1 || f.opened[0] != "/p/.todo-list/open/t1.md" {
		t.Errorf("opened %v", f.opened)
	}
	v.remove() // read-only: says so, asks nothing
	if len(f.notes) != 1 || !strings.Contains(f.notes[0], "read-only") {
		t.Errorf("notes %v", f.notes)
	}
}
