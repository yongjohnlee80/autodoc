package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// a store with one open task, claimed, or released (todos.release: the read-only case)
func scriptedStore(claimed bool) *featuretest.ScriptedCore {
	s := newScripted("kb")
	s.Answers["todos.resolve"] = func([]any) (any, error) {
		return map[string]any{"store": "/kb/.todo-list", "claimed": claimed, "released": !claimed}, nil
	}
	s.Answers["todos.list"] = func(p []any) (any, error) {
		opts := p[1].(map[string]any)
		switch b := opts["buckets"].([]any)[0].(string); {
		case b == "archived":
			return map[string]any{"partitions": []any{map[string]any{"month": "2026/09", "files": int64(3)}}}, nil
		case strings.HasPrefix(b, "archived/"):
			return map[string]any{"tasks": []any{map[string]any{"id": "old", "title": "Old", "status": "archived", "path": "archived/2026/09/old.md"}}}, nil
		case b == "open":
			return map[string]any{"tasks": []any{map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": "open", "priority": "high",
				"path": "open/t1.md", "tags": []any{"x"}, "description": "the notes",
				"errors": []any{map[string]any{"field": "adr[0]", "code": "not-found", "message": "gone"}}}},
				"malformed": []any{map[string]any{"path": "open/bad.md"}}}, nil
		}
		return map[string]any{"tasks": []any{}}, nil
	}
	s.Answers["todos.stores"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "/p/.todo-list", "roots": []any{"/p"}, "claimed": true},
			map[string]any{"path": "/q/.todo-list", "roots": []any{}}}, nil
	}
	return s
}

// tabRows are tab's rows as its table shows them: "<#>  <title>".
func tabRows(v *todos, tab int) []string {
	m := v.tasks[tab].Model()
	var out []string
	for i := 0; i < m.Len(); i++ {
		out = append(out, m.Data(tuidecl.Index{Row: i}, "n").Raw+"  "+m.Data(tuidecl.Index{Row: i}, "title").Raw)
	}
	return out
}

// storeLabels are the dropdown's rows.
func storeLabels(v *todos) []string {
	m := v.storeRows.Model()
	var out []string
	for i := 0; i < m.Len(); i++ {
		out = append(out, m.Data(tuidecl.Index{Row: i}, "label").Raw)
	}
	return out
}

// TestTodosShowsOneStoreChosenInADropdown: SPC x toggles the panel, saying so when there is no
// workspace and no store. Opened, the dropdown lists the workspace's store first and every other
// with a root of its own, by label or name; only the store shown is read, its Open tab. Choosing
// another in the dropdown empties the tabs and reads its own; a read of the store before, answered
// late, is dropped.
func TestTodosShowsOneStoreChosenInADropdown(t *testing.T) {
	none := newScripted("")
	newTodos(none).toggle()
	if len(none.Notes) != 1 || len(none.Views) != 0 {
		t.Errorf("no workspace: notes %v, views %v", none.Notes, none.Views)
	}
	s := scriptedStore(false)
	v := newTodos(s)
	v.toggle()
	if len(s.Views) != 1 || s.Views[0] != "todos.toggle" {
		t.Errorf("SPC x: views %v", s.Views)
	}
	v.Panels()[0].Opened(nil)
	if got, want := storeLabels(v), []string{"kb", "p"}; !slices.Equal(got, want) {
		t.Fatalf("the dropdown %q, want %q (the rootless store left out)", got, want)
	}
	if s.Sets["App.todoStoreIndex"] != 0 || v.cur.id != "ws:kb" || s.Sets["App.todoHasStore"] != true {
		t.Errorf("the store shown: index %v, %+v", s.Sets["App.todoStoreIndex"], v.cur)
	}
	var reads []string
	for _, c := range s.Calls {
		if strings.HasPrefix(c, "todos.list") {
			reads = append(reads, c)
		}
	}
	if !slices.Equal(reads, []string{"todos.list[kb map[buckets:[open]]]"}) {
		t.Errorf("reads %q: only the store shown's Open tab", reads)
	}
	if got := tabRows(v, 0); !slices.Equal(got, []string{"1  ⚠ Ship"}) {
		t.Errorf("Open: %q", got)
	}
	if spec := v.Panels()[0]; spec.ID != "todos" || spec.Edge(prefs{todosEdge: "left"}) != "left" {
		t.Errorf("the panel %+v", spec)
	}

	// another store chosen: the tabs emptied, its Open read; the old store's late answer dropped
	s.Answers["todos.list"] = func(p []any) (any, error) {
		return map[string]any{"tasks": []any{map[string]any{"id": "x", "title": fmt.Sprint(p[0])}}}, nil
	}
	s.Deferred = true
	v.reload() // kb's Open, held
	held := s.Pending
	s.Pending = nil
	s.Deferred = false
	v.storeChosen(1)
	if v.cur.id != "root:/p" || !s.Called("todos.list[map[root:/p] map[buckets:[open]]]") {
		t.Fatalf("p chosen: %+v, calls %v", v.cur, s.Calls)
	}
	held[0]() // kb's Open, late
	if got := tabRows(v, 0); !slices.Equal(got, []string{"1  map[root:/p]"}) {
		t.Errorf("p's Open after kb's late answer: %q", got)
	}
	n := len(s.Calls)
	v.storeChosen(1) // the same store: nothing read
	v.storeChosen(9) // no such store
	if len(s.Calls) != n {
		t.Errorf("the same store chosen again read %v", s.Calls[n:])
	}
}

