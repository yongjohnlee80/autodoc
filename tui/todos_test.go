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

// sectionRead waits for the todos' section of the workspace's store to have been asked for: a key
// after it acts on the section as read, a change reads it again.
func sectionRead(t *testing.T, r *running, bucket string) {
	t.Helper()
	r.s.WaitFor(t, bucket+" read", func(string) bool {
		return onLoop(r, func() bool { return todosOf(r).open[bucketKey("ws:kb", bucket)] })
	})
}

// chooseStatusOf is s on the task under the cursor, the picker narrowed to status by typing it, and
// Enter: the task moves there.
func chooseStatusOf(t *testing.T, r *running, status string) {
	t.Helper()
	r.keys(t, key('s'))
	r.s.WaitForText(t, "statuses (5 of 5)")
	r.keys(t, decltest.Type(status)...)
	r.s.WaitForText(t, "statuses (1 of 5)")
	r.keys(t, enter())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "statuses (") })
}

// TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover: SPC x opens the panel, the workspace's
// store its first root, marked read-only while the daemon has not claimed it; its open tasks are
// numbered. On a store not claimed the action row offers nothing, only its note, and every write's
// key does nothing: no dialog, no notice, no error. SPC x again closes the panel.
func TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, false, todo.Spec{Title: "Write the view", Priority: "high"})
	r := attached(t, d)
	openTodos(t, r, "· ws kb  (read-only", '1')
	r.s.WaitForText(t, "1  Write the view")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status: open")
	// the tree's label, and the action row's note: no action
	r.s.WaitFor(t, "the action row's note", func(sc string) bool { return strings.Count(sc, "read-only until the cutover") >= 2 })
	r.keys(t, key('a'), key('s'), key('d'), key('A'), key('M'))
	time.Sleep(300 * time.Millisecond)
	sc := r.s.String()
	for _, absent := range []string{"[ Add (a) ]", "[ Status… (s) ]", "[ Remove (d) ]", "[ Assign (A) ]", "[ Refresh (R) ]",
		"new task", "statuses (", "remove the task?", "assign to an agent", "move the store"} {
		if strings.Contains(sc, absent) {
			t.Errorf("a read-only store shows %q:\n%s", absent, sc)
		}
	}
	if r.noticed("todos") || r.noticed("read-only") {
		t.Errorf("a key on a read-only store was answered with a notice: %+v", onLoop(r, func() []notice { return r.h.noticeLog.items }))
	}
	r.leader(t, 'x')
	r.s.WaitFor(t, "the panel closed", func(sc string) bool { return !strings.Contains(sc, "Write the view") })
}

// TestTodosAddsChoosesAStatusAndRemoves: on a claimed store, marked ●, a adds, s's picker moves the
// task to the status chosen (in progress, deferred, completed), d removes after asking; each lands
// in the store, and the sections follow it, the cursor with the task. d is the panel's alone: with
// the keyboard in the page it asks nothing.
func TestTodosAddsChoosesAStatusAndRemoves(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "· ws kb", '1')
	sectionRead(t, r, "open")
	r.s.WaitForText(t, "[ Add (a) ] [ Refresh (R) ]") // a section's actions
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
	r.s.WaitForText(t, "[ Status… (s) ] [ Assign (A) ] [ Remove (d) ]")
	for _, status := range []string{"in-progress", "deferred", "completed"} {
		chooseStatusOf(t, r, status)
		r.s.WaitFor(t, status, func(string) bool { return len(listed(status)) == 1 })
		r.s.WaitForText(t, "status: "+status) // the cursor followed the task
	}
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

// TestTodosStatusPickerArchivesATask: archived is one of s's statuses: the task is filed under its
// month, as the daemon files it.
func TestTodosStatusPickerArchivesATask(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Old news"})
	r := attached(t, d)
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "1  Old news")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status: open")
	chooseStatusOf(t, r, "archived")
	month := "archived/" + time.Now().Format("2006/01")
	r.s.WaitFor(t, "archived", func(string) bool {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{month}})
		return err == nil && len(l.Tasks) == 1 && l.Tasks[0].Title == "Old news"
	})
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
	sectionRead(t, r, "open")
	// another client's add arrives as an event, as sys.events carries it
	if _, err := d.todos.Add(context.Background(), kbTodos, todo.Spec{Title: "From an agent"}); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { return r.h.reg.event("todos.changed", "kb", "x") })
	r.s.WaitForText(t, "From an agent")
}

// panelHeading is where the todos panel's heading is on the screen: its row and column, or -1.
func panelHeading(sc string) (row, col int) {
	for i, line := range strings.Split(sc, "\n") {
		if j := strings.Index(line, "┌ todos"); j >= 0 {
			return i, len([]rune(line[:j]))
		}
	}
	return -1, -1
}

