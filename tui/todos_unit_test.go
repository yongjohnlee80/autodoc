package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

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
func (s *scriptedCore) Open(id string)    { s.opened = append(s.opened, id) }
func (s *scriptedCore) Close(id string)   { s.closed = append(s.closed, id) }
func (s *scriptedCore) Notify(msg string) { s.notes = append(s.notes, msg) }

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

// treeLabels are the labels of the rows under parent (nil: the roots).
func treeLabels(v *todos, parent *tuidecl.Index) []string {
	var out []string
	for r := 0; r < v.model.RowCount(parent); r++ {
		out = append(out, v.model.Data(tuidecl.Index{Row: r, Parent: parent}, "label").Raw)
	}
	return out
}

// openSection opens a section of a store, as the tree does when it first opens one, and is its row.
func openSection(t *testing.T, v *todos, store, bucket string) tuidecl.Index {
	t.Helper()
	ix, ok := v.indexOf(store, bucketKey(store, bucket))
	if !ok {
		t.Fatalf("no section %s of %s: %v", bucket, store, treeLabels(v, nil))
	}
	v.model.FetchMore(ix)
	return ix
}

// TestTodosListsTheStoresAsTheTreesRoots: SPC x toggles the panel, saying so when there is no
// workspace and no store; opened, the workspace's store is the first root and every other with a
// root of its own follows, ● on those the daemon writes; each has the five sections, read only when
// one opens.
func TestTodosListsTheStoresAsTheTreesRoots(t *testing.T) {
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
	if got, want := treeLabels(v, nil), []string{"  kb · ws kb  (read-only until the cutover)", "● p"}; !slices.Equal(got, want) {
		t.Fatalf("roots %q, want %q (the rootless store left out)", got, want)
	}
	if got := treeLabels(v, &tuidecl.Index{Row: 0}); !slices.Equal(got, []string{"Open", "In progress", "Deferred", "Completed", "Archived"}) {
		t.Errorf("sections %q", got)
	}
	if s.called("todos.list") {
		t.Error("a section was read before it opened")
	}
	if spec := v.Panels()[0]; spec.ID != "todos" || spec.Edge(prefs{todosEdge: "right"}) != "right" {
		t.Errorf("the panel %+v", spec)
	}
}

// TestTodosReadsASectionWhenItOpens: a section's tasks are read as it opens, errored ones first
// and marked, the open section's numbered; the details are the task under the cursor's; Enter opens
// its file and closes the panel, and on a section opens or closes it.
func TestTodosReadsASectionWhenItOpens(t *testing.T) {
	s := scriptedStore(false)
	s.answers["todos.list"] = func(p []any) (any, error) {
		return map[string]any{"tasks": []any{
			map[string]any{"id": "t1", "rev": "r1", "title": "Clean", "status": "open", "path": "open/t1.md"},
			map[string]any{"id": "t2", "rev": "r2", "title": "Ship", "status": "open", "priority": "high", "path": "open/t2.md",
				"tags": []any{"x"}, "description": "the notes", "errors": []any{map[string]any{"field": "adr[0]", "code": "not-found", "message": "gone"}}}},
			"malformed": []any{map[string]any{"path": "open/bad.md"}}}, nil
	}
	v := newTodos(s)
	v.list()
	open := openSection(t, v, "ws:kb", "open")
	if got, want := treeLabels(v, &open), []string{"⚠ 1  Ship", "2  Clean"}; !slices.Equal(got, want) {
		t.Fatalf("tasks %q, want %q", got, want)
	}
	if !strings.Contains(fmt.Sprint(s.set["App.todosStatus"]), "2 tasks · 1 malformed files") {
		t.Errorf("status %v", s.set["App.todosStatus"])
	}
	ship := tuidecl.Index{Row: 0, Parent: &open}
	v.moved(ship)
	d := fmt.Sprint(s.set["App.todoDetail"])
	for _, want := range []string{"Ship", "priority: high", "tags:", "errors:", "adr[0] not-found: gone", "the notes"} {
		if !strings.Contains(d, want) {
			t.Errorf("details lack %q:\n%s", want, d)
		}
	}
	v.moved(open)
	if s.set["App.todoDetail"] != "" {
		t.Error("details for a section")
	}
	if err := v.activated(ship); err != nil {
		t.Fatal(err)
	}
	if len(s.files) != 1 || s.files[0] != "/kb/.todo-list/open/t2.md" || !slices.Contains(s.views, "todos.close") {
		t.Errorf("Enter on a task: files %v, views %v", s.files, s.views)
	}
	if err := v.activated(open); err != nil || !slices.Contains(s.views, "todoTree.toggleExpanded") {
		t.Errorf("Enter on a section: %v, views %v", err, s.views)
	}
}