// TestTodosTabsAreNumberedTablesReadWhenShown: a tab's tasks are read when it first shows, errored
// ones first and marked, every row numbered; the details are the task under the cursor's; 1–5 and
// [ ] change the tab, round, the keyboard in its table; a tab read already is not read again until
// a change; Enter opens the task's file and closes the panel.
func TestTodosTabsAreNumberedTablesReadWhenShown(t *testing.T) {
	s := scriptedStore(true)
	s.Answers["todos.list"] = func(p []any) (any, error) {
		b := p[1].(map[string]any)["buckets"].([]any)[0].(string)
		if b == "archived" {
			return map[string]any{"partitions": []any{}}, nil
		}
		return map[string]any{"tasks": []any{
			map[string]any{"id": b + "1", "rev": "r1", "title": "Clean " + b, "status": b, "path": b + "/1.md"},
			map[string]any{"id": b + "2", "rev": "r2", "title": "Ship " + b, "status": b, "priority": "high", "path": b + "/2.md",
				"tags": []any{"x"}, "description": "the notes", "errors": []any{map[string]any{"field": "adr[0]", "code": "not-found", "message": "gone"}}}}}, nil
	}
	v := newTodos(s)
	v.list()
	if got, want := tabRows(v, 0), []string{"1  ⚠ Ship open", "2  Clean open"}; !slices.Equal(got, want) {
		t.Fatalf("Open %q, want %q", got, want)
	}
	if s.Sets["App.todoOpenIndex"] != 0 {
		t.Errorf("the cursor %v", s.Sets["App.todoOpenIndex"])
	}
	d := fmt.Sprint(s.Sets["App.todoDetail"])
	for _, want := range []string{"Ship open", "priority: high", "tags:", "errors:", "adr[0] not-found: gone", "the notes"} {
		if !strings.Contains(d, want) {
			t.Errorf("details lack %q:\n%s", want, d)
		}
	}
	v.moved(0, 1)
	if !strings.Contains(fmt.Sprint(s.Sets["App.todoDetail"]), "## Clean open") {
		t.Errorf("the cursor moved: %v", s.Sets["App.todoDetail"])
	}
	v.moved(0, -1) // a table with rows moved away for a moment (selectRow): the cursor stays
	if v.cursors[0] != 1 {
		t.Errorf("a passing -1 took the cursor: %d", v.cursors[0])
	}
	v.moved(2, 0) // a table not shown moves its own cursor only
	if !strings.Contains(fmt.Sprint(s.Sets["App.todoDetail"]), "## Clean open") {
		t.Errorf("a hidden table moved the details: %v", s.Sets["App.todoDetail"])
	}
	s.Views = nil
	v.showTab(2)
	if s.Sets["App.todoTab"] != 2 || !s.Called("todos.list[kb map[buckets:[deferred]]]") || !slices.Contains(s.Views, "todoDeferredTable.forceActiveFocus") {
		t.Fatalf("3: tab %v, views %v", s.Sets["App.todoTab"], s.Views)
	}
	if got := tabRows(v, 2); len(got) != 2 {
		t.Errorf("Deferred %q", got)
	}
	for _, step := range []struct{ by, want int }{{1, 3}, {1, 4}, {1, varsTab}, {1, 0}, {-1, varsTab}, {-1, 4}, {-1, 3}} {
		v.stepTab(step.by)
		if v.tab != step.want || s.Sets["App.todoTab"] != step.want {
			t.Fatalf("a step of %d: tab %d, want %d", step.by, v.tab, step.want)
		}
	}
	s.Views = nil
	v.tabShown(3) // the bar's echo of the tab shown: nothing
	if len(s.Views) != 0 {
		t.Errorf("the bar's echo took the keyboard: %v", s.Views)
	}
	v.tabShown(0) // a click on Open's title
	if v.tab != 0 || !slices.Contains(s.Views, "todoOpenTable.forceActiveFocus") {
		t.Errorf("a click on a title: tab %d, views %v", v.tab, s.Views)
	}
	v.showTab(2)
	n := len(s.Calls)
	v.showTab(2) // read already, nothing changed
	v.showTab(7) // no such tab
	if len(s.Calls) != n {
		t.Errorf("a tab read already was read again: %v", s.Calls[n:])
	}
	v.activated(2, 1)
	if !slices.Equal(s.OpenedFiles, []string{"/kb/.todo-list/deferred/1.md"}) || !slices.Contains(s.Views, "todos.close") {
		t.Errorf("Enter on a task: files %v, views %v", s.OpenedFiles, s.Views)
	}
	v.activated(2, 9) // no such row
	if len(s.OpenedFiles) != 1 {
		t.Errorf("Enter on no row opened %v", s.OpenedFiles)
	}
}

