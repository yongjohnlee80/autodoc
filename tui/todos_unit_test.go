package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// scriptedCore answers a feature's daemon calls from a table and runs Go at once, recording what
// the feature asked.
type scriptedCore struct {
	ws      string
	answers map[string]func(params []any) (any, error)
	calls   []string
	set     map[string]any
	opened  []string
	asked   []Question
	closed  []string
	files   []string
	notes   []string
	failed  []string
	views   []string    // the view methods called, "id.method"
	listing FileList    // what Files answers
	spaces  []Workspace // what Workspaces answers
	page    Page        // what Page answers
	crumbs  []string    // the breadcrumb last put on the frame
	focused bool        // what HasFocus answers
	viewErr error       // what View answers
	// deferred holds each Go's answer until deliver runs it: answers late and in any order
	deferred bool
	pending  []func()
}

func newScripted(ws string) *scriptedCore {
	return &scriptedCore{ws: ws, answers: map[string]func([]any) (any, error){}, set: map[string]any{}}
}

func (s *scriptedCore) Set(name string, v any)   { s.set[name] = v }
func (*scriptedCore) AgentEntries() []AgentEntry { return nil }
func (*scriptedCore) HandTask(AgentRef, TaskRef, string, func(error)) error {
	return ErrAgentUnavailable
}
func (*scriptedCore) SendText(AgentRef, string, func(error)) error { return ErrAgentUnavailable }
func (s *scriptedCore) Open(id string)                             { s.opened = append(s.opened, id) }
func (s *scriptedCore) Close(id string)                            { s.closed = append(s.closed, id) }
func (s *scriptedCore) Notify(msg string)                          { s.notes = append(s.notes, msg) }

// Ask records the question and holds it for answer: its OnYes runs when the test says Yes.
func (s *scriptedCore) Ask(q Question) Asked {
	s.asked = append(s.asked, q)
	return Asked{}
}

// yes answers the latest question Yes.
func (s *scriptedCore) yes()                       { s.asked[len(s.asked)-1].OnYes() }
func (s *scriptedCore) Workspace() string          { return s.ws }
func (s *scriptedCore) Prefs() prefs               { return prefs{} }
func (s *scriptedCore) OpenFile(abs string)        { s.files = append(s.files, abs) }
func (s *scriptedCore) OpenRef(ws, path string)    { s.files = append(s.files, ws+":"+path) }
func (s *scriptedCore) Say(msg string)             { s.notes = append(s.notes, msg) }
func (s *scriptedCore) SetPref(name, value string) { s.set[name] = value }
func (s *scriptedCore) Files() FileList            { return s.listing }
func (s *scriptedCore) Workspaces() []Workspace    { return s.spaces }
func (s *scriptedCore) HasFocus(string) bool       { return s.focused }
func (s *scriptedCore) SearchModelsChanged(ranker bool) {
	s.views = append(s.views, fmt.Sprint("search.changed ranker=", ranker))
}
func (s *scriptedCore) Post(fn func())        { fn() }
func (s *scriptedCore) TogglePanel(id string) { s.views = append(s.views, id+".toggle") }
func (s *scriptedCore) Page() Page            { return s.page }
func (s *scriptedCore) Crumbs(parts []string) { s.crumbs = parts }
func (s *scriptedCore) GoToLine(line int) {
	s.views = append(s.views, fmt.Sprint("editor.line ", line))
}
func (s *scriptedCore) View(id, method string, _ ...any) error {
	s.views = append(s.views, id+"."+method)
	return s.viewErr
}
func (s *scriptedCore) Failed(what string, err error) {
	s.failed = append(s.failed, what+": "+err.Error())
}

// Ticket is the host's latest-wins rule over the sequence (there is no connection to move).
func (s *scriptedCore) Ticket(seq *uint64, _ TicketScope) Ticket {
	if seq == nil {
		return Ticket{}
	}
	*seq++
	n := *seq
	return Ticket{current: func() bool { return *seq == n }}
}

func (s *scriptedCore) Go(fn func(context.Context) any, then func(any)) {
	v := fn(context.Background())
	if s.deferred {
		s.pending = append(s.pending, func() { then(v) })
		return
	}
	then(v)
}

