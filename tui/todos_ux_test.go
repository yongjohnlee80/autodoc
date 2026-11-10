package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/parse/qml"
)

// todos_ux_test.go: the todos panel as it now works: it opens large, the
// user's size kept; its status line says only what needs the user; and a released store takes
// no key as an error.

// TestTodosPanelOpensLargeAndKeepsTheUsersSize: before the user sizes it the panel is about 65% of
// the window wide and 80% tall, at any edge, the left one unless chosen; a size the user leaves it
// at is kept, per edge, and wins, and so do an edge they chose and a place they float it to.
func TestTodosPanelOpensLargeAndKeepsTheUsersSize(t *testing.T) {
	p := prefs{panelDefs: []feature.PanelSpec{todosPanelSpec()}}
	for edge, want := range map[string][2]int{"right": {65, 80}, "left": {65, 80}, "top": {80, 65}, "bottom": {80, 65}} {
		if s, l := p.panelGeometry("todos", edge); s != want[0] || l != want[1] {
			t.Errorf("at the %s edge, before it is sized: %d × %d, want %d × %d", edge, s, l, want[0], want[1])
		}
	}
	p.todosEdge = "right"
	if st := panelState(p); st["App.todosSize"] != 65 || st["App.todosLength"] != 80 {
		t.Errorf("the document reads %v × %v", st["App.todosSize"], st["App.todosLength"])
	}
	readPanelPrefs(&p, map[string]any{"tui.todos.right.size": "40", "tui.todos.right.length": "50", "tui.todos.top.size": "99"})
	if s, l := p.panelGeometry("todos", "right"); s != 40 || l != 50 {
		t.Errorf("the user's size at the right edge: %d × %d, want 40 × 50", s, l)
	}
	if s, l := p.panelGeometry("todos", "top"); s != 80 || l != 65 {
		t.Errorf("an out-of-bounds size kept: %d × %d, want the default", s, l)
	}
	// the edge: the left by default, a kept one (the right) winning
	if p := defaultPrefs(); p.todosEdge != "left" {
		t.Errorf("a fresh state's todos edge: %q, want left", p.todosEdge)
	}
	var kept prefs
	feature.ReadRows(&kept, map[string]any{prefTodosEdge: "right"}, legacyPrefRows(nil, false))
	if kept.todosEdge != "right" || todosPanelSpec().Edge(kept) != "right" {
		t.Errorf("a kept right edge read as %q, the panel's %q", kept.todosEdge, todosPanelSpec().Edge(kept))
	}
	r := [4]float64{5, 6, 30, 40}
	st := floatState(map[string][4]float64{"todos": r}, p.panelDefs)
	if st["App.todosFloating"] != true || st["App.todosX"] != 5.0 || st["App.todosW"] != 30.0 || st["App.todosH"] != 40.0 {
		t.Errorf("a place the user floated it to: %v", st)
	}
}

// TestTodosStatusLineSaysOnlyWhatNeedsTheUser: listing the stores, showing tabs and months, choosing
// another store and moving the cursor put nothing in the status line, which stays hidden: no count
// of stores, months or tasks, no guidance. A file a tab could not list is said, where it is, until
// another store shows.
func TestTodosStatusLineSaysOnlyWhatNeedsTheUser(t *testing.T) {
	s := scriptedStore(true)
	s.Answers["todos.list"] = func(p []any) (any, error) {
		switch b := p[1].(map[string]any)["buckets"].([]any)[0].(string); b {
		case "archived":
			return map[string]any{"partitions": []any{map[string]any{"month": "2026/09", "files": int64(1)}}}, nil
		case "deferred":
			return map[string]any{"tasks": []any{}, "malformed": []any{map[string]any{"path": "deferred/a.md"}, map[string]any{"path": "deferred/b.md"}}}, nil
		}
		return map[string]any{"tasks": []any{map[string]any{"id": "t1", "rev": "r1", "title": "Ship", "status": "open"}}}, nil
	}
	v := newTodos(s)
	quiet := func(when string) {
		t.Helper()
		if s.Sets["App.todosStatus"] != "" || s.Sets["App.todosStatusShown"] != false || len(s.Notes) != 0 {
			t.Errorf("%s: status %q (shown %v), notes %q", when, s.Sets["App.todosStatus"], s.Sets["App.todosStatusShown"], s.Notes)
		}
	}
	v.list()
	quiet("the stores listed, Open read")
	v.showTab(1)
	quiet("a tab shown")
	v.showTab(archivedTab)
	quiet("Archived's months")
	v.monthChosen(0)
	quiet("a month read")
	v.moved(archivedTab, 0)
	v.chooseFolder("load")
	quiet("the cursor moved, and Load…")
	if s.Opened[len(s.Opened)-1] != "todoFolder" { // its title says what the folder is for
		t.Errorf("Load… opened %v", s.Opened)
	}
	v.showTab(2)
	if got := s.Sets["App.todosStatus"]; got != "⚠ 2 malformed files not listed: 2 in Deferred" || s.Sets["App.todosStatusShown"] != true {
		t.Errorf("malformed files: %q (shown %v)", got, s.Sets["App.todosStatusShown"])
	}
	v.showTab(0)
	if got := s.Sets["App.todosStatus"]; got != "⚠ 2 malformed files not listed: 2 in Deferred" {
		t.Errorf("another tab shown: %q", got)
	}
	v.storeChosen(1)
	quiet("another store")
	for name, val := range s.Sets {
		if text := fmt.Sprint(val); strings.Contains(text, "open a section") || strings.Contains(text, "open one for its tasks") ||
			strings.Contains(text, " tasks") || strings.Contains(text, "? keys") || strings.Contains(text, "stores:") {
			t.Errorf("%s says %q", name, text)
		}
	}
}