// TestTodosArchivedTabListsItsMonths: Archived lists its months with their counts, and a month's
// tasks are read only when it opens; Backspace puts the keyboard back in the months; a read again
// keeps the month open, or closes it when it is gone.
func TestTodosArchivedTabListsItsMonths(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	s.Views = nil
	v.showTab(archivedTab)
	if v.months.Len() != 1 || !slices.Contains(s.Views, "todoMonths.forceActiveFocus") {
		t.Fatalf("months %d, views %v", v.months.Len(), s.Views)
	}
	if s.Called("todos.list[kb map[buckets:[archived/2026/09]]]") {
		t.Fatal("a month was read before it opened")
	}
	v.monthMoved(0)
	v.enter() // Enter on the card: the month under the cursor opens
	if !s.Called("todos.list[kb map[buckets:[archived/2026/09]]]") || !slices.Equal(tabRows(v, archivedTab), []string{"1  Old"}) {
		t.Fatalf("the month: calls %v, rows %q", s.Calls, tabRows(v, archivedTab))
	}
	if !slices.Contains(s.Views, "todoArchivedTable.forceActiveFocus") || !strings.Contains(fmt.Sprint(s.Sets["App.todoDetail"]), "## Old") {
		t.Errorf("the month's tasks: views %v, details %v", s.Views, s.Sets["App.todoDetail"])
	}
	s.Views = nil
	v.monthsBack()
	if !slices.Equal(s.Views, []string{"todoMonths.forceActiveFocus"}) {
		t.Errorf("Backspace: %v", s.Views)
	}
	v.reload()
	if v.month != "2026/09" || len(tabRows(v, archivedTab)) != 1 {
		t.Errorf("read again: month %q, rows %q", v.month, tabRows(v, archivedTab))
	}
	s.Answers["todos.list"] = func([]any) (any, error) {
		return map[string]any{"partitions": []any{map[string]any{"month": "2026/10", "files": int64(1)}}}, nil
	}
	v.reload()
	if v.month != "" || len(tabRows(v, archivedTab)) != 0 {
		t.Errorf("the month gone: month %q, rows %q", v.month, tabRows(v, archivedTab))
	}
	v.monthChosen(5) // no such month
	v.showTab(0)
	s.Views = nil
	v.monthsBack() // not on Archived: nothing
	if len(s.Views) != 0 {
		t.Errorf("Backspace off Archived: %v", s.Views)
	}
}