// deliver runs the held answers, in the order given (indexes into pending at the call).
func (s *scriptedCore) deliver(order ...int) {
	held := s.pending
	s.pending = nil
	for _, i := range order {
		held[i]()
	}
}
func (s *scriptedCore) Call(_ context.Context, verb string, params ...any) (any, error) {
	s.calls = append(s.calls, fmt.Sprint(verb, params))
	if a, ok := s.answers[verb]; ok {
		return a(params)
	}
	return nil, nil
}

func (s *scriptedCore) called(prefix string) bool {
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// a store with one open task, claimed or not
func scriptedStore(claimed bool) *scriptedCore {
	s := newScripted("kb")
	s.answers["todos.resolve"] = func([]any) (any, error) {
		return map[string]any{"store": "/kb/.todo-list", "claimed": claimed}, nil
	}
	s.answers["todos.list"] = func(p []any) (any, error) {
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
	s.answers["todos.stores"] = func([]any) (any, error) {
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
	if len(none.notes) != 1 || len(none.views) != 0 {
		t.Errorf("no workspace: notes %v, views %v", none.notes, none.views)
	}
	s := scriptedStore(false)
	v := newTodos(s)
	v.toggle()
	if len(s.views) != 1 || s.views[0] != "todos.toggle" {
		t.Errorf("SPC x: views %v", s.views)
	}
	v.Panels()[0].Opened(nil)
	if got, want := storeLabels(v), []string{"kb", "p"}; !slices.Equal(got, want) {
		t.Fatalf("the dropdown %q, want %q (the rootless store left out)", got, want)
	}
	if s.set["App.todoStoreIndex"] != 0 || v.cur.id != "ws:kb" || s.set["App.todoHasStore"] != true {
		t.Errorf("the store shown: index %v, %+v", s.set["App.todoStoreIndex"], v.cur)
	}
	var reads []string
	for _, c := range s.calls {
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
	s.answers["todos.list"] = func(p []any) (any, error) {
		return map[string]any{"tasks": []any{map[string]any{"id": "x", "title": fmt.Sprint(p[0])}}}, nil
	}
	s.deferred = true
	v.reload() // kb's Open, held
	held := s.pending
	s.pending = nil
	s.deferred = false
	v.storeChosen(1)
	if v.cur.id != "root:/p" || !s.called("todos.list[map[root:/p] map[buckets:[open]]]") {
		t.Fatalf("p chosen: %+v, calls %v", v.cur, s.calls)
	}
	held[0]() // kb's Open, late
	if got := tabRows(v, 0); !slices.Equal(got, []string{"1  map[root:/p]"}) {
		t.Errorf("p's Open after kb's late answer: %q", got)
	}
	n := len(s.calls)
	v.storeChosen(1) // the same store: nothing read
	v.storeChosen(9) // no such store
	if len(s.calls) != n {
		t.Errorf("the same store chosen again read %v", s.calls[n:])
	}
}

// TestTodosTabsAreNumberedTablesReadWhenShown: a tab's tasks are read when it first shows, errored
// ones first and marked, every row numbered; the details are the task under the cursor's; 1–5 and
// [ ] change the tab, round, the keyboard in its table; a tab read already is not read again until
// a change; Enter opens the task's file and closes the panel.
func TestTodosTabsAreNumberedTablesReadWhenShown(t *testing.T) {
	s := scriptedStore(true)
	s.answers["todos.list"] = func(p []any) (any, error) {
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
	if s.set["App.todoOpenIndex"] != 0 {
		t.Errorf("the cursor %v", s.set["App.todoOpenIndex"])
	}
	d := fmt.Sprint(s.set["App.todoDetail"])
	for _, want := range []string{"Ship open", "priority: high", "tags:", "errors:", "adr[0] not-found: gone", "the notes"} {
		if !strings.Contains(d, want) {
			t.Errorf("details lack %q:\n%s", want, d)
		}
	}
	v.moved(0, 1)
	if !strings.Contains(fmt.Sprint(s.set["App.todoDetail"]), "## Clean open") {
		t.Errorf("the cursor moved: %v", s.set["App.todoDetail"])
	}
	v.moved(2, 0) // a table not shown moves its own cursor only
	if !strings.Contains(fmt.Sprint(s.set["App.todoDetail"]), "## Clean open") {
		t.Errorf("a hidden table moved the details: %v", s.set["App.todoDetail"])
	}
	s.views = nil
	v.showTab(2)
	if s.set["App.todoTab"] != 2 || !s.called("todos.list[kb map[buckets:[deferred]]]") || !slices.Contains(s.views, "todoDeferredTable.forceActiveFocus") {
		t.Fatalf("3: tab %v, views %v", s.set["App.todoTab"], s.views)
	}
	if got := tabRows(v, 2); len(got) != 2 {
		t.Errorf("Deferred %q", got)
	}
	for _, step := range []struct{ by, want int }{{1, 3}, {1, 4}, {1, 0}, {-1, 4}, {-1, 3}} {
		v.stepTab(step.by)
		if v.tab != step.want || s.set["App.todoTab"] != step.want {
			t.Fatalf("a step of %d: tab %d, want %d", step.by, v.tab, step.want)
		}
	}
	n := len(s.calls)
	v.showTab(2) // read already, nothing changed
	v.showTab(7) // no such tab
	if len(s.calls) != n {
		t.Errorf("a tab read already was read again: %v", s.calls[n:])
	}
	v.activated(2, 1)
	if !slices.Equal(s.files, []string{"/kb/.todo-list/deferred/1.md"}) || !slices.Contains(s.views, "todos.close") {
		t.Errorf("Enter on a task: files %v, views %v", s.files, s.views)
	}
	v.activated(2, 9) // no such row
	if len(s.files) != 1 {
		t.Errorf("Enter on no row opened %v", s.files)
	}
}

// TestTodosArchivedTabListsItsMonths: Archived lists its months with their counts, and a month's
// tasks are read only when it opens; Backspace puts the keyboard back in the months; a read again
// keeps the month open, or closes it when it is gone.
func TestTodosArchivedTabListsItsMonths(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	s.views = nil
	v.showTab(archivedTab)
	if v.months.Len() != 1 || !slices.Contains(s.views, "todoMonths.forceActiveFocus") {
		t.Fatalf("months %d, views %v", v.months.Len(), s.views)
	}
	if s.called("todos.list[kb map[buckets:[archived/2026/09]]]") {
		t.Fatal("a month was read before it opened")
	}
	v.monthMoved(0)
	v.enter() // Enter on the card: the month under the cursor opens
	if !s.called("todos.list[kb map[buckets:[archived/2026/09]]]") || !slices.Equal(tabRows(v, archivedTab), []string{"1  Old"}) {
		t.Fatalf("the month: calls %v, rows %q", s.calls, tabRows(v, archivedTab))
	}
	if !slices.Contains(s.views, "todoArchivedTable.forceActiveFocus") || !strings.Contains(fmt.Sprint(s.set["App.todoDetail"]), "## Old") {
		t.Errorf("the month's tasks: views %v, details %v", s.views, s.set["App.todoDetail"])
	}
	s.views = nil
	v.monthsBack()
	if !slices.Equal(s.views, []string{"todoMonths.forceActiveFocus"}) {
		t.Errorf("Backspace: %v", s.views)
	}
	v.reload()
	if v.month != "2026/09" || len(tabRows(v, archivedTab)) != 1 {
		t.Errorf("read again: month %q, rows %q", v.month, tabRows(v, archivedTab))
	}
	s.answers["todos.list"] = func([]any) (any, error) {
		return map[string]any{"partitions": []any{map[string]any{"month": "2026/10", "files": int64(1)}}}, nil
	}
	v.reload()
	if v.month != "" || len(tabRows(v, archivedTab)) != 0 {
		t.Errorf("the month gone: month %q, rows %q", v.month, tabRows(v, archivedTab))
	}
	v.monthChosen(5) // no such month
	v.showTab(0)
	s.views = nil
	v.monthsBack() // not on Archived: nothing
	if len(s.views) != 0 {
		t.Errorf("Backspace off Archived: %v", s.views)
	}
}

// TestTodosWritesAClaimedStoreAndNotAReadOnlyOne: on the claimed store a, s, d, R and M write to the
// store shown, an added task shown in Open under the cursor; on one the daemon has not claimed each
// writes nothing and says nothing (the action row says it is read-only), and R reads it again.
func TestTodosWritesAClaimedStoreAndNotAReadOnlyOne(t *testing.T) {
	s := scriptedStore(true)
	s.answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "t1", "status": "open"}, nil }
	s.answers["todos.refresh"] = func([]any) (any, error) { return map[string]any{"moved": int64(1)}, nil }
	v := newTodos(s)
	v.list()
	v.showTab(2)
	v.add()
	if s.opened[len(s.opened)-1] != "todoNew" {
		t.Fatalf("opened %v", s.opened)
	}
	v.addTitle("  ")
	if s.called("todos.add") {
		t.Error("an empty title was added")
	}
	v.addTitle(" Write it ")
	if v.tab != 0 || s.set["App.todoTab"] != 0 || s.set["App.todoOpenIndex"] != 0 {
		t.Errorf("the added task: tab %d, cursor %v", v.tab, s.set["App.todoOpenIndex"])
	}
	v.remove()
	if len(s.asked) != 1 || s.asked[0].Title != "remove the task?" {
		t.Fatalf("asked %v", s.asked)
	}
	s.yes()
	v.chooseStatus()
	v.statusPick.accept(1, "") // in-progress
	for _, want := range []string{"todos.add[kb map[title:Write it]]", "todos.remove[kb t1 r1]", "todos.status[kb t1 r1 in-progress]"} {
		if !s.called(want) {
			t.Errorf("no %s: %v", want, s.calls)
		}
	}
	v.refresh()
	if !s.called("todos.refresh") || !strings.Contains(strings.Join(s.notes, "|"), "refreshed: 1 moved") {
		t.Errorf("refresh: %v %v", s.calls, s.notes)
	}
	v.toggleDetail()
	if s.set["App.todoDetailShown"] != false {
		t.Errorf("i: details shown %v", s.set["App.todoDetailShown"])
	}
	s.answers["todos.remove"] = func([]any) (any, error) { return nil, errors.New("-32071 the task changed") }
	s.yes()
	if len(s.failed) != 1 || !strings.Contains(s.failed[0], "todos.remove") {
		t.Errorf("failed %v", s.failed)
	}

	ro := scriptedStore(false)
	w := newTodos(ro)
	w.list()
	n := len(ro.calls)
	w.add()
	w.remove()
	w.chooseStatus()
	w.chooseFolder("move")
	if len(ro.calls) != n || len(ro.notes) != 0 || len(ro.opened) != 0 || len(ro.asked) != 0 {
		t.Errorf("a read-only store was written: %v, notes %v, opened %v", ro.calls[n:], ro.notes, ro.opened)
	}
	w.refresh() // read-only: the tab read again, nothing reconciled
	if len(ro.calls) == n || ro.called("todos.refresh") {
		t.Errorf("refresh on a read-only store: %v", ro.calls[n:])
	}
}

// TestTodosEventsAndFolders: an event for the store shown reads its tab again, another's nothing;
// Load registers the folder and shows its store; New registers and scaffolds it; M moves the store
// shown.
func TestTodosEventsAndFolders(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	n := len(s.calls)
	v.Event("todos.changed", "kb", "")
	v.Event("todos.changed", "elsewhere", "x")
	if got := len(s.calls) - n; got != 1 {
		t.Errorf("%d reads after the events, want the one tab shown of kb", got)
	}
	v.storeChosen(1)
	n = len(s.calls)
	v.Event("todos.changed", "", "/p#t9")
	if got := len(s.calls) - n; got != 1 {
		t.Errorf("%d reads after /p's event", got)
	}
	v.chooseFolder("load")
	if s.opened[len(s.opened)-1] != "todoFolder" {
		t.Errorf("folder dialog %v", s.opened)
	}
	s.answers["todos.stores"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "/p/.todo-list", "roots": []any{"/p"}, "claimed": true},
			map[string]any{"path": "/r/.todo-list", "roots": []any{"/r"}}}, nil
	}
	v.folderChosen("/r/.todo-list")
	if !s.called("todos.register[/r]") || v.cur == nil || v.cur.id != "root:/r" || s.set["App.todoStoreIndex"] != 2 {
		t.Errorf("load: calls %v, shown %+v, index %v", s.calls, v.cur, s.set["App.todoStoreIndex"])
	}
	v.folderAs = "new"
	v.folderChosen("/n")
	if !s.called("todos.scaffold[map[root:/n]]") {
		t.Errorf("new: %v", s.calls)
	}
	v.storeChosen(0)
	v.folderAs = "move"
	v.folderChosen("/m")
	if !s.called("todos.move_store[kb /m/.todo-list]") {
		t.Errorf("move: %v", s.calls)
	}
	v.folderChosen("")
}

// TestTodosAnswersDoNotCancelEachOther: a newer listing of the stores drops an older one's answer
// and its error; a write is never dropped.
func TestTodosAnswersDoNotCancelEachOther(t *testing.T) {
	s := scriptedStore(true)
	s.deferred = true
	v := newTodos(s)
	failing := true
	s.answers["todos.stores"] = func([]any) (any, error) {
		if failing {
			failing = false
			return nil, errors.New("-32060 the daemon is busy")
		}
		return []any{}, nil
	}
	v.list() // [0], fails
	v.list() // [1]
	s.deliver(1, 0)
	if len(s.failed) != 0 {
		t.Errorf("a stale answer's error was shown: %v", s.failed)
	}
	s.deliver(0) // the labels the newer listing asked for
	s.deliver(0) // then the workspace's resolve
	if got := storeLabels(v); len(got) != 1 {
		t.Fatalf("stores %q", got)
	}
	// a write is never dropped: two adds both land
	s.answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "x"}, nil }
	s.pending = nil
	v.addTitle("one")
	v.addTitle("two")
	s.deliver(1, 0)
	if n := strings.Count(strings.Join(s.notes, "|"), "todos: added x"); n != 2 {
		t.Errorf("%d adds answered, want 2: %v", n, s.notes)
	}
}