// TestTodosAReadOnlyStoreTakesNoKeyAsAnError: on a store todos.release gave back, every key of the
// panel, on a task, on an empty tab and on Archived's months, writes nothing, says nothing and fails
// nothing: the action row says it is read-only. R reads it again, and L labels it (AutoDoc's own
// name for it). New… on a folder whose store was released loads it and says so.
func TestTodosAReadOnlyStoreTakesNoKeyAsAnError(t *testing.T) {
	writes := []string{"todos.add", "todos.remove", "todos.status", "todos.assign", "todos.move_store", "todos.scaffold", "todos.refresh"}
	s := &agentCore{ScriptedCore: scriptedStore(false), entries: []feature.AgentEntry{{Ref: feature.AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true}}}
	v := newTodos(s)
	v.list()
	cmds := v.Commands()
	for _, where := range []struct {
		kind string
		tab  int
	}{{"task", 0}, {"empty tab", 1}, {"months", archivedTab}} {
		place := func() { v.showTab(where.tab) }
		place()
		for _, key := range v.Keys() {
			if key.Scope != "todos" || key.Run == "" {
				continue // ? opens its card: the QML's
			}
			name, arg, _ := strings.Cut(strings.TrimSuffix(key.Run, ")"), "(")
			var err error
			if arg != "" {
				err = cmds[name].Run([]qml.SpecValue{{Kind: qml.SpecValueNumber, Raw: arg}})
			} else {
				err = cmds[name].Handler(name)(nil)
			}
			if err != nil {
				t.Errorf("on a read-only %s, %s: %v", where.kind, key.Show, err)
			}
			for _, d := range []string{"todoFolderMove", "todoNew", "todoStatusPicker", "agentAssignPicker"} {
				if slices.Contains(s.Opened, d) {
					t.Errorf("on a read-only %s, %s opened %s", where.kind, key.Show, d)
				}
			}
			place() // where the key left the tab, back
		}
		for _, w := range writes {
			if s.Called(w) {
				t.Errorf("on a read-only %s, a key wrote: %v", where.kind, s.Calls)
			}
		}
		if len(s.Notes) != 0 || len(s.Failures) != 0 || len(s.Asked) != 0 {
			t.Errorf("on a read-only %s: notes %q, failed %q, asked %v", where.kind, s.Notes, s.Failures, s.Asked)
		}
	}
	if !s.Called("todos.list") || !slices.Contains(s.Opened, "todoLabel") {
		t.Errorf("R read nothing again, or L asked for no label: calls %v, opened %v", s.Calls, s.Opened)
	}

	n := len(s.Calls)
	v.folderAs = "new"
	v.folderChosen(taskFile("/n"))
	if s.Called("todos.scaffold") || len(s.Notes) != 1 || s.Notes[0] != "todos: "+taskFile("/n")+" is loaded, read-only: its store was released (todos.release)" ||
		!slices.Contains(s.Calls[n:], "todos.stores[]") {
		t.Errorf("New… on a folder whose store was released: calls %v, notes %q", s.Calls[n:], s.Notes)
	}
}

// TestTodosEnterOnTheCardOfKeysOpensTheRow: Enter on ?'s card is Enter in the tab shown: the task
// under the cursor's file opens; on Archived's months, the month under their cursor; with no task,
// nothing. A write asked of a released store is never sent.
func TestTodosEnterOnTheCardOfKeysOpensTheRow(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	if err := v.Commands()["App.todoEnter"].Handler("App.todoEnter")(nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.OpenedFiles, []string{taskFile("/kb/.todo-list/open/t1.md")}) {
		t.Errorf("Enter on a task: files %v", s.OpenedFiles)
	}
	s.ViewErr = errors.New("the panel is gone")
	v.enter()
	if len(s.Failures) != 1 || s.Failures[0] != "todos: open: the panel is gone" {
		t.Errorf("a refused close: failed %v", s.Failures)
	}
	s.ViewErr = nil
	v.showTab(archivedTab)
	v.monthMoved(0)
	v.enter()
	if v.month != "2026/09" || len(tabRows(v, archivedTab)) != 1 {
		t.Errorf("Enter on a month: %q, rows %q", v.month, tabRows(v, archivedTab))
	}
	v.showTab(1) // empty
	n := len(s.OpenedFiles)
	v.enter()
	if len(s.OpenedFiles) != n {
		t.Errorf("Enter on an empty tab opened %v", s.OpenedFiles[n:])
	}

	ro := scriptedStore(false)
	w := newTodos(ro)
	w.list()
	n = len(ro.Calls)
	w.write(w.stores[0], "todos.add", []any{"kb", map[string]any{"title": "x"}}, nil)
	if len(ro.Calls) != n {
		t.Errorf("a write to a store not claimed was sent: %v", ro.Calls[n:])
	}
}
