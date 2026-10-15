package tui

import (
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// varsStore is scriptedStore with todos.vars answered: a derived built-in, a variable from the
// store's file and a machine-only one with no value here.
func varsStore(claimed bool) *scriptedCore {
	s := scriptedStore(claimed)
	s.answers["todos.vars"] = func([]any) (any, error) {
		return []any{
			map[string]any{"name": "KB_ROOT", "value": "/kb", "builtin": true, "derived": "/kb", "source": "derived"},
			map[string]any{"name": "PROJ", "value": "/src/p", "source": "store"},
			map[string]any{"name": "DUMPS", "machine": true},
		}, nil
	}
	return s
}

// varRows are the Variables tab's rows: "<name> <from> <value>".
func varRows(v *todos) []string {
	m := v.vars.Model()
	var out []string
	for i := 0; i < m.Len(); i++ {
		at := tuidecl.Index{Row: i}
		out = append(out, m.Data(at, "name").Raw+" "+m.Data(at, "from").Raw+" "+m.Data(at, "value").Raw)
	}
	return out
}

// The Variables tab (6) lists the store's variables as todos.vars answers them, each value and
// where it came from; the task actions hide there and the variable actions show. e sets the value
// under the cursor in the store's file (a machine-only name: for this machine), m for this machine,
// a adds NAME=value to the file, d clears a value set here (asked first). [ ] reach it round.
func TestTheVariablesTab(t *testing.T) {
	s := varsStore(true)
	v := newTodos(s)
	v.Panels()[0].Opened(nil)
	v.showTab(varsTab)
	if !s.called("todos.vars[") {
		t.Fatalf("the tab did not read todos.vars: %v", s.calls)
	}
	if got := strings.Join(varRows(v), " | "); got != "$KB_ROOT derived /kb | $PROJ store /src/p | $DUMPS machine (set per machine: none here)" {
		t.Errorf("rows %q", got)
	}
	if s.set["App.todoOnVars"] != true || s.set["App.todoActStatus"] != false || s.set["App.todoActAdd"] != false ||
		s.set["App.todoActVarSet"] != true || s.set["App.todoActVarAdd"] != true {
		t.Errorf("actions on the tab: %v", s.set)
	}
	if d, _ := s.set["App.todoDetail"].(string); !strings.Contains(d, "## $KB_ROOT") || !strings.Contains(d, "derived from the store's place: /kb") {
		t.Errorf("details %q", d)
	}
	if s.set["App.todoActVarClear"] != false {
		t.Error("a derived value offers to be cleared")
	}

	v.varMoved(1) // $PROJ, from the store's file
	if s.set["App.todoActVarClear"] != true {
		t.Error("a value from the store's file offers no clear")
	}
	v.enter()
	if s.opened[len(s.opened)-1] != "todoVar" || !strings.Contains(s.set["App.todoVarTitle"].(string), "the store's file") ||
		s.set["App.todoVarText"] != "/src/p" {
		t.Fatalf("Enter on a variable: opened %v, %v", s.opened, s.set)
	}
	v.varSet("/src/q")
	if !s.called("todos.set_var[PROJ /src/q map[at:") || !strings.Contains(s.calls[len(s.calls)-2], "scope:store") {
		t.Errorf("e's set: %v", s.calls)
	}

	v.varMoved(2) // $DUMPS, machine-only: e keeps it for this machine
	v.varEdit()
	v.varSet("/data")
	if !s.called("todos.set_var[DUMPS /data map[at:") || !strings.Contains(strings.Join(s.calls, "\n"), "DUMPS /data map[at:kb scope:machine]") {
		t.Errorf("a machine-only name's set: %v", s.calls)
	}

	v.add()
	v.varSet("NEW = ../x")
	if !s.called("todos.set_var[NEW ../x map[at:") {
		t.Errorf("a: %v", s.calls)
	}
	v.varSet("") // no prompt open: nothing
	v.add()
	v.varSet("no-equals")
	if s.notes[len(s.notes)-1] != "todos: a variable is NAME=value" {
		t.Errorf("a without NAME=value: %v", s.notes)
	}

	v.varMoved(1)
	v.remove()
	if len(s.asked) == 0 {
		t.Fatal("d did not ask")
	}
	s.yes()
	if !strings.Contains(strings.Join(s.calls, "\n"), "todos.set_var[PROJ  map[at:kb scope:store]]") {
		t.Errorf("d's clear: %v", s.calls)
	}

	v.showTab(archivedTab)
	v.stepTab(1)
	if v.tab != varsTab {
		t.Errorf("] after Archived: tab %d", v.tab)
	}
	v.stepTab(1)
	if v.tab != 0 || s.set["App.todoOnVars"] != false || s.set["App.todoActVarSet"] != false {
		t.Errorf("] after Variables: tab %d, %v", v.tab, s.set)
	}

	ro := varsStore(false)
	w := newTodos(ro)
	w.Panels()[0].Opened(nil)
	w.showTab(varsTab)
	w.varEdit()
	w.add()
	if len(ro.opened) != 0 || ro.set["App.todoActVarSet"] != false {
		t.Errorf("a read-only store's variables: opened %v, %v", ro.opened, ro.set)
	}
}
