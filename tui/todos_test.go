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

// openTodos opens the todos panel on the workspace's store (its dropdown showing label) and shows
// tab n (1–5), its tasks read.
func openTodos(t *testing.T, r *running, label string, n rune) {
	t.Helper()
	r.s.WaitFor(t, "kb entered", func(string) bool { return onLoop(r, func() bool { return r.h.ws == "kb" }) })
	r.leader(t, 'x')
	r.s.WaitForText(t, label)
	r.s.WaitForText(t, "In progress")
	tabRead(t, r, 0)
	r.keys(t, key(n))
	tabRead(t, r, int(n-'1'))
}

// tabRead waits for tab's tasks (for Archived, its months) to have been read.
func tabRead(t *testing.T, r *running, tab int) {
	t.Helper()
	r.s.WaitFor(t, todoBuckets[tab].title+" read", func(string) bool {
		return onLoop(r, func() bool { v := todosOf(r); return v.tab == tab && !v.stale[tab] })
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

// listedIn is how many tasks the daemon has in bucket of kb's store.
func listedIn(t *testing.T, d *daemon, bucket string) []todo.Task {
	t.Helper()
	l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{bucket}})
	if err != nil {
		t.Fatal(err)
	}
	return l.Tasks
}

// TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover: SPC x opens the panel on the workspace's
// store, its tasks numbered in the Open tab. On a store not claimed the action row offers nothing,
// only its note, and every write's key does nothing: no dialog, no notice, no error. SPC x again
// closes the panel.
func TestTodosShowsTheWorkspacesStoreReadOnlyUntilTheCutover(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, false, todo.Spec{Title: "Write the view", Priority: "high"})
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "Write the view")
	r.s.WaitForText(t, "status: open")
	r.s.WaitForText(t, "read-only until the cutover")
	r.keys(t, key('a'), key('s'), key('d'), key('A'), key('M'))
	time.Sleep(300 * time.Millisecond)
	sc := r.s.String()
	for _, absent := range []string{"[ Add ]", "[ Status… ]", "[ Remove ]", "[ Assign ]",
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

// TestTodosAddsChoosesAStatusAndRemoves: on a claimed store a adds a task, shown in Open under the
// cursor; s's picker moves it to the status chosen (in progress, deferred, completed), the tab of
// that status showing with the cursor on it; d removes after asking. Each lands in the store. d is
// the panel's alone: with the keyboard in the page it asks nothing.
func TestTodosAddsChoosesAStatusAndRemoves(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "[ Add ]") // an empty tab: Add alone
	r.keys(t, key('a'))
	r.s.WaitForText(t, "new task")
	r.keys(t, decltest.Type("Ship the todos")...)
	r.keys(t, enter())
	r.s.WaitForText(t, "Ship the todos")
	if got := listedIn(t, d, "open"); len(got) != 1 || got[0].Title != "Ship the todos" {
		t.Fatalf("open after a: %+v", got)
	}
	r.s.WaitForText(t, "status: open") // the cursor on the task added
	r.s.WaitForText(t, "[ Status… ] [ Assign ] [ Remove ] [ Add ]")
	for _, status := range []string{"in-progress", "deferred", "completed"} {
		chooseStatusOf(t, r, status)
		r.s.WaitFor(t, status, func(string) bool { return len(listedIn(t, d, status)) == 1 })
		r.s.WaitForText(t, "status: "+status) // the cursor followed the task to its tab
		if got := onLoop(r, func() int { return todosOf(r).tab }); got != tabOf(status) {
			t.Fatalf("after %s the tab shown is %d", status, got)
		}
	}
	// d with the keyboard in the page is not the panel's
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editor", "forceActiveFocus")); return true })
	r.keys(t, key('d'))
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(r.s.String(), "remove the task?") {
		t.Fatal("d in the page asked to remove a task")
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoCompletedTable", "forceActiveFocus")); return true })
	r.keys(t, key('d'))
	r.s.WaitForText(t, "remove the task?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "removed", func(string) bool { return len(listedIn(t, d, "completed")) == 0 })
}

// TestTodosStatusPickerArchivesATask: archived is one of s's statuses: the task is filed under its
// month, as the daemon files it, and the Archived tab shows that month with the cursor on it.
func TestTodosStatusPickerArchivesATask(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Old news"})
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "status: open")
	chooseStatusOf(t, r, "archived")
	month := time.Now().Format("2006/01")
	r.s.WaitFor(t, "archived", func(string) bool { return len(listedIn(t, d, "archived/"+month)) == 1 })
	r.s.WaitForText(t, month)
	r.s.WaitForText(t, "status: archived")
}

// TestTodosArchiveIsReadByMonth: Archived lists its months, and a month's tasks are read only once
// it is opened.
func TestTodosArchiveIsReadByMonth(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Old and done", Status: "archived"}, todo.Spec{Title: "Still open"})
	r := attached(t, d)
	openTodos(t, r, "kb", '5')
	month := time.Now().Format("2006/01")
	r.s.WaitForText(t, month)
	if strings.Contains(r.s.String(), "Old and done") {
		t.Fatal("an archived task was read before its month was opened")
	}
	r.keys(t, enter()) // the month, opened
	r.s.WaitForText(t, "Old and done")
}

// TestTodosRefreshesOnAnotherClientsChange: todos.changed from another client reads the tab shown
// again, so the panel never shows a stale list.
func TestTodosRefreshesOnAnotherClientsChange(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true)
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	// another client's add arrives as an event, as sys.events carries it
	if _, err := d.todos.Add(context.Background(), kbTodos, todo.Spec{Title: "From an agent"}); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { return r.h.reg.event("todos.changed", "kb", "x") })
	r.s.WaitForText(t, "From an agent")
}