// TestTodosAtTheEdges: with no workspace the stores alone are listed; with no store the keys do
// nothing; a refused focus or close is said.
func TestTodosAtTheEdges(t *testing.T) {
	s := scriptedStore(true)
	s.ws = ""
	v := newTodos(s)
	v.list()
	if got := storeLabels(v); !slices.Equal(got, []string{"p"}) || v.cur.id != "root:/p" {
		t.Errorf("no workspace: stores %q", got)
	}

	idle := newScripted("")
	idle.answers["todos.stores"] = func([]any) (any, error) { return []any{}, nil }
	w := newTodos(idle)
	w.list()
	if w.cur != nil || idle.set["App.todoHasStore"] != false || idle.set["App.todoStoreIndex"] != -1 {
		t.Errorf("no store: %+v, index %v", w.cur, idle.set["App.todoStoreIndex"])
	}
	n := len(idle.calls)
	w.add()
	w.chooseStatus()
	w.refresh()
	w.label()
	w.enter()
	w.folderAs = "move"
	w.folderChosen("/m")
	w.Event("todos.changed", "kb", "")
	if len(idle.calls) != n {
		t.Errorf("with no store, the keys asked %v", idle.calls[n:])
	}
	cmds := w.Commands()
	for _, c := range []string{"App.todoLoad", "App.todoNew", "App.todoMove", "App.todoFocusStore"} {
		if err := cmds[c].Run(nil); err != nil {
			t.Errorf("%s: %v", c, err)
		}
	}
	if got := strings.Join(idle.opened, ","); got != "todoFolder,todoFolderNew" { // M on no store: nothing
		t.Errorf("the folder dialog opened %q", got)
	}
	if err := cmds["App.todoMoved"].Run([]qml.SpecValue{{Kind: qml.SpecValueNumber, Raw: "x"}, {Kind: qml.SpecValueNumber, Raw: "1"}}); err == nil {
		t.Error("a tab that is no number was taken")
	}
	w.moved(9, 0) // no such tab

	sc := newScripted("kb")
	sc.viewErr = errors.New("no such view")
	x := newTodos(sc)
	x.focus("todoMonths")
	if len(sc.failed) != 1 || !strings.Contains(sc.failed[0], "todos: the keyboard") {
		t.Errorf("a refused focus: %v", sc.failed)
	}
}