// TestTodosWritesAClaimedStoreAndNotAReadOnlyOne: on the claimed store a, s, d, R and M write to the
// store shown, an added task shown in Open under the cursor; on one the daemon has not claimed each
// writes nothing and says nothing (the action row says it is read-only), and R reads it again.
func TestTodosWritesAClaimedStoreAndNotAReadOnlyOne(t *testing.T) {
	s := scriptedStore(true)
	s.Answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "t1", "status": "open"}, nil }
	s.Answers["todos.refresh"] = func([]any) (any, error) { return map[string]any{"moved": int64(1)}, nil }
	v := newTodos(s)
	v.list()
	v.showTab(2)
	v.add()
	if s.Opened[len(s.Opened)-1] != "todoNew" {
		t.Fatalf("opened %v", s.Opened)
	}
	v.addTitle("  ")
	if s.Called("todos.add") {
		t.Error("an empty title was added")
	}
	v.addTitle(" Write it ")
	if v.tab != 0 || s.Sets["App.todoTab"] != 0 || s.Sets["App.todoOpenIndex"] != 0 {
		t.Errorf("the added task: tab %d, cursor %v", v.tab, s.Sets["App.todoOpenIndex"])
	}
	v.remove()
	if len(s.Asked) != 1 || s.Asked[0].Title != "remove the task?" {
		t.Fatalf("asked %v", s.Asked)
	}
	s.Yes()
	v.chooseStatus()
	v.statusPick.Accept(1, "") // in-progress
	for _, want := range []string{"todos.add[kb map[title:Write it]]", "todos.remove[kb t1 r1]", "todos.status[kb t1 r1 in-progress]"} {
		if !s.Called(want) {
			t.Errorf("no %s: %v", want, s.Calls)
		}
	}
	v.refresh()
	if !s.Called("todos.refresh") || !strings.Contains(strings.Join(s.Notes, "|"), "refreshed: 1 moved") {
		t.Errorf("refresh: %v %v", s.Calls, s.Notes)
	}
	v.toggleDetail()
	if s.Sets["App.todoDetailShown"] != false {
		t.Errorf("i: details shown %v", s.Sets["App.todoDetailShown"])
	}
	s.Answers["todos.remove"] = func([]any) (any, error) { return nil, errors.New("-32071 the task changed") }
	s.Yes()
	if len(s.Failures) != 1 || !strings.Contains(s.Failures[0], "todos.remove") {
		t.Errorf("failed %v", s.Failures)
	}

	ro := scriptedStore(false)
	w := newTodos(ro)
	w.list()
	n := len(ro.Calls)
	w.add()
	w.remove()
	w.chooseStatus()
	w.chooseFolder("move")
	if len(ro.Calls) != n || len(ro.Notes) != 0 || len(ro.Opened) != 0 || len(ro.Asked) != 0 {
		t.Errorf("a read-only store was written: %v, notes %v, opened %v", ro.Calls[n:], ro.Notes, ro.Opened)
	}
	w.refresh() // read-only: the tab read again, nothing reconciled
	if len(ro.Calls) == n || ro.Called("todos.refresh") {
		t.Errorf("refresh on a read-only store: %v", ro.Calls[n:])
	}
}

// TestTodosEventsAndFolders: an event for the store shown reads its tab again, another's nothing;
// Load registers the folder and shows its store; New registers and scaffolds it; M moves the store
// shown.
func TestTodosEventsAndFolders(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	n := len(s.Calls)
	v.Event("todos.changed", "kb", "")
	v.Event("todos.changed", "elsewhere", "x")
	if got := len(s.Calls) - n; got != 1 {
		t.Errorf("%d reads after the events, want the one tab shown of kb", got)
	}
	v.storeChosen(1)
	n = len(s.Calls)
	v.Event("todos.changed", "", "/p#t9")
	if got := len(s.Calls) - n; got != 1 {
		t.Errorf("%d reads after /p's event", got)
	}
	v.chooseFolder("load")
	if s.Opened[len(s.Opened)-1] != "todoFolder" {
		t.Errorf("folder dialog %v", s.Opened)
	}
	s.Answers["todos.stores"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "/p/.todo-list", "roots": []any{"/p"}, "claimed": true},
			map[string]any{"path": "/r/.todo-list", "roots": []any{"/r"}}}, nil
	}
	v.folderChosen("/r/.todo-list")
	if !s.Called("todos.register[/r]") || v.cur == nil || v.cur.id != "root:/r" || s.Sets["App.todoStoreIndex"] != 2 {
		t.Errorf("load: calls %v, shown %+v, index %v", s.Calls, v.cur, s.Sets["App.todoStoreIndex"])
	}
	v.folderAs = "new"
	v.folderChosen("/n")
	if !s.Called("todos.scaffold[map[root:/n]]") {
		t.Errorf("new: %v", s.Calls)
	}
	v.storeChosen(0)
	v.folderAs = "move"
	v.folderChosen("/m")
	if !s.Called("todos.move_store[kb /m/.todo-list]") {
		t.Errorf("move: %v", s.Calls)
	}
	v.folderChosen("")
}

