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
	openTodos(t, r, "· ws kb  (read-only", '1') // the label is cut to the panel's width
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
	openTodos(t, r, "· ws kb", '1')
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
	r.s.WaitForText(t, "status: open") // the task under the cursor: the details show it (a key and the move are not ordered)
	r.keys(t, key('s'))                // open → in progress, the cursor following the task
	r.s.WaitFor(t, "in progress", func(string) bool { return len(listed("in-progress")) == 1 })
	r.s.WaitForText(t, "status: in-progress")
	r.keys(t, key('>')) // a step on: completed, the cursor following again
	r.s.WaitFor(t, "completed", func(string) bool { return len(listed("completed")) == 1 })
	r.s.WaitForText(t, "status: completed")
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
	openTodos(t, r, "· ws kb", '5')
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
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "0 tasks")
	// another client's add arrives as an event, as sys.events carries it
	if _, err := d.todos.Add(context.Background(), kbTodos, todo.Spec{Title: "From an agent"}); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { return r.h.reg.event("todos.changed", "kb", "x") })
	r.s.WaitForText(t, "From an agent")
}

// TestTodosTaskButtonsFitTheNarrowestPanel: at its minimum width on an 80-column screen the task
// buttons wrap, every one whole, and a click runs its key's action: Status ▶ moves the task on, the
// cursor following it.
func TestTodosTaskButtonsFitTheNarrowestPanel(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 40)
	r.ready(t)
	openTodos(t, r, "· ws kb", '1')
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status: open")
	onLoop(r, func() bool { r.h.panelResized("todos", 25, 100); return true })
	// each whole: its decoration's both ends on the screen, so nothing of it is cut
	for _, b := range []string{"Preview (i)", "Add (a)", "◀ Status (<)", "Status ▶ (>)", "Remove (d)", "Refresh (R)"} {
		r.s.WaitForText(t, "[ "+b+" ]")
	}
	// the resize lands in steps (its size, then its length): click where the button is once the
	// panel has its final place, 25% of 80 columns at the right edge, its whole height (its heading
	// at row 0, column 60), not where an earlier step drew it
	var x, y int
	r.s.WaitFor(t, "the panel at its minimum", func(sc string) bool {
		lines := strings.Split(sc, "\n")
		if i := strings.Index(lines[0], "┌ todos"); i < 0 || len([]rune(lines[0][:i])) != 60 {
			return false
		}
		for i, line := range lines {
			if j := strings.Index(line, "[ Status ▶ (>) ]"); j >= 0 {
				x, y = len([]rune(line[:j])), i
				return true
			}
		}
		return false
	})
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x + 2, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + 2, Y: y})
	r.s.WaitFor(t, "moved on", func(string) bool {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{"in-progress"}})
		return err == nil && len(l.Tasks) == 1
	})
	// the cursor followed it into In progress: its details are its (the narrow pane wraps the line)
	r.s.WaitFor(t, "the cursor on the moved task", func(string) bool {
		return onLoop(r, func() bool {
			_, x := todosOf(r).cursor()
			return str(x, "title") == "Ship it" && str(x, "status") == "in-progress"
		})
	})
}

// todosOf is the running TUI's todos feature.
func todosOf(r *running) *todos {
	for _, f := range r.h.reg.features {
		if v, ok := f.(*todos); ok {
			return v
		}
	}
	return nil
}