// TestTodosReadsTheArchiveByMonth: Archived lists its months with their counts, and a month's tasks
// are read only when it opens; s leaves an archived task alone.
func TestTodosReadsTheArchiveByMonth(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	archived := openSection(t, v, "ws:kb", "archived")
	if got := treeLabels(v, &archived); !slices.Equal(got, []string{"2026/09  (3)"}) {
		t.Fatalf("months %q", got)
	}
	if s.called("todos.list[kb map[buckets:[archived/2026/09]]]") {
		t.Fatal("a month was read before it opened")
	}
	month := tuidecl.Index{Row: 0, Parent: &archived}
	v.model.FetchMore(month)
	if !s.called("todos.list[kb map[buckets:[archived/2026/09]]]") || !slices.Equal(treeLabels(v, &month), []string{"Old"}) {
		t.Fatalf("the month: calls %v, rows %q", s.calls, treeLabels(v, &month))
	}
	v.moved(tuidecl.Index{Row: 0, Parent: &month})
	v.cycle()
	if s.called("todos.status") {
		t.Error("s moved an archived task")
	}
}

// TestTodosWritesAClaimedStoreAndNotAReadOnlyOne: on the claimed store a, s, d, R and M write to the
// cursor's store; on one the daemon has not claimed each says so and writes nothing, and R reads it
// again.
func TestTodosWritesAClaimedStoreAndNotAReadOnlyOne(t *testing.T) {
	s := scriptedStore(true)
	s.answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "new"}, nil }
	s.answers["todos.refresh"] = func([]any) (any, error) { return map[string]any{"moved": int64(1)}, nil }
	v := newTodos(s)
	v.list()
	open := openSection(t, v, "ws:kb", "open")
	v.moved(tuidecl.Index{Row: 0, Parent: &open})
	v.add()
	if s.opened[len(s.opened)-1] != "todoNew" {
		t.Fatalf("opened %v", s.opened)
	}
	v.addTitle("  ")
	if s.called("todos.add") {
		t.Error("an empty title was added")
	}
	v.addTitle(" Write it ")
	v.moved(tuidecl.Index{Row: 0, Parent: &open})
	v.remove()
	if len(s.asked) != 1 || s.asked[0].Title != "remove the task?" {
		t.Fatalf("asked %v", s.asked)
	}
	s.yes()
	v.cycle()
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
	v.toggleHelp()
	if s.set["App.todoDetailShown"] != false || s.set["App.todoHelpShown"] != true {
		t.Errorf("toggles %v %v", s.set["App.todoDetailShown"], s.set["App.todoHelpShown"])
	}
	s.answers["todos.remove"] = func([]any) (any, error) { return nil, errors.New("-32071 the task changed") }
	s.yes()
	if len(s.failed) != 1 || !strings.Contains(s.failed[0], "todos.remove") {
		t.Errorf("failed %v", s.failed)
	}

	ro := scriptedStore(false)
	w := newTodos(ro)
	w.list()
	ropen := openSection(t, w, "ws:kb", "open")
	w.moved(tuidecl.Index{Row: 0, Parent: &ropen})
	n := len(ro.calls)
	w.add()
	w.remove()
	w.cycle()
	w.chooseFolder("move", "x")
	if len(ro.calls) != n || len(ro.notes) != 4 {
		t.Errorf("a read-only store was written: %v, notes %v", ro.calls[n:], ro.notes)
	}
	w.refresh() // read-only: the open section read again, nothing reconciled
	if len(ro.calls) == n || ro.called("todos.refresh") {
		t.Errorf("refresh on a read-only store: %v", ro.calls[n:])
	}
}

