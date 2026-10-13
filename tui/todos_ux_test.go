package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// todos_ux_test.go: the todos panel as Johno asked for it after v0.1.41-rc1: it opens large, the
// user's size kept; its status line says only what needs the user; and a store not claimed takes
// no key as an error.

// TestTodosPanelOpensLargeAndKeepsTheUsersSize: before the user sizes it the panel is about 65% of
// the window wide and 80% tall, at any edge; a size the user leaves it at is kept, per edge, and
// wins, and so does a place they float it to.
func TestTodosPanelOpensLargeAndKeepsTheUsersSize(t *testing.T) {
	p := prefs{panelDefs: []PanelSpec{todosPanelSpec()}}
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
	r := [4]float64{5, 6, 30, 40}
	st := floatState(map[string][4]float64{"todos": r}, p.panelDefs)
	if st["App.todosFloating"] != true || st["App.todosX"] != 5.0 || st["App.todosW"] != 30.0 || st["App.todosH"] != 40.0 {
		t.Errorf("a place the user floated it to: %v", st)
	}
}

// TestTodosStatusLineSaysOnlyWhatNeedsTheUser: listing the stores, opening sections and months, and
// moving the cursor put nothing in the status line, which stays hidden: no count of stores, months
// or tasks, no guidance. A file a section could not list is said, where it is, until the stores
// are listed again.
func TestTodosStatusLineSaysOnlyWhatNeedsTheUser(t *testing.T) {
	s := scriptedStore(true)
	s.answers["todos.list"] = func(p []any) (any, error) {
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
		if s.set["App.todosStatus"] != "" || s.set["App.todosStatusShown"] != false || len(s.notes) != 0 {
			t.Errorf("%s: status %q (shown %v), notes %q", when, s.set["App.todosStatus"], s.set["App.todosStatusShown"], s.notes)
		}
	}
	v.list()
	quiet("the stores listed")
	open := openSection(t, v, "ws:kb", "open")
	quiet("a section read")
	archived := openSection(t, v, "ws:kb", "archived")
	quiet("Archived read")
	v.model.FetchMore(tuidecl.Index{Row: 0, Parent: &archived})
	quiet("a month read")
	v.moved(tuidecl.Index{Row: 0, Parent: &open})
	v.chooseFolder("load", "load a todo store: its project's folder")
	quiet("the cursor moved, and Load…")
	if s.set["App.todoFolderTitle"] != "load a todo store: its project's folder" {
		t.Errorf("the folder dialog's title: %q", s.set["App.todoFolderTitle"])
	}
	openSection(t, v, "root:/p", "deferred")
	if got := s.set["App.todosStatus"]; got != "⚠ 2 malformed files not listed: 2 in p › Deferred" || s.set["App.todosStatusShown"] != true {
		t.Errorf("malformed files: %q (shown %v)", got, s.set["App.todosStatusShown"])
	}
	openSection(t, v, "ws:kb", "deferred")
	if got := s.set["App.todosStatus"]; got != "⚠ 4 malformed files not listed: 2 in kb › Deferred, 2 in p › Deferred" {
		t.Errorf("malformed files in two stores: %q", got)
	}
	v.list()
	quiet("listed again")
	for name, val := range s.set {
		if text := fmt.Sprint(val); strings.Contains(text, "open a section") || strings.Contains(text, "open one for its tasks") ||
			strings.Contains(text, " tasks") || strings.Contains(text, "? keys") {
			t.Errorf("%s says %q", name, text)
		}
	}
}

// TestTodosAReadOnlyStoreTakesNoKeyAsAnError: on a store the daemon has not claimed, every key of
// the panel, on its row, a section's and a task's, writes nothing, says nothing and fails nothing:
// the action row says it is read-only. R reads it again, and L labels it (AutoDoc's own name for
// it). New… on a folder whose store is not claimed loads it and says its store waits.
func TestTodosAReadOnlyStoreTakesNoKeyAsAnError(t *testing.T) {
	writes := []string{"todos.add", "todos.remove", "todos.status", "todos.assign", "todos.move_store", "todos.scaffold", "todos.refresh"}
	s := &agentCore{scriptedCore: scriptedStore(false), entries: []AgentEntry{{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe", Running: true}}}
	v := newTodos(s)
	v.list()
	open := openSection(t, v, "ws:kb", "open")
	cmds := v.Commands()
	for kind, ix := range map[string]tuidecl.Index{"store": {Row: 0}, "section": open, "task": {Row: 0, Parent: &open}} {
		v.moved(ix)
		for _, key := range v.Keys() {
			if key.Scope != "todos" || key.Run == "" {
				continue // ? opens its card: the QML's
			}
			name, arg, _ := strings.Cut(strings.TrimSuffix(key.Run, ")"), "(")
			if arg != "" { // 1–5: a section
				var i int
				if _, err := fmt.Sscan(arg, &i); err != nil {
					t.Fatal(err)
				}
				v.section(i)
			} else if err := cmds[name].handler(name)(nil); err != nil {
				t.Errorf("on a read-only %s, %s: %v", kind, key.Show, err)
			}
			for _, d := range []string{"todoFolder", "todoNew", "todoStatusPicker", "agentAssignPicker"} {
				if slices.Contains(s.opened, d) {
					t.Errorf("on a read-only %s, %s opened %s", kind, key.Show, d)
				}
			}
			v.moved(ix) // where the key left the cursor, back on the row
		}
		for _, w := range writes {
			if s.called(w) {
				t.Errorf("on a read-only %s, a key wrote: %v", kind, s.calls)
			}
		}
		if len(s.notes) != 0 || len(s.failed) != 0 || len(s.asked) != 0 {
			t.Errorf("on a read-only %s: notes %q, failed %q, asked %v", kind, s.notes, s.failed, s.asked)
		}
	}
	if !s.called("todos.list") || !slices.Contains(s.opened, "todoLabel") {
		t.Errorf("R read nothing again, or L asked for no label: calls %v, opened %v", s.calls, s.opened)
	}

	n := len(s.calls)
	v.folderAs = "new"
	v.folderChosen("/n")
	if s.called("todos.scaffold") || len(s.notes) != 1 || s.notes[0] != "todos: /n is loaded, read-only: its new store waits for the cutover" ||
		!slices.Contains(s.calls[n:], "todos.stores[]") {
		t.Errorf("New… on a folder not claimed: calls %v, notes %q", s.calls[n:], s.notes)
	}
}