// TestTodosPanelOpensLargeWithOneActionRow: on an 80 × 40 screen the panel opens large, 65% of the
// width (52 columns, at the right edge) and 80% of the height (32 rows, centred); on a task its
// actions are one row, Status…, Assign and Remove, whole, and nothing else: no greyed button, no
// second row. A click on Status… opens the picker, and the task moves to the status chosen, the
// cursor following it. A size the user gives the panel is kept, and wins.
func TestTodosPanelOpensLargeWithOneActionRow(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 40)
	r.ready(t)
	openTodos(t, r, "· ws kb", '1')
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status: open")
	r.s.WaitFor(t, "the panel at its default", func(sc string) bool {
		row, col := panelHeading(sc)
		return row == 4 && col == 28
	})
	var x, y int
	r.s.WaitFor(t, "the task's action row", func(sc string) bool {
		for i, line := range strings.Split(sc, "\n") {
			if j := strings.Index(line, "[ Status… (s) ] [ Assign (A) ] [ Remove (d) ]"); j >= 0 {
				x, y = len([]rune(line[:j])), i
				return true
			}
		}
		return false
	})
	sc := r.s.String()
	for _, absent := range []string{"[ Add (a) ]", "[ Refresh (R) ]", "[ Label (L) ]", "Preview (i)", "Status ▶", "◀ Status", "read-only until"} {
		if strings.Contains(sc, absent) {
			t.Errorf("on a task the panel shows %q:\n%s", absent, sc)
		}
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x + 2, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + 2, Y: y})
	r.s.WaitForText(t, "statuses (5 of 5)")
	r.s.WaitForText(t, "● open")
	r.keys(t, decltest.Type("in-p")...)
	r.s.WaitForText(t, "statuses (1 of 5)")
	r.keys(t, enter())
	r.s.WaitFor(t, "moved on", func(string) bool {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{"in-progress"}})
		return err == nil && len(l.Tasks) == 1
	})
	r.s.WaitFor(t, "the cursor on the moved task", func(string) bool {
		return onLoop(r, func() bool {
			_, x := todosOf(r).cursor()
			return str(x, "title") == "Ship it" && str(x, "status") == "in-progress"
		})
	})
	// the user's size, kept, wins: 40% of 80 columns, the whole height
	onLoop(r, func() bool { r.h.panelResized("todos", 40, 100); return true })
	r.s.WaitFor(t, "the panel at the user's size", func(sc string) bool {
		row, col := panelHeading(sc)
		return row == 0 && col == 48
	})
	if geo := onLoop(r, func() [2]int { s, l := r.h.prefs.panelGeometry("todos", "right"); return [2]int{s, l} }); geo != [2]int{40, 100} {
		t.Errorf("the size kept: %v, want [40 100]", geo)
	}
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

// TestTodosQuestionMarkOpensTheKeysCard: ? in the panel opens the card of its keys, grouped by
// purpose, over the page as SPC's is; ? again closes it, and so does Esc; a key on it runs, the
// card closing first. The panel holds no inline list of keys.
func TestTodosQuestionMarkOpensTheKeysCard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "· ws kb", '1')
	sectionRead(t, r, "open")
	shown := func(sc string) bool {
		return strings.Contains(sc, "todos — keys") && strings.Contains(sc, "MOVE") && strings.Contains(sc, "STATUS") &&
			strings.Contains(sc, "s  status…: choose the task's") && strings.Contains(sc, "?, q or Esc closes")
	}
	closed := func(sc string) bool { return !strings.Contains(sc, "todos — keys") }
	for _, closer := range []tuicore.Event{key('?'), esc()} {
		r.keys(t, key('?'))
		r.s.WaitFor(t, "the card open", shown)
		if sc := r.s.String(); strings.Contains(sc, "a step back") || strings.Contains(sc, "these keys") {
			t.Fatalf("the old inline keys show:\n%s", sc)
		}
		r.keys(t, closer)
		r.s.WaitFor(t, "the card closed", closed)
	}
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card open", shown)
	r.keys(t, key('a')) // a key on the card: the card closes, and a adds
	r.s.WaitForText(t, "new task")
	r.s.WaitFor(t, "the card closed by its key", closed)
}

// TestTodosKeysAreTheListsNotTheDetails: with the keyboard in the details pane, the task list's keys
// do nothing — s opens no picker, d asks to remove nothing, i hides nothing — since a key read there
// must never change the task being read. The details' read-only Editor lets the edit keys it refuses
// (a d s i o) bubble. In the list they still work, and on the action row's buttons too.
func TestTodosKeysAreTheListsNotTheDetails(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Read me"})
	r := attached(t, d)
	openTodos(t, r, "· ws kb", '1')
	r.s.WaitForText(t, "1  Read me")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "status: open")
	listed := func(bucket string) int {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{bucket}})
		if err != nil {
			t.Fatal(err)
		}
		return len(l.Tasks)
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoDetails", "forceActiveFocus")); return true })
	r.keys(t, key('s'), key('d'), key('i'), key('a'))
	time.Sleep(300 * time.Millisecond)
	if sc := r.s.String(); strings.Contains(sc, "remove the task?") || strings.Contains(sc, "new task") || strings.Contains(sc, "statuses (") ||
		!strings.Contains(sc, "status: open") || listed("open") != 1 {
		t.Fatalf("keys in the details pane acted on the task (open %d):\n%s", listed("open"), sc)
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoTree", "forceActiveFocus")); return true })
	chooseStatusOf(t, r, "in-progress") // the list's s
	r.s.WaitFor(t, "in progress", func(string) bool { return listed("in-progress") == 1 })
	r.s.WaitForText(t, "status: in-progress") // the cursor followed the task (the follow lands after the move)
	// and the action row's below the details: the same keys, on a button
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoStatusButton", "forceActiveFocus")); return true })
	chooseStatusOf(t, r, "completed")
	r.s.WaitFor(t, "completed, from a task button", func(string) bool { return listed("completed") == 1 })
}
