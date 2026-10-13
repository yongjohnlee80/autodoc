package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// todos_labels_test.go: what a store is called (its derived name, the user's label), the < and >
// steps and the s cycle with the cursor following the task, and the task buttons' states.

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

// TestTodosStepsATaskAndTheCursorFollows: > is a step on (open → in progress → completed), < a step
// back; the cursor goes with the task into its new section. At the ends, and on a deferred or an
// archived task, nothing moves and the status line says why.
func TestTodosStepsATaskAndTheCursorFollows(t *testing.T) {
	s, cur := movingStore("open")
	v := newTodos(s)
	v.list()
	onTask(t, v, "open")
	v.step(todoBack, "back")
	if s.called("todos.status") || !strings.Contains(fmt.Sprint(s.set["App.todosStatus"]), "nothing back of open") {
		t.Fatalf("< on open: %v, status %v", s.calls, s.set["App.todosStatus"])
	}
	for _, want := range []string{"in-progress", "completed"} {
		s.views = nil
		v.step(todoForward, "forward")
		if *cur != want {
			t.Fatalf("> moved the task to %s, want %s", *cur, want)
		}
		if got := v.at; len(got) != 3 || got[1] != bucketKey("ws:kb", want) || got[2] != taskKey("ws:kb", "t1") || !slices.Contains(s.views, "todoTree.setCurrentIndex") {
			t.Fatalf("the cursor did not follow into %s: at %q, views %v", want, got, s.views)
		}
	}
	n := len(s.calls)
	v.step(todoForward, "forward")
	if len(s.calls) != n || !strings.Contains(fmt.Sprint(s.set["App.todosStatus"]), "nothing forward of completed") {
		t.Errorf("> on completed: %v", s.calls[n:])
	}
	v.step(todoBack, "back")
	if *cur != "in-progress" || v.at[1] != bucketKey("ws:kb", "in-progress") {
		t.Errorf("< from completed: %s, at %q", *cur, v.at)
	}
	for _, off := range []string{"deferred", "archived"} {
		s, _ := movingStore(off)
		w := newTodos(s)
		w.list()
		if off == "archived" {
			// an archived task is under a month; put it under the cursor as read
			w.tasks[taskKey("ws:kb", "t1")] = map[string]any{"id": "t1", "status": "archived"}
			w.at = []string{"ws:kb", bucketKey("ws:kb", "archived"), taskKey("ws:kb", "t1")}
		} else {
			onTask(t, w, off)
		}
		w.step(todoForward, "forward")
		w.step(todoBack, "back")
		if s.called("todos.status") {
			t.Errorf("a %s task moved: %v", off, s.calls)
		}
	}
}

// TestTodosCycleTakesInProgress: s goes open → in progress → completed → deferred → open, the
// cursor following.
func TestTodosCycleTakesInProgress(t *testing.T) {
	s, cur := movingStore("open")
	v := newTodos(s)
	v.list()
	onTask(t, v, "open")
	for _, want := range []string{"in-progress", "completed", "deferred", "open"} {
		v.cycle()
		if *cur != want || v.at[1] != bucketKey("ws:kb", want) {
			t.Fatalf("s: %s, at %q; want %s", *cur, v.at, want)
		}
	}
}

// TestTodosButtonsFollowTheCursor: which task buttons apply follows the row under the cursor and
// the store's writes; a button's own click never changes its state at once, only the answer that
// lands later (a golib Button must never disable itself from its click).
func TestTodosButtonsFollowTheCursor(t *testing.T) {
	can := func(s *scriptedCore) string {
		var on []string
		for _, b := range []string{"Preview", "Add", "Back", "Forward", "Remove"} {
			if s.set["App.todoCan"+b] == true {
				on = append(on, b)
			}
		}
		return strings.Join(on, " ")
	}
	for _, tc := range []struct {
		status, want string
	}{
		{"open", "Preview Add Forward Remove"},
		{"in-progress", "Preview Add Back Forward Remove"},
		{"completed", "Preview Add Back Remove"},
		{"deferred", "Preview Add Remove"},
	} {
		s, _ := movingStore(tc.status)
		v := newTodos(s)
		v.list()
		onTask(t, v, tc.status)
		if got := can(s); got != tc.want {
			t.Errorf("on a %s task: %q, want %q", tc.status, got, tc.want)
		}
		v.moved(tuidecl.Index{Row: 0}) // the store's row: no task
		if got := can(s); got != "Add" {
			t.Errorf("on the store: %q", got)
		}
	}
	ro := scriptedStore(false)
	v := newTodos(ro)
	v.list()
	open := openSection(t, v, "ws:kb", "open")
	v.moved(tuidecl.Index{Row: 0, Parent: &open})
	if got := can(ro); got != "Preview" {
		t.Errorf("on a read-only store's task: %q", got)
	}

	// > clicked on an in-progress task: Status ▶ stays as it was until the move's answer lands
	s, _ := movingStore("in-progress")
	w := newTodos(s)
	w.list()
	onTask(t, w, "in-progress")
	s.deferred = true
	w.step(todoForward, "forward")
	if s.set["App.todoCanForward"] != true {
		t.Fatal("Status ▶ changed inside its own click")
	}
	for len(s.pending) > 0 { // every answer held, in the order asked, until none is left
		order := make([]int, len(s.pending))
		for i := range order {
			order[i] = i
		}
		s.deliver(order...)
	}
	if s.set["App.todoCanForward"] != false || s.set["App.todoCanBack"] != true {
		t.Errorf("after the move landed: forward %v, back %v", s.set["App.todoCanForward"], s.set["App.todoCanBack"])
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
		s.deferred = true
		v.step(todoForward, "forward")
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
