package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// todos_labels_test.go: what a store is called (its derived name, the user's label, renamed in
// place), s's Status… picker with the cursor following the task to its tab, and the action row.

// TestStoreNamesDeriveFromTheirFolders: a KB folder is named for the project holding it; any other
// store for its own folder.
func TestStoreNamesDeriveFromTheirFolders(t *testing.T) {
	for root, want := range map[string]string{
		"/home/j/Source/LabelManager/.auto-agents/kb": "LabelManager",
		"/home/j/.config/nvim/.auto-agents-config/kb": "nvim",
		"/home/j/notes/kb/kb":                         "notes",
		"/home/j/notes/kb":                            "notes",
		"/home/j/html-archives":                       "html-archives",
		"/home/j/html-archives/":                      "html-archives",
		"/kb":                                         "kb",
		"/.auto-agents/kb":                            "kb",
	} {
		if got := storeName(root); got != want {
			t.Errorf("storeName(%q) = %q, want %q", root, got, want)
		}
	}
	if got := storeRoot("/p/.todo-list"); got != "/p" {
		t.Errorf("storeRoot = %q", got)
	}
}

// labelledStores is a daemon of three stores, two of them KB folders, keeping preferences as the
// daemon does: preference.set's values are preference.list's.
func labelledStores(prefs map[string]any) *scriptedCore {
	s := newScripted("")
	s.answers["todos.stores"] = func([]any) (any, error) {
		return []any{
			map[string]any{"id": "S1", "path": "/src/LabelManager/.auto-agents/kb/.todo-list", "roots": []any{"/src/LabelManager/.auto-agents/kb"}, "claimed": true},
			map[string]any{"id": "S2", "path": "/home/j/.config/nvim/.auto-agents-config/kb/.todo-list", "roots": []any{"/home/j/.config/nvim/.auto-agents-config/kb"}},
			map[string]any{"id": "S3", "path": "/home/j/html-archives/.todo-list", "roots": []any{"/home/j/html-archives"}},
		}, nil
	}
	s.answers["preference.list"] = func([]any) (any, error) { return prefs, nil }
	s.answers["preference.set"] = func(p []any) (any, error) { prefs[p[0].(string)] = p[1]; return nil, nil }
	return s
}

// TestTodosLabelAStoreAndClearIt: the dropdown shows the stores by their derived names; L (the top
// bar's Edit) renames the store shown (a read-only one too: the label is AutoDoc's), kept as a
// preference by the store's id, so a new panel (a restart) shows it, and the store stays shown; an
// empty label is the derived name again. Two stores may share a label: the heading, the store's
// folder, tells them apart. The store's files are never written.
func TestTodosLabelAStoreAndClearIt(t *testing.T) {
	prefs := map[string]any{"tui.other": "x"}
	s := labelledStores(prefs)
	v := newTodos(s)
	v.list()
	if got, want := storeLabels(v), []string{"LabelManager", "nvim", "html-archives"}; !slices.Equal(got, want) {
		t.Fatalf("the dropdown %q, want %q", got, want)
	}
	v.storeChosen(1)
	if got := s.set["App.todosTitle"]; got != "todos · /home/j/.config/nvim/.auto-agents-config/kb/.todo-list" {
		t.Errorf("heading %v", got)
	}
	v.label()
	if s.opened[len(s.opened)-1] != "todoLabel" || s.set["App.todoLabelText"] != "" {
		t.Fatalf("L: opened %v, text %v", s.opened, s.set["App.todoLabelText"])
	}
	v.labelSet("  Config  ")
	if prefs["tui.todos.label.S2"] != "Config" {
		t.Fatalf("prefs %v", prefs)
	}
	if got := storeLabels(v)[1]; got != "Config" || v.cur.id != "root:/home/j/.config/nvim/.auto-agents-config/kb" || s.set["App.todoStoreIndex"] != 1 {
		t.Errorf("labelled: %q, shown %+v, index %v", got, v.cur, s.set["App.todoStoreIndex"])
	}
	// the same label on another store: both show it, and their headings differ
	h1 := s.set["App.todosTitle"]
	v.storeChosen(2)
	v.label()
	v.labelSet("Config")
	if got := storeLabels(v); got[1] != got[2] {
		t.Errorf("two stores labelled alike: %q", got)
	}
	if h2 := s.set["App.todosTitle"]; h1 == h2 {
		t.Errorf("both headings %v", h1)
	}
	// a restart: a new panel on the same daemon reads the labels again
	again := newTodos(s)
	again.list()
	if got := storeLabels(again)[1]; got != "Config" {
		t.Errorf("after a restart: %q", got)
	}
	// cleared: the derived name, and the label shown in L's prompt as it was
	again.storeChosen(1)
	again.label()
	if s.set["App.todoLabelText"] != "Config" {
		t.Errorf("L's prompt holds %v", s.set["App.todoLabelText"])
	}
	again.labelSet("")
	if got := storeLabels(again)[1]; got != "nvim" {
		t.Errorf("cleared: %q", got)
	}
	if !strings.Contains(strings.Join(s.notes, "|"), "shows as nvim again") {
		t.Errorf("notes %v", s.notes)
	}
	// only preferences were written: no todos.* write reached a store
	for _, c := range s.calls {
		if strings.HasPrefix(c, "todos.") && !strings.HasPrefix(c, "todos.stores") && !strings.HasPrefix(c, "todos.list") {
			t.Errorf("a store was asked %s", c)
		}
	}
	// a label prompt answered after its store is gone writes nothing
	n := len(s.calls)
	again.labelSet("late")
	if len(s.calls) != n {
		t.Errorf("a label with no store asked: %v", s.calls[n:])
	}
}