// TestTodosStoreDropdownShowsOneStore: the dropdown shows the store shown alone; another chosen in
// it (Enter opens it, ↓ and Enter choose) shows its own tasks, and the first's are gone.
func TestTodosStoreDropdownShowsOneStore(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "A kb task"})
	ctx := context.Background()
	other := todo.At{Root: "/other"}
	d.todoDisk.MkdirAll("/other")
	if _, err := d.todos.Register(ctx, "/other"); err != nil {
		t.Fatal(err)
	}
	if err := d.todos.Claim(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := d.todos.Scaffold(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := d.todos.Add(ctx, other, todo.Spec{Title: "Another store's task"}); err != nil {
		t.Fatal(err)
	}
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "A kb task")
	if strings.Contains(r.s.String(), "other") {
		t.Fatalf("a store not shown is on the screen:\n%s", r.s.String())
	}
	r.keys(t, key('S')) // the dropdown, focused
	r.s.WaitFor(t, "the dropdown focused", func(string) bool {
		return onLoop(r, func() bool { c, ok := r.h.p.Find("todoStore"); return ok && r.h.p.App().FocusWithin(c) })
	})
	r.keys(t, enter())
	r.s.WaitForText(t, "other")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, enter())
	r.s.WaitForText(t, "Another store's task")
	r.s.WaitFor(t, "kb's task gone", func(sc string) bool { return !strings.Contains(sc, "A kb task") })
}

// panelFrame is where the todos panel's frame is on the screen: its heading's row and its left and
// right corners' columns, or -1.
func panelFrame(sc string) (row, left, right int) {
	for i, line := range strings.Split(sc, "\n") {
		if j := strings.Index(line, "┌ todos"); j >= 0 {
			runes := []rune(line)
			l := len([]rune(line[:j]))
			for k := l + 1; k < len(runes); k++ {
				if runes[k] == '┐' {
					return i, l, k
				}
			}
			return i, l, -1
		}
	}
	return -1, -1, -1
}

// TestTodosPanelOpensLargeWithOneActionRow: on an 80 × 40 screen the panel opens large at the left
// edge, 65% of the width (52 columns) and 80% of the height (32 rows, centred); its top bar is the
// store's dropdown and the icons on one line; on a task its actions are one row, Status…, Assign,
// Remove and Add, whole, and nothing else. A click on Status… opens the picker, and the task moves
// to the status chosen, the cursor following it. A size the user gives the panel is kept, and wins.
func TestTodosPanelOpensLargeWithOneActionRow(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 40)
	r.ready(t)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "status: open")
	r.s.WaitFor(t, "the panel at its default", func(sc string) bool {
		row, left, right := panelFrame(sc)
		return row == 4 && left == 0 && right == 51
	})
	r.s.WaitFor(t, "the top bar on one line", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "kb") && strings.Contains(line, "[ ✚ ]") && strings.Contains(line, "[ ⎘ ]") &&
				strings.Contains(line, "[ ✎ ]") && strings.Contains(line, "[ ⟳ ]") {
				return true
			}
		}
		return false
	})
	var x, y int
	r.s.WaitFor(t, "the task's action row", func(sc string) bool {
		for i, line := range strings.Split(sc, "\n") {
			if j := strings.Index(line, "[ Status… ] [ Assign ] [ Remove ] [ Add ]"); j >= 0 {
				x, y = len([]rune(line[:j])), i
				return true
			}
		}
		return false
	})
	sc := r.s.String()
	for _, absent := range []string{"Preview (i)", "Status ▶", "◀ Status", "read-only until"} {
		if strings.Contains(sc, absent) {
			t.Errorf("on a task the panel shows %q:\n%s", absent, sc)
		}
	}
	// the pointer comes to the button first, as a mouse does: a press with no motion before it, at a
	// new place, can be lost on a button with a tooltip (golib)
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseMotion, X: x + 2, Y: y})
	r.s.WaitFor(t, "the pointer on Status…", func(string) bool {
		return onLoop(r, func() bool { c, ok := r.h.p.Find("todoStatusButton"); return ok && hoveredWithin(r, c) })
	})
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x + 2, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + 2, Y: y})
	r.s.WaitForText(t, "statuses (5 of 5)")
	r.keys(t, decltest.Type("in-p")...)
	r.s.WaitForText(t, "statuses (1 of 5)")
	r.keys(t, enter())
	r.s.WaitFor(t, "moved on", func(string) bool { return len(listedIn(t, d, "in-progress")) == 1 })
	r.s.WaitFor(t, "the cursor on the moved task", func(string) bool {
		return onLoop(r, func() bool {
			_, x := todosOf(r).cursor()
			return str(x, "title") == "Ship it" && str(x, "status") == "in-progress"
		})
	})
	// the user's size, kept, wins: 40% of 80 columns, the whole height
	onLoop(r, func() bool { r.h.panelResized("todos", 40, 100); return true })
	r.s.WaitFor(t, "the panel at the user's size", func(sc string) bool {
		row, left, right := panelFrame(sc)
		return row == 0 && left == 0 && right == 31
	})
	if geo := onLoop(r, func() [2]int { s, l := r.h.prefs.panelGeometry("todos", "left"); return [2]int{s, l} }); geo != [2]int{40, 100} {
		t.Errorf("the size kept: %v, want [40 100]", geo)
	}
}

