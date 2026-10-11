package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// scriptedCore answers a feature's daemon calls from a table and runs Go at once, recording what
// the feature asked.
type scriptedCore struct {
	ws      string
	answers map[string]func(params []any) (any, error)
	calls   []string
	set     map[string]any
	opened  []string
	closed  []string
	files   []string
	notes   []string
	failed  []string
	// deferred holds each Go's answer until deliver runs it: answers late and in any order
	deferred bool
	pending  []func()
}

func newScripted(ws string) *scriptedCore {
	return &scriptedCore{ws: ws, answers: map[string]func([]any) (any, error){}, set: map[string]any{}}
}

func (s *scriptedCore) Set(name string, v any) { s.set[name] = v }
func (s *scriptedCore) Open(id string)         { s.opened = append(s.opened, id) }
func (s *scriptedCore) Close(id string)        { s.closed = append(s.closed, id) }
func (s *scriptedCore) Notify(msg string)      { s.notes = append(s.notes, msg) }
func (s *scriptedCore) Workspace() string      { return s.ws }
func (s *scriptedCore) Prefs() prefs           { return prefs{} }
func (s *scriptedCore) OpenFile(abs string)    { s.files = append(s.files, abs) }
func (s *scriptedCore) Failed(what string, err error) {
	s.failed = append(s.failed, what+": "+err.Error())
}