// TestTodosListWithoutPreferences: a daemon keeping no preferences still lists the stores, by their
// derived names.
func TestTodosListWithoutPreferences(t *testing.T) {
	s := labelledStores(nil)
	s.answers["preference.list"] = func([]any) (any, error) { return nil, fmt.Errorf("-32004 no preferences") }
	v := newTodos(s)
	v.list()
	if got := storeLabels(v); len(got) != 3 || got[0] != "LabelManager" || len(s.failed) != 0 {
		t.Errorf("stores %q, failed %v", got, s.failed)
	}
}

// movingStore is a claimed workspace store whose one task moves as todos.status says: its tab is the
// task's status, an archived one under 2026/10.
func movingStore(status string) (*scriptedCore, *string) {
	s := scriptedStore(true)
	cur := status
	s.answers["todos.list"] = func(p []any) (any, error) {
		b := p[1].(map[string]any)["buckets"].([]any)[0].(string)
		switch {
		case b == "archived" && cur == "archived":
			return map[string]any{"partitions": []any{map[string]any{"month": "2026/10", "files": int64(1)}}}, nil
		case b == "archived":
			return map[string]any{"partitions": []any{}}, nil
		case b == "archived/2026/10" && cur == "archived":
			return map[string]any{"tasks": []any{map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": cur, "path": "archived/2026/10/t1.md"}}}, nil
		case b != cur:
			return map[string]any{"tasks": []any{}}, nil
		}
		return map[string]any{"tasks": []any{map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": cur, "path": cur + "/t1.md"}}}, nil
	}
	s.answers["todos.status"] = func(p []any) (any, error) {
		cur = p[3].(string)
		path := cur + "/t1.md"
		if cur == "archived" {
			path = "archived/2026/10/t1.md"
		}
		return map[string]any{"id": "t1", "rev": "r2", "status": cur, "path": path}, nil
	}
	return s, &cur
}

// onTask shows the tab of status and puts the cursor on its task: for archived, in its month.
func onTask(t *testing.T, v *todos, status string) {
	t.Helper()
	v.showTab(tabOf(status))
	if status == "archived" {
		v.monthChosen(0)
	}
	if _, x := v.cursor(); x == nil {
		t.Fatalf("no task under the cursor on %s: %q", status, tabRows(v, v.tab))
	}
}

// statusRows are the rows s's picker lists.
func statusRows(v *todos) []string {
	m := v.statusPick.list.Model()
	var out []string
	for i := 0; i < m.Len(); i++ {
		out = append(out, m.Data(tuidecl.Index{Row: i}, "status").Raw)
	}
	return out
}

// pickStatus is s on the task under the cursor, then the row of status chosen in its picker.
func pickStatus(t *testing.T, v *todos, status string) {
	t.Helper()
	v.chooseStatus()
	i := slices.Index(todoStatuses, status)
	if i < 0 || v.statusing == nil {
		t.Fatalf("no picker for %s: statusing %v", status, v.statusing)
	}
	v.statusPick.accept(i, "")
}

// TestTodosStatusPickerSetsEveryStatus: s opens a picker of every status, the task's own marked;
// choosing one moves the task there through todos.status, deferred and archived too, and the tab
// of that status shows, the cursor on the task (in Archived, in the month its file went to). An
// archived task comes back the same way. Choosing the status the task has, or dismissing the
// picker, asks nothing of the daemon.
func TestTodosStatusPickerSetsEveryStatus(t *testing.T) {
	for _, from := range []string{"open", "archived"} {
		for _, want := range todoStatuses {
			if want == from {
				continue
			}
			s, cur := movingStore(from)
			v := newTodos(s)
			v.list()
			onTask(t, v, from)
			v.chooseStatus()
			if !slices.Contains(s.opened, "todoStatusPicker") {
				t.Fatalf("s opened %v", s.opened)
			}
			marked := slices.IndexFunc(statusRows(v), func(r string) bool { return strings.HasPrefix(r, "● ") })
			if got := statusRows(v); len(got) != 5 || marked != slices.Index(todoStatuses, from) {
				t.Fatalf("from %s, the statuses offered: %q", from, got)
			}
			s.views = nil
			v.statusPick.accept(slices.Index(todoStatuses, want), "")
			if *cur != want || !s.called("todos.status[kb t1 r1 "+want+"]") || !slices.Contains(s.closed, "todoStatusPicker") {
				t.Fatalf("%s → %s: the task is %s, calls %v, closed %v", from, want, *cur, s.calls, s.closed)
			}
			tab := tabOf(want)
			if v.tab != tab || s.set["App.todoTab"] != tab || !slices.Contains(s.views, todoTabs[tab].table+".forceActiveFocus") {
				t.Errorf("%s → %s: tab %d (shown %v), views %v", from, want, v.tab, s.set["App.todoTab"], s.views)
			}
			if _, x := v.cursor(); str(x, "id") != "t1" || str(x, "status") != want {
				t.Errorf("%s → %s: the cursor is on %v", from, want, x)
			}
			if want == "archived" && v.month != "2026/10" {
				t.Errorf("into Archived: month %q", v.month)
			}
		}
	}

	// its own status, or the picker dismissed: nothing asked
	for _, way := range []string{"its own", "dismissed"} {
		s, _ := movingStore("deferred")
		v := newTodos(s)
		v.list()
		onTask(t, v, "deferred")
		v.chooseStatus()
		if way == "dismissed" {
			v.statusPick.dismiss()
			v.statusChosen(todoStatusChoice{status: "open"}) // a stray choice after the dismissal
		} else {
			v.statusPick.accept(2, "")
		}
		if s.called("todos.status") || v.statusing != nil {
			t.Errorf("%s: calls %v, statusing %v", way, s.calls, v.statusing)
		}
	}
}

// TestTodosStatusPickerPreviewsTheMove: the picker's preview says what choosing a status does to the
// task, and that its own status changes nothing.
func TestTodosStatusPickerPreviewsTheMove(t *testing.T) {
	s, _ := movingStore("open")
	v := newTodos(s)
	v.list()
	onTask(t, v, "open")
	v.chooseStatus()
	if got := s.set["App.todoStatusPreviewText"]; got != "## open\n\nits status now:\nchoosing it\nchanges nothing\n" || s.set["App.todoStatusPreviewTitle"] != "Ship" {
		t.Errorf("the first row's preview: %q, titled %q", got, s.set["App.todoStatusPreviewTitle"])
	}
	v.statusPick.move(2)
	if got := s.set["App.todoStatusPreviewText"]; got != "## deferred\n\nfrom open\nto deferred\n" {
		t.Errorf("deferred's preview: %q", got)
	}
	if got := s.set["App.todoStatusList"]; got != "statuses (5 of 5)" {
		t.Errorf("the list's title: %q", got)
	}
	v.statusPick.filter("arch")
	if got := statusRows(v); !slices.Equal(got, []string{"  archived"}) {
		t.Errorf("filtered: %q", got)
	}
	v.statusing = nil
	if got := v.statusPane(todoStatusChoice{status: "open"}); got != (pane{}) {
		t.Errorf("no task: %+v", got)
	}
}

// actions is the action row as syncActions last set it: the actions shown, and the read-only note.
func actions(s *scriptedCore) string {
	var on []string
	if s.set["App.todoReadOnly"] == true {
		on = append(on, "read-only")
	}
	for _, a := range []string{"Status", "Assign", "Remove", "Add"} {
		if s.set["App.todoAct"+a] == true {
			on = append(on, a)
		}
	}
	return strings.Join(on, " ")
}

// TestTodosActionRowFollowsTheCursor: the action row shows only the actions that apply: on a
// claimed store's task, whatever its status and in any tab, Status…, Assign, Remove and Add; with no
// task under the cursor (an empty tab, Archived's months), Add alone. A store the daemon has not
// claimed shows none, anywhere, only its note. A button's own click changes nothing at once, only
// the answer that lands later (a golib Button must never hide itself from its click).
func TestTodosActionRowFollowsTheCursor(t *testing.T) {
	for _, claimed := range []bool{true, false} {
		s := scriptedStore(claimed)
		v := newTodos(s)
		v.list()
		task, empty := "Status Assign Remove Add", "Add"
		if !claimed {
			task, empty = "read-only", "read-only"
		}
		if got := actions(s); got != task {
			t.Errorf("claimed %v, on Open's task: %q, want %q", claimed, got, task)
		}
		v.showTab(1) // In progress: empty
		if got := actions(s); got != empty {
			t.Errorf("claimed %v, on an empty tab: %q, want %q", claimed, got, empty)
		}
		v.showTab(archivedTab) // the months, no month open
		if got := actions(s); got != empty {
			t.Errorf("claimed %v, on Archived's months: %q, want %q", claimed, got, empty)
		}
		v.monthChosen(0)
		if got := actions(s); got != task {
			t.Errorf("claimed %v, on an archived task: %q, want %q", claimed, got, task)
		}
	}
	for _, status := range []string{"in-progress", "deferred", "completed"} {
		s, _ := movingStore(status)
		v := newTodos(s)
		v.list()
		onTask(t, v, status)
		if got := actions(s); got != "Status Assign Remove Add" {
			t.Errorf("on a %s task: %q", status, got)
		}
	}
	none := newScripted("")
	newTodos(none).syncActions()
	if got := actions(none); got != "" {
		t.Errorf("no store listed: %q", got)
	}

	// Status… chosen on an in-progress task: its button stays until the move's answer lands, and the
	// cursor, following the task, keeps it shown
	s, _ := movingStore("in-progress")
	w := newTodos(s)
	w.list()
	onTask(t, w, "in-progress")
	w.chooseStatus()
	s.deferred = true
	w.statusPick.accept(slices.Index(todoStatuses, "completed"), "")
	if s.set["App.todoActStatus"] != true {
		t.Fatal("Status… changed inside its own choice")
	}
	for len(s.pending) > 0 { // every answer held, in the order asked, until none is left
		order := make([]int, len(s.pending))
		for i := range order {
			order[i] = i
		}
		s.deliver(order...)
	}
	if s.set["App.todoActStatus"] != true || w.tab != tabOf("completed") {
		t.Errorf("after the move landed: Status… %v, tab %d", s.set["App.todoActStatus"], w.tab)
	}
}

// TestTodoDetailsAreMarkdown: a task's details are Markdown, rendered as the pickers' previews:
// its title a heading, its fields a list, its description as written.
func TestTodoDetailsAreMarkdown(t *testing.T) {
	d := todoDetail(map[string]any{"title": "Ship", "status": "open", "tags": []any{"x"}, "description": "**bold** notes",
		"errors": []any{map[string]any{"field": "adr[0]", "code": "not-found", "message": "gone"}}})
	for _, want := range []string{"## Ship\n\n", "- status: open\n", "- tags: x\n", "errors:\n\n- adr[0] not-found: gone\n", "\n**bold** notes\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("details lack %q:\n%s", want, d)
		}
	}
}

// TestTodosTheCursorFollowsWhenTheTabLands: after a move the destination tab is read, and the
// cursor goes to the task only when that read lands; a read of the tab before, answered after,
// moves nothing.
func TestTodosTheCursorFollowsWhenTheTabLands(t *testing.T) {
	s, _ := movingStore("in-progress")
	v := newTodos(s)
	v.list()
	onTask(t, v, "in-progress")
	v.chooseStatus()
	s.deferred = true
	v.statusPick.accept(slices.Index(todoStatuses, "completed"), "")
	s.deliver(0) // the move's answer: Completed shown, its read held
	if v.tab != tabOf("completed") || len(s.pending) != 1 || v.follow.id != "t1" {
		t.Fatalf("after the move: tab %d, held %d, follow %+v", v.tab, len(s.pending), v.follow)
	}
	if _, x := v.cursor(); x != nil {
		t.Errorf("the cursor is on %v before Completed landed", x)
	}
	s.deliver(0)
	if _, x := v.cursor(); str(x, "id") != "t1" || v.follow.id != "" || s.set["App.todoCompletedIndex"] != 0 {
		t.Errorf("after Completed landed: %v, follow %+v, index %v", x, v.follow, s.set["App.todoCompletedIndex"])
	}
}

// TestTodosLabelsAndMovesAtTheEdges: the rarer paths. The home folder shows as ~; a plain folder is
// its own root; a workspace whose store the daemon has not placed is named for the workspace; a
// labels answer for an older listing is dropped; s with no task under the cursor, and L with no
// store, do nothing; a moved task that is not where its tab's read says leaves the cursor where the
// read put it; an answer with no task in it still follows the task by its id.
func TestTodosLabelsAndMovesAtTheEdges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := tildePath(home + "/notes/.todo-list"); got != "~/notes/.todo-list" {
		t.Errorf("tildePath = %q", got)
	}
	if got := tildePath("/elsewhere"); got != "/elsewhere" {
		t.Errorf("tildePath outside home = %q", got)
	}
	if got := storeRoot("/p"); got != "/p" {
		t.Errorf("storeRoot of a plain folder = %q", got)
	}
	if got := monthOf("archived/2026/10/x.md"); got != "2026/10" {
		t.Errorf("monthOf = %q", got)
	}
	if got := monthOf("open/x.md"); got != "" {
		t.Errorf("monthOf an open task = %q", got)
	}

	// the workspace's store not placed yet: named for the workspace
	s := scriptedStore(true)
	s.answers["todos.resolve"] = func([]any) (any, error) { return map[string]any{"claimed": false}, nil }
	v := newTodos(s)
	v.list()
	if got := storeLabels(v)[0]; got != "kb" {
		t.Errorf("an unplaced workspace store: %q", got)
	}

	// a labels answer for a listing since replaced is dropped
	d := labelledStores(map[string]any{"tui.todos.label.S1": "Old"})
	d.deferred = true
	w := newTodos(d)
	w.list()
	d.deliver(0) // the stores: the labels asked for, held
	w.list()     // listed again before they answer
	held := d.pending
	d.pending = nil
	held[0]() // the first listing's labels: dropped
	if len(storeLabels(w)) != 0 {
		t.Fatalf("a stale labels answer showed the stores: %q", storeLabels(w))
	}
	held[1]() // the second listing's stores
	d.deliver(0)
	if got := storeLabels(w); len(got) != 3 || got[0] != "Old" {
		t.Errorf("the second listing: %q", got)
	}

	// s by its command, and L, with nothing to act on
	m, _ := movingStore("in-progress")
	x := newTodos(m)
	x.list() // Open: empty
	n := len(m.calls)
	if err := x.Commands()["App.todoChooseStatus"].handler("App.todoChooseStatus")(nil); err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != n || slices.Contains(m.opened, "todoStatusPicker") {
		t.Errorf("s with no task asked %v, opened %v", m.calls[n:], m.opened)
	}
	none := newTodos(newScripted(""))
	none.label()
	if len(none.c.(*scriptedCore).opened) != 0 {
		t.Error("L with no store opened the prompt")
	}

	// the moved task not in its new tab's read (another client moved it on): the cursor on the row
	// the read gives
	g, _ := movingStore("open")
	g.answers["todos.status"] = func([]any) (any, error) { return nil, nil } // the read still finds it nowhere new
	y := newTodos(g)
	y.list()
	onTask(t, y, "open")
	pickStatus(t, y, "in-progress")
	if _, task := y.cursor(); task != nil || y.tab != 1 || y.follow.id != "" {
		t.Errorf("a task not found: cursor %v, tab %d, follow %+v", task, y.tab, y.follow)
	}
	// a status unknown to the tabs follows nothing
	y.followTask(map[string]any{"id": "t1"}, "nowhere")
	if y.tab != 1 {
		t.Errorf("an unknown status changed the tab: %d", y.tab)
	}
}