// TestTodosSharesTheLeftEdgeWithTheExplorer: the explorer and the todos both open at the left edge
// by default; as two drawers on one edge do, each opens over the page, the one opened later drawn
// over the other, and Ctrl+l from either goes back to the page.
func TestTodosSharesTheLeftEdgeWithTheExplorer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 100, 30)
	r.ready(t)
	r.leader(t, 'e')
	r.s.WaitForText(t, "┌ explorer")
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "Ship it")
	if got := onLoop(r, func() map[string]string { return r.h.panelEdges() }); got["explorer"] != "left" || got["todos"] != "left" {
		t.Fatalf("edges %v", got)
	}
	open := onLoop(r, func() [2]bool { return [2]bool{r.h.panelOpen["explorer"], r.h.panelOpen["todos"]} })
	if open != [2]bool{true, true} {
		t.Fatalf("open %v: the todos closed the explorer", open)
	}
	row, left, _ := panelFrame(r.s.String())
	if left != 0 || row < 0 {
		t.Fatalf("the todos panel at row %d, column %d", row, left)
	}
}

// hoveredWithin reports whether the pointer rests on c (the node under it is c's).
func hoveredWithin(_ *running, c tuicore.Component) bool {
	n, ok := c.(interface{ Context() *tuicore.Context })
	return ok && n.Context() != nil && n.Context().Hovered()
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
	openTodos(t, r, "kb", '1')
	shown := func(sc string) bool {
		return strings.Contains(sc, "todos — keys") && strings.Contains(sc, "MOVE") && strings.Contains(sc, "TABS") &&
			strings.Contains(sc, "STORE") && strings.Contains(sc, "s  status…: move the task") && strings.Contains(sc, "?, q or Esc closes")
	}
	closed := func(sc string) bool { return !strings.Contains(sc, "todos — keys") }
	for _, closer := range []tuicore.Event{key('?'), esc()} {
		r.keys(t, key('?'))
		r.s.WaitFor(t, "the card open", shown)
		r.keys(t, closer)
		r.s.WaitFor(t, "the card closed", closed)
	}
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card open", shown)
	r.keys(t, key(']')) // a key on the card: the card closes, and ] shows the next tab
	r.s.WaitFor(t, "the card closed by its key", closed)
	tabRead(t, r, 1)
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card open", shown)
	r.keys(t, key('a'))
	r.s.WaitForText(t, "new task")
}

// TestTodosKeysAreTheListsNotTheDetails: with the keyboard in the details pane, the panel's keys do
// nothing — s opens no picker, d asks to remove nothing, i hides nothing — since a key read there
// must never change the task being read. The details' read-only Editor lets the edit keys it refuses
// (a d s i o) bubble. In the tab's table they still work, and on the action row's buttons too.
func TestTodosKeysAreTheListsNotTheDetails(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Read me"})
	r := attached(t, d)
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "status: open")
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoDetails", "forceActiveFocus")); return true })
	r.keys(t, key('s'), key('d'), key('i'), key('a'))
	time.Sleep(300 * time.Millisecond)
	if sc := r.s.String(); strings.Contains(sc, "remove the task?") || strings.Contains(sc, "new task") || strings.Contains(sc, "statuses (") ||
		!strings.Contains(sc, "status: open") || len(listedIn(t, d, "open")) != 1 {
		t.Fatalf("keys in the details pane acted on the task:\n%s", sc)
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoOpenTable", "forceActiveFocus")); return true })
	chooseStatusOf(t, r, "in-progress") // the table's s
	r.s.WaitFor(t, "in progress", func(string) bool { return len(listedIn(t, d, "in-progress")) == 1 })
	r.s.WaitForText(t, "status: in-progress") // the cursor followed the task to its tab
	// and the action row's below the details: the same keys, on a button
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todoStatusButton", "forceActiveFocus")); return true })
	chooseStatusOf(t, r, "completed")
	r.s.WaitFor(t, "completed, from a task button", func(string) bool { return len(listedIn(t, d, "completed")) == 1 })
}