// TestTodosSectionsByNumberEventsAndFolders: 1–5 put the cursor on that section of its store; an
// event for a store listed reads its open sections again, another's nothing; Load registers and
// lists, New registers and scaffolds, M moves the cursor's store.
func TestTodosSectionsByNumberEventsAndFolders(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	v.section(3)
	if want := []string{"ws:kb", bucketKey("ws:kb", "completed")}; !slices.Equal(v.at, want) || !slices.Contains(s.views, "todoTree.setCurrentIndex") {
		t.Errorf("4: at %q, views %v", v.at, s.views)
	}
	v.section(9) // no such section: nothing
	openSection(t, v, "root:/p", "open")
	n := len(s.calls)
	v.Event("todos.changed", "", "/p#t9")
	v.Event("todos.changed", "elsewhere", "x")
	if got := len(s.calls) - n; got != 1 {
		t.Errorf("%d reads after the events, want the one open section of /p", got)
	}
	v.chooseFolder("load", "load")
	if s.opened[len(s.opened)-1] != "todoFolder" {
		t.Errorf("folder dialog %v", s.opened)
	}
	v.folderChosen("/r/.todo-list")
	if !s.called("todos.register[/r]") {
		t.Errorf("load: %v", s.calls)
	}
	v.folderAs = "new"
	v.folderChosen("/n")
	if !s.called("todos.scaffold[map[root:/n]]") {
		t.Errorf("new: %v", s.calls)
	}
	v.at = []string{"ws:kb"}
	v.folderAs = "move"
	v.folderChosen("/m")
	if !s.called("todos.move_store[kb /m/.todo-list]") {
		t.Errorf("move: %v", s.calls)
	}
	v.folderChosen("")
}