// TestTodosAnswersDoNotCancelEachOther: a newer listing of the stores drops an older one's answer
// and its error; a write is never dropped.
func TestTodosAnswersDoNotCancelEachOther(t *testing.T) {
	s := scriptedStore(true)
	s.Deferred = true
	v := newTodos(s)
	failing := true
	s.Answers["todos.stores"] = func([]any) (any, error) {
		if failing {
			failing = false
			return nil, errors.New("-32060 the daemon is busy")
		}
		return []any{}, nil
	}
	v.list() // [0], fails
	v.list() // [1]
	s.Deliver(1, 0)
	if len(s.Failures) != 0 {
		t.Errorf("a stale answer's error was shown: %v", s.Failures)
	}
	s.Deliver(0) // the labels the newer listing asked for
	s.Deliver(0) // then the workspace's resolve
	if got := storeLabels(v); len(got) != 1 {
		t.Fatalf("stores %q", got)
	}
	// a write is never dropped: two adds both land
	s.Answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "x"}, nil }
	s.Pending = nil
	v.addTitle("one")
	v.addTitle("two")
	s.Deliver(1, 0)
	if n := strings.Count(strings.Join(s.Notes, "|"), "todos: added x"); n != 2 {
		t.Errorf("%d adds answered, want 2: %v", n, s.Notes)
	}
}

// TestTodosAtTheEdges: with no workspace the stores alone are listed; with no store the keys do
// nothing; a refused focus or close is said.
func TestTodosAtTheEdges(t *testing.T) {
	s := scriptedStore(true)
	s.WS = ""
	v := newTodos(s)
	v.list()
	if got := storeLabels(v); !slices.Equal(got, []string{"p"}) || v.cur.id != "root:/p" {
		t.Errorf("no workspace: stores %q", got)
	}

	idle := newScripted("")
	idle.Answers["todos.stores"] = func([]any) (any, error) { return []any{}, nil }
	w := newTodos(idle)
	w.list()
	if w.cur != nil || idle.Sets["App.todoHasStore"] != false || idle.Sets["App.todoStoreIndex"] != -1 {
		t.Errorf("no store: %+v, index %v", w.cur, idle.Sets["App.todoStoreIndex"])
	}
	n := len(idle.Calls)
	w.add()
	w.chooseStatus()
	w.refresh()
	w.label()
	w.enter()
	w.folderAs = "move"
	w.folderChosen("/m")
	w.Event("todos.changed", "kb", "")
	if len(idle.Calls) != n {
		t.Errorf("with no store, the keys asked %v", idle.Calls[n:])
	}
	cmds := w.Commands()
	for _, c := range []string{"App.todoLoad", "App.todoNew", "App.todoMove", "App.todoFocusStore"} {
		if err := cmds[c].Run(nil); err != nil {
			t.Errorf("%s: %v", c, err)
		}
	}
	if got := strings.Join(idle.Opened, ","); got != "todoFolder,todoFolderNew" { // M on no store: nothing
		t.Errorf("the folder dialog opened %q", got)
	}
	if err := cmds["App.todoMoved"].Run([]qml.SpecValue{{Kind: qml.SpecValueNumber, Raw: "x"}, {Kind: qml.SpecValueNumber, Raw: "1"}}); err == nil {
		t.Error("a tab that is no number was taken")
	}
	w.moved(9, 0) // no such tab

	sc := newScripted("kb")
	sc.ViewErr = errors.New("no such view")
	x := newTodos(sc)
	x.focus("todoMonths")
	if len(sc.Failures) != 1 || !strings.Contains(sc.Failures[0], "todos: the keyboard") {
		t.Errorf("a refused focus: %v", sc.Failures)
	}
}