// Ticket is the host's latest-wins rule over the sequence (there is no connection to move).
func (s *scriptedCore) Ticket(seq *uint64, _ TicketScope) Ticket {
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

func TestTodosOpensTheWorkspacesStoreAndShowsATask(t *testing.T) {
	if s := newScripted(""); true {
		newTodos(s).open()
		if len(s.notes) != 1 || len(s.opened) != 0 {
			t.Errorf("no workspace: notes %v, opened %v", s.notes, s.opened)
		}
	}
	s := scriptedStore(false)
	v := newTodos(s)
	v.open()
	if s.opened[0] != "todosDialog" || !s.called("todos.stores") || !s.called("todos.list") {
		t.Fatalf("opened %v, calls %v", s.opened, s.calls)
	}
	if s.set["App.todosTitle"] != "todos · kb  (read-only until the cutover)" {
		t.Errorf("title %v", s.set["App.todosTitle"])
	}
	if !strings.Contains(fmt.Sprint(s.set["App.todosStatus"]), "1 tasks · 1 malformed files") {
		t.Errorf("status %v", s.set["App.todosStatus"])
	}
	d := fmt.Sprint(s.set["App.todoDetail"])
	for _, want := range []string{"Ship", "priority: high", "tags:", "errors:", "adr[0] not-found: gone", "the notes"} {
		if !strings.Contains(d, want) {
			t.Errorf("details lack %q:\n%s", want, d)
		}
	}
	v.moved(9)
	if s.set["App.todoDetail"] != "" {
		t.Error("details for no row")
	}
	v.activated(0)
	if len(s.files) != 1 || s.files[0] != "/kb/.todo-list/open/t1.md" {
		t.Errorf("opened file %v", s.files)
	}
	// read-only: every write says so and calls nothing
	v.moved(0)
	n := len(s.calls)
	v.add()
	v.remove()
	v.cycle()
	v.chooseFolder("move", "x")
	if len(s.calls) != n || len(s.notes) != 4 {
		t.Errorf("a read-only store was written: %v, notes %v", s.calls[n:], s.notes)
	}
	v.refresh() // read-only: reads again
	if !s.called("todos.resolve") || s.called("todos.refresh") {
		t.Error("refresh on a read-only store")
	}
}

func TestTodosWritesAClaimedStore(t *testing.T) {
	s := scriptedStore(true)
	s.answers["todos.add"] = func([]any) (any, error) { return map[string]any{"id": "new"}, nil }
	s.answers["todos.refresh"] = func([]any) (any, error) { return map[string]any{"moved": int64(1)}, nil }
	v := newTodos(s)
	v.open()
	v.add()
	if s.opened[len(s.opened)-1] != "todoNew" {
		t.Fatalf("opened %v", s.opened)
	}
	v.addTitle("  ")
	if s.called("todos.add") {
		t.Error("an empty title was added")
	}
	v.addTitle(" Write it ")
	if !s.called("todos.add[kb map[title:Write it]]") {
		t.Errorf("calls %v", s.calls)
	}
	v.moved(0)
	v.remove()
	if s.opened[len(s.opened)-1] != "todoRemove" {
		t.Fatalf("opened %v", s.opened)
	}
	v.removeConfirmed()
	v.cycle()
	if !s.called("todos.remove[kb t1 r1]") || !s.called("todos.status[kb t1 r1 completed]") {
		t.Errorf("calls %v", s.calls)
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
	v.removeConfirmed()
	if len(s.failed) != 1 || !strings.Contains(s.failed[0], "todos.remove") {
		t.Errorf("failed %v", s.failed)
	}
}

func TestTodosTabsArchiveStoresAndFolders(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.open()
	v.showTab(archivedTab)
	if v.months == nil || len(v.months) != 1 {
		t.Fatalf("months %v", v.months)
	}
	v.monthChosen(0)
	if !s.called("todos.list[kb map[buckets:[archived/2026/09]]]") || len(v.tasks[archivedTab]) != 1 {
		t.Errorf("month: %v", s.calls)
	}
	v.monthChosen(5)
	v.cursor = 0
	v.cycle() // an archived task: s does nothing
	if s.called("todos.status") {
		t.Error("s moved an archived task")
	}
	v.showTab(1)
	v.showTab(9)
	if v.tab != 1 {
		t.Errorf("tab %d", v.tab)
	}
	// stores: the chosen one by its root; one with no root is left alone
	v.storeChosen(0)
	if v.rootOf() != "/p" {
		t.Errorf("at %v", v.at)
	}
	v.storeChosen(1)
	v.storeChosen(7)
	if v.rootOf() != "/p" {
		t.Errorf("a store with no root was chosen: %v", v.at)
	}
	// a workspace entered does not replace a chosen root; with none chosen it does
	v.Entered("other")
	if v.rootOf() != "/p" {
		t.Error("entering a workspace replaced the chosen store")
	}
	// events: the chosen root's, and nothing else's, read it again
	n := len(s.calls)
	v.Event("todos.changed", "", "/p#t9")
	v.Event("todos.changed", "elsewhere", "x")
	if got := len(s.calls) - n; got != 2 { // resolve + list, once
		t.Errorf("%d calls after the events", got)
	}
	// folders: load registers and shows; new registers and scaffolds; M moves the store
	v.chooseFolder("load", "load")
	if s.set["App.todoFolder"] != "/p" || s.opened[len(s.opened)-1] != "todoFolder" {
		t.Errorf("folder dialog %v %v", s.set["App.todoFolder"], s.opened)
	}
	v.folderChosen("/r/.todo-list")
	if !s.called("todos.register[/r]") || v.rootOf() != "/r" {
		t.Errorf("load: %v at %v", s.calls, v.at)
	}
	v.folderAs = "new"
	v.folderChosen("/n")
	if !s.called("todos.scaffold[map[root:/n]]") {
		t.Errorf("new: %v", s.calls)
	}
	v.folderAs = "move"
	v.folderChosen("/m")
	if !s.called("todos.move_store[map[root:/n] /m/.todo-list]") {
		t.Errorf("move: %v", s.calls)
	}
	v.folderChosen("")
	var none todos
	none.Event("todos.changed", "kb", "") // no store chosen: nothing
	fresh := newTodos(newScripted("kb"))
	fresh.Entered("kb")
	if fresh.at != "kb" {
		t.Errorf("entered: %v", fresh.at)
	}
}

// TestTodosAnswersDoNotCancelEachOther: opening asks for the stores and the store at once; each
// answer arrives, in either order, and neither drops the other. Only a newer read of the same
// kind makes an answer stale, and a stale answer's error is not shown.
func TestTodosAnswersDoNotCancelEachOther(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		s := scriptedStore(true)
		s.deferred = true
		v := newTodos(s)
		v.open() // held: [0] todos.stores, [1] todos.resolve
		s.deliver(order...)
		if len(v.stores) != 2 {
			t.Errorf("order %v: the stores answer was dropped: %v", order, v.stores)
		}
		s.deliver(0) // the list the resolve asked for
		if len(v.tasks[0]) != 1 {
			t.Errorf("order %v: the open tasks were dropped", order)
		}
	}
	// two reads of the store: the older answer is dropped, its error unseen
	s := scriptedStore(true)
	s.deferred = true
	v := newTodos(s)
	v.at, v.atLabel = "kb", "kb"
	failing := true
	s.answers["todos.resolve"] = func([]any) (any, error) {
		if failing {
			failing = false
			return nil, errors.New("-32060 no such workspace")
		}
		return map[string]any{"store": "/kb/.todo-list", "claimed": true}, nil
	}
	v.load() // [0], fails
	v.load() // [1]
	s.deliver(1, 0)
	if len(s.failed) != 0 {
		t.Errorf("a stale answer's error was shown: %v", s.failed)
	}
	if !v.claimed {
		t.Error("the latest read was not applied")
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