// TestTodosAnswersDoNotCancelEachOther: a newer listing of the stores drops an older one's answer
// and its error; two reads of one section, the older is dropped; a write is never dropped.
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
	if got := treeLabels(v, nil); len(got) != 1 {
		t.Fatalf("roots %q", got)
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

// TestTodosAStaleReadIsNotTheNewTrees: a section's read on its way when the stores are listed
// again answers into nothing: the new tree's section stays closed and unread, and once opened it
// is read, and a change reads it again.
func TestTodosAStaleReadIsNotTheNewTrees(t *testing.T) {
	s := scriptedStore(true)
	s.deferred = true
	v := newTodos(s)
	v.list()
	s.deliver(0) // the stores; the labels held
	s.deliver(0) // the labels; the workspace's resolve held
	s.deliver(0) // the resolve: the tree
	openSection(t, v, "ws:kb", "open")
	old := s.pending[0] // the section's read, held
	s.pending = nil
	v.list()
	s.deliver(0)
	s.deliver(0)
	s.deliver(0) // listed again: a new tree, the section closed
	old()        // the old read answers late
	section, ok := v.indexOf("ws:kb", bucketKey("ws:kb", "open"))
	if !ok || v.model.RowCount(&section) != 0 || !v.model.CanFetchMore(section) {
		t.Fatalf("the old read filled the new tree: rows %q", treeLabels(v, &section))
	}
	v.model.FetchMore(section)
	s.deliver(0)
	if got := treeLabels(v, &section); len(got) != 1 {
		t.Fatalf("the new tree's section, opened: %q", got)
	}
	n := len(s.calls)
	v.Event("todos.changed", "kb", "")
	if len(s.calls) == n {
		t.Error("a change did not read the opened section again")
	}
}

// TestTodosARefreshKeepsAnOpenMonthsTasks: a store read again with an archived month open reads the
// month after its Archived section, under the month's new row, so the month keeps its tasks
// whichever answer comes first.
func TestTodosARefreshKeepsAnOpenMonthsTasks(t *testing.T) {
	for _, monthFirst := range []bool{false, true} {
		s := scriptedStore(true)
		v := newTodos(s)
		v.list()
		archived := openSection(t, v, "ws:kb", "archived")
		v.model.FetchMore(tuidecl.Index{Row: 0, Parent: &archived})
		s.deferred = true
		v.reread(v.stores[0]) // held: [0] Archived
		if monthFirst {
			v.read(v.stores[0], "m", "2026/09") // a read of the month already on its way: [1]
			s.deliver(1, 0)                     // the month answers first, then Archived replaces its row
		} else {
			s.deliver(0)
		}
		s.deliver(0) // the month read again under its new row
		month, ok := v.indexOf("ws:kb", bucketKey("ws:kb", "archived"), monthKey("ws:kb", "2026/09"))
		if got := treeLabels(v, &month); !ok || !slices.Equal(got, []string{"Old"}) {
			t.Errorf("month first %v: the month's tasks after a refresh: %q", monthFirst, got)
		}
	}
}

// TestTodosAtTheEdges: with no workspace the stores alone are the roots; with nothing under the
// cursor, or a row whose store is gone, the keys do nothing; Load, New and M open the folder dialog;
// a section the tree no longer shows is not jumped to, and a refused jump is said; a month gone from
// Archived is no longer read again.
func TestTodosAtTheEdges(t *testing.T) {
	s := scriptedStore(true)
	s.ws = ""
	v := newTodos(s)
	v.list()
	if got := treeLabels(v, nil); !slices.Equal(got, []string{"● p"}) {
		t.Errorf("no workspace: roots %q", got)
	}

	idle := scriptedStore(true)
	w := newTodos(idle) // nothing listed: no store under the cursor
	n := len(idle.calls)
	w.add()
	w.cycle()
	w.refresh()
	w.folderAs = "move"
	w.folderChosen("/m")
	if len(idle.calls) != n {
		t.Errorf("with no store, the keys asked %v", idle.calls[n:])
	}
	cmds := w.Commands()
	for _, c := range []string{"App.todoLoad", "App.todoNew", "App.todoMove"} {
		if err := cmds[c].Run(nil); err != nil {
			t.Errorf("%s: %v", c, err)
		}
	}
	if got := strings.Join(idle.opened, ","); got != "todoFolder,todoFolder" { // M on no store: read-only, said
		t.Errorf("the folder dialog opened %q", got)
	}

	v = newTodos(scriptedStore(true))
	v.list()
	open := openSection(t, v, "ws:kb", "open")
	task := tuidecl.Index{Row: 0, Parent: &open}
	v.stores[0].path = "" // a store not yet resolved: Enter has no file to open
	if err := v.activated(task); err != nil {
		t.Fatal(err)
	}
	v.stores = v.stores[1:] // the task's store is gone
	if err := v.activated(task); err != nil {
		t.Fatal(err)
	}
	if f := v.c.(*scriptedCore).files; len(f) != 0 {
		t.Errorf("Enter opened %v", f)
	}
	v.stores = append(v.stores, &todoStore{id: "root:/z"})
	v.at = []string{"root:/z"} // a store the tree does not show
	v.section(0)
	sc := v.c.(*scriptedCore)
	if slices.Contains(sc.views, "todoTree.setCurrentIndex") {
		t.Error("jumped to a section the tree does not show")
	}
	sc.viewErr = errors.New("no such row")
	v.at = []string{"root:/p"}
	v.section(0)
	if len(sc.failed) != 1 || !strings.Contains(sc.failed[0], "a section") {
		t.Errorf("a refused jump: failed %v", sc.failed)
	}

	m := scriptedStore(true)
	a := newTodos(m)
	a.list()
	archived := openSection(t, a, "ws:kb", "archived")
	a.model.FetchMore(tuidecl.Index{Row: 0, Parent: &archived})
	m.answers["todos.list"] = func(p []any) (any, error) {
		return map[string]any{"partitions": []any{map[string]any{"month": "2026/10", "files": int64(1)}}}, nil
	}
	a.reread(a.stores[0])
	if a.open[monthKey("ws:kb", "2026/09")] {
		t.Error("a month gone from Archived is still read again")
	}
}
