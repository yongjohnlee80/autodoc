package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// todos_labels_test.go: what a store is called (its derived name, the user's label), s's Status…
// picker with the cursor following the task, and the action row.

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

// TestTodosLabelAStoreAndClearIt: the stores show by their derived names; L labels the one under the
// cursor (a read-only one too: the label is AutoDoc's), kept as a preference by the store's id, so a
// new panel (a restart) shows it; an empty label is the derived name again. Two stores may share a
// label: the heading, the store's folder, tells them apart. The store's files are never written.
func TestTodosLabelAStoreAndClearIt(t *testing.T) {
	prefs := map[string]any{"tui.other": "x"}
	s := labelledStores(prefs)
	v := newTodos(s)
	v.list()
	if got, want := treeLabels(v, nil), []string{"● LabelManager", "  nvim  (read-only until the cutover)", "  html-archives  (read-only until the cutover)"}; !slices.Equal(got, want) {
		t.Fatalf("roots %q, want %q", got, want)
	}
	v.moved(tuidecl.Index{Row: 1})
	if got := s.set["App.todosTitle"]; got != "todos · /home/j/.config/nvim/.auto-agents-config/kb/.todo-list" {
		t.Errorf("heading %v", got)
	}
	if d := fmt.Sprint(s.set["App.todoDetail"]); !strings.Contains(d, "## nvim") || !strings.Contains(d, "- name: nvim") || !strings.Contains(d, "label: none") {
		t.Errorf("a store's details:\n%s", d)
	}
	v.label()
	if s.opened[len(s.opened)-1] != "todoLabel" || s.set["App.todoLabelText"] != "" {
		t.Fatalf("L: opened %v, text %v", s.opened, s.set["App.todoLabelText"])
	}
	v.labelSet("  Config  ")
	if prefs["tui.todos.label.S2"] != "Config" {
		t.Fatalf("prefs %v", prefs)
	}
	if got := treeLabels(v, nil)[1]; got != "  Config  (read-only until the cutover)" {
		t.Errorf("labelled: %q", got)
	}
	// the same label on another store: both show it, and their headings differ
	v.moved(tuidecl.Index{Row: 2})
	v.label()
	v.labelSet("Config")
	if got := treeLabels(v, nil); got[1] != got[2] {
		t.Errorf("two stores labelled alike: %q", got)
	}
	v.moved(tuidecl.Index{Row: 1})
	h1 := s.set["App.todosTitle"]
	v.moved(tuidecl.Index{Row: 2})
	if h2 := s.set["App.todosTitle"]; h1 == h2 {
		t.Errorf("both headings %v", h1)
	}
	// a restart: a new panel on the same daemon reads the labels again
	again := newTodos(s)
	again.list()
	if got := treeLabels(again, nil)[1]; got != "  Config  (read-only until the cutover)" {
		t.Errorf("after a restart: %q", got)
	}
	// cleared: the derived name, and the label shown in L's prompt as it was
	again.moved(tuidecl.Index{Row: 1})
	again.label()
	if s.set["App.todoLabelText"] != "Config" {
		t.Errorf("L's prompt holds %v", s.set["App.todoLabelText"])
	}
	again.labelSet("")
	if got := treeLabels(again, nil)[1]; got != "  nvim  (read-only until the cutover)" {
		t.Errorf("cleared: %q", got)
	}
	if !strings.Contains(strings.Join(s.notes, "|"), "shows as nvim again") {
		t.Errorf("notes %v", s.notes)
	}
	// only preferences were written: no todos.* write reached a store
	for _, c := range s.calls {
		if strings.HasPrefix(c, "todos.") && !strings.HasPrefix(c, "todos.stores") {
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
	if got := treeLabels(v, nil); len(got) != 3 || got[0] != "● LabelManager" || len(s.failed) != 0 {
		t.Errorf("roots %q, failed %v", got, s.failed)
	}
}

// movingStore is a claimed workspace store whose one task moves as todos.status says: its section
// is the task's status.
func movingStore(status string) (*scriptedCore, *string) {
	s := scriptedStore(true)
	cur := status
	s.answers["todos.list"] = func(p []any) (any, error) {
		b := p[1].(map[string]any)["buckets"].([]any)[0].(string)
		if b != cur {
			return map[string]any{"tasks": []any{}}, nil
		}
		return map[string]any{"tasks": []any{map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": cur, "path": cur + "/t1.md"}}}, nil
	}
	s.answers["todos.status"] = func(p []any) (any, error) { cur = p[3].(string); return nil, nil }
	return s, &cur
}

// onTask opens the task's section and puts the cursor on the task.
func onTask(t *testing.T, v *todos, bucket string) {
	t.Helper()
	ix := openSection(t, v, "ws:kb", bucket)
	v.moved(tuidecl.Index{Row: 0, Parent: &ix})
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
// choosing one moves the task there through todos.status, deferred and archived too, and the
// cursor follows it into its new section (Archived lists months, so there it stays). An archived
// task comes back the same way. Choosing the status the task has, or dismissing the picker, asks
// nothing of the daemon.
func TestTodosStatusPickerSetsEveryStatus(t *testing.T) {
	for _, want := range []string{"in-progress", "deferred", "completed", "archived"} {
		s, cur := movingStore("open")
		v := newTodos(s)
		v.list()
		onTask(t, v, "open")
		v.chooseStatus()
		if !slices.Contains(s.opened, "todoStatusPicker") {
			t.Fatalf("s opened %v", s.opened)
		}
		if got := statusRows(v); !slices.Equal(got, []string{"● open", "  in-progress", "  deferred", "  completed", "  archived"}) {
			t.Fatalf("the statuses offered: %q", got)
		}
		s.views = nil
		v.statusPick.accept(slices.Index(todoStatuses, want), "")
		if *cur != want || !s.called("todos.status[kb t1 r1 "+want+"]") || !slices.Contains(s.closed, "todoStatusPicker") {
			t.Fatalf("chose %s: the task is %s, calls %v, closed %v", want, *cur, s.calls, s.closed)
		}
		following := slices.Contains(s.views, "todoTree.setCurrentIndex")
		if want == "archived" {
			if following || v.follow.store != "" || v.at[1] != bucketKey("ws:kb", "open") {
				t.Errorf("archived: the cursor went %q (views %v, follow %+v)", v.at, s.views, v.follow)
			}
			continue
		}
		if !following || len(v.at) != 3 || v.at[1] != bucketKey("ws:kb", want) || v.at[2] != taskKey("ws:kb", "t1") {
			t.Errorf("chose %s: the cursor did not follow: at %q, views %v", want, v.at, s.views)
		}
	}

	// an archived task, under its month as read, back to open: the cursor follows it into Open
	s, cur := movingStore("archived")
	v := newTodos(s)
	v.list()
	v.tasks[taskKey("ws:kb", "t1")] = map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": "archived"}
	v.at = []string{"ws:kb", bucketKey("ws:kb", "archived"), monthKey("ws:kb", "2026/09"), taskKey("ws:kb", "t1")}
	v.chooseStatus()
	if got := statusRows(v); got[4] != "● archived" {
		t.Fatalf("an archived task's statuses: %q", got)
	}
	v.statusPick.accept(0, "")
	if *cur != "open" || len(v.at) != 3 || v.at[1] != bucketKey("ws:kb", "open") {
		t.Errorf("archived → open: the task is %s, the cursor at %q", *cur, v.at)
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
	for _, a := range []string{"Add", "Status", "Refresh", "Label", "Assign", "Remove"} {
		if s.set["App.todoAct"+a] == true {
			on = append(on, a)
		}
	}
	return strings.Join(on, " ")
}

// TestTodosActionRowFollowsTheRowKind: the action row shows only the actions that apply to the row
// under the cursor: on a claimed store's row Add, Refresh and Label; on a section's, or an archived
// month's, Add and Refresh; on a task's, whatever its status, Status…, Assign and Remove. A store
// the daemon has not claimed shows none, on any row, only its note. A button's own click changes
// nothing at once, only the answer that lands later (a golib Button must never hide itself from
// its click).
func TestTodosActionRowFollowsTheRowKind(t *testing.T) {
	for _, claimed := range []bool{true, false} {
		s := scriptedStore(claimed)
		v := newTodos(s)
		v.list()
		want := map[string]string{"store": "Add Refresh Label", "section": "Add Refresh", "month": "Add Refresh", "task": "Status Assign Remove"}
		if !claimed {
			want = map[string]string{"store": "read-only", "section": "read-only", "month": "read-only", "task": "read-only"}
		}
		if got := actions(s); got != want["store"] { // the cursor not yet moved: the first store
			t.Errorf("claimed %v, before the cursor moves: %q, want %q", claimed, got, want["store"])
		}
		open := openSection(t, v, "ws:kb", "open")
		archived := openSection(t, v, "ws:kb", "archived")
		for kind, ix := range map[string]tuidecl.Index{"store": {Row: 0}, "section": open,
			"month": {Row: 0, Parent: &archived}, "task": {Row: 0, Parent: &open}} {
			v.moved(ix)
			if got := actions(s); got != want[kind] {
				t.Errorf("claimed %v, on a %s: %q, want %q", claimed, kind, got, want[kind])
			}
		}
	}
	for _, status := range []string{"in-progress", "deferred", "completed"} {
		s, _ := movingStore(status)
		v := newTodos(s)
		v.list()
		onTask(t, v, status)
		if got := actions(s); got != "Status Assign Remove" {
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
	if s.set["App.todoActStatus"] != true || w.at[1] != bucketKey("ws:kb", "completed") {
		t.Errorf("after the move landed: Status… %v, at %q", s.set["App.todoActStatus"], w.at)
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

// TestTodosTheCursorFollowsOnceBothSectionsLand: the section a task left and the one it went to are
// read again after a move, their answers in either order; the cursor goes to the task only after
// both, since a section changing after the cursor is placed would move the rows under it.
func TestTodosTheCursorFollowsOnceBothSectionsLand(t *testing.T) {
	for _, destFirst := range []bool{true, false} {
		s, _ := movingStore("in-progress")
		v := newTodos(s)
		v.list()
		onTask(t, v, "in-progress")
		v.chooseStatus()
		s.deferred = true
		v.statusPick.accept(slices.Index(todoStatuses, "completed"), "")
		s.deliver(0) // the move's answer: the store's sections read again, held
		reads := map[string]int{}
		for i, c := range s.calls[len(s.calls)-len(s.pending):] {
			switch {
			case strings.Contains(c, "[in-progress]"):
				reads["from"] = i
			case strings.Contains(c, "[completed]"):
				reads["to"] = i
			}
		}
		if len(reads) != 2 || len(s.pending) != 2 {
			t.Fatalf("after the move, reads %v of %d held: %v", reads, len(s.pending), s.calls)
		}
		first, second := reads["from"], reads["to"]
		if destFirst {
			first, second = second, first
		}
		s.views = nil
		held := s.pending
		s.pending = nil
		held[first]()
		if slices.Contains(s.views, "todoTree.setCurrentIndex") {
			t.Errorf("destFirst=%v: the cursor was placed before both sections landed", destFirst)
		}
		held[second]()
		if !slices.Contains(s.views, "todoTree.setCurrentIndex") || len(v.at) != 3 || v.at[1] != bucketKey("ws:kb", "completed") {
			t.Errorf("destFirst=%v: after both, at %q, views %v", destFirst, v.at, s.views)
		}
	}
}

// TestTodosLabelsAndStepsAtTheEdges: the rarer paths. The home folder shows as ~; a plain folder is
// its own root; a workspace whose store the daemon has not placed is named for the workspace; a
// labels answer for an older listing is dropped; s (its command) with no task under the cursor, and
// L with no store, do nothing; a moved task that is not where its section's read says leaves the
// cursor where it is.
func TestTodosLabelsAndStepsAtTheEdges(t *testing.T) {
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

	// the workspace's store not placed yet: named for the workspace
	s := scriptedStore(true)
	s.answers["todos.resolve"] = func([]any) (any, error) { return map[string]any{"claimed": false}, nil }
	v := newTodos(s)
	v.list()
	if got := treeLabels(v, nil)[0]; got != "  kb · ws kb  (read-only until the cutover)" {
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
	if len(treeLabels(w, nil)) != 0 {
		t.Fatalf("a stale labels answer showed the stores: %q", treeLabels(w, nil))
	}
	held[1]() // the second listing's stores
	d.deliver(0)
	if got := treeLabels(w, nil); len(got) != 3 || got[0] != "● Old" {
		t.Errorf("the second listing: %q", got)
	}

	// s by its command, and L, with nothing to act on
	m, _ := movingStore("in-progress")
	x := newTodos(m)
	x.list()
	x.moved(tuidecl.Index{Row: 0}) // the store's row: no task
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

	// the moved task not in its new section's read (another client moved it on): no cursor move
	g, _ := movingStore("open")
	g.answers["todos.status"] = func([]any) (any, error) { return nil, nil } // the read still finds it nowhere new
	y := newTodos(g)
	y.list()
	onTask(t, y, "open")
	g.views = nil
	pickStatus(t, y, "in-progress")
	if slices.Contains(g.views, "todoTree.setCurrentIndex") || y.follow.store != "" {
		t.Errorf("a task not found moved the cursor: views %v, follow %+v", g.views, y.follow)
	}

	// the view refusing the cursor's move is said, as a failure, not lost
	e, _ := movingStore("open")
	z := newTodos(e)
	z.list()
	onTask(t, z, "open")
	e.viewErr = errors.New("the row is gone")
	pickStatus(t, z, "in-progress")
	if len(e.failed) != 1 || !strings.Contains(e.failed[0], "todos: the moved task: the row is gone") {
		t.Errorf("failed %v", e.failed)
	}
}
