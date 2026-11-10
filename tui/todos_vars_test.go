package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// varsStore is scriptedStore with todos.vars answered: a derived built-in, a variable from the KB's
// file, a machine-only one with no value here, and one a migration holds in conflict.
func varsStore(claimed bool) *featuretest.ScriptedCore {
	s := scriptedStore(claimed)
	s.Answers["todos.vars"] = func([]any) (any, error) {
		return []any{
			map[string]any{"name": "KB_ROOT", "value": "/kb", "builtin": true, "derived": "/kb", "source": "derived"},
			map[string]any{"name": "PROJ", "value": "/src/p", "source": "kb"},
			map[string]any{"name": "DUMPS", "machine": true},
			map[string]any{"name": "P", "source": "conflict", "conflict": true},
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

// The Variables tab (6) lists the store's workspace's variables as todos.vars answers them, each value
// and where it came from, or its conflict; the task actions hide there and the variable actions show.
// e sets the value under the cursor in the KB's file (a machine-only name: for this machine), m for
// this machine,
// a adds NAME=value to the file, d clears a value set here (asked first). [ ] reach it round.
func TestTheVariablesTab(t *testing.T) {
	s := varsStore(true)
	v := newTodos(s)
	v.Panels()[0].Opened(nil)
	v.showTab(varsTab)
	if !s.Called("todos.vars[") {
		t.Fatalf("the tab did not read todos.vars: %v", s.Calls)
	}
	if got := strings.Join(varRows(v), " | "); got != "$KB_ROOT derived /kb | $PROJ kb /src/p | $DUMPS machine (set per machine: none here) | "+
		"$P conflict (conflict: set it in the KB's file, or machine-only)" {
		t.Errorf("rows %q", got)
	}
	if s.Sets["App.todoOnVars"] != true || s.Sets["App.todoActStatus"] != false || s.Sets["App.todoActAdd"] != false ||
		s.Sets["App.todoActVarSet"] != true || s.Sets["App.todoActVarAdd"] != true {
		t.Errorf("actions on the tab: %v", s.Sets)
	}
	if d, _ := s.Sets["App.todoDetail"].(string); !strings.Contains(d, "## $KB_ROOT") || !strings.Contains(d, "derived from the workspace: /kb") {
		t.Errorf("details %q", d)
	}
	if s.Sets["App.todoActVarClear"] != false {
		t.Error("a derived value offers to be cleared")
	}

	v.varMoved(3) // $P, in conflict: nothing set here to clear, and the details say what settles it
	if d, _ := s.Sets["App.todoDetail"].(string); s.Sets["App.todoActVarClear"] != false || !strings.Contains(d, "in conflict") {
		t.Errorf("a conflict: clear %v, details %q", s.Sets["App.todoActVarClear"], d)
	}
	v.varMoved(1) // $PROJ, from the KB's file
	if s.Sets["App.todoActVarClear"] != true {
		t.Error("a value from the KB's file offers no clear")
	}
	v.enter()
	if s.Opened[len(s.Opened)-1] != "todoVar" || !strings.Contains(s.Sets["App.todoVarTitle"].(string), "the KB's file") ||
		s.Sets["App.todoVarText"] != "/src/p" {
		t.Fatalf("Enter on a variable: opened %v, %v", s.Opened, s.Sets)
	}
	v.varSet("/src/q")
	if !s.Called("todos.set_var[PROJ /src/q map[at:") || !strings.Contains(s.Calls[len(s.Calls)-2], "scope:store") {
		t.Errorf("e's set: %v", s.Calls)
	}

	v.varMoved(2) // $DUMPS, machine-only: e keeps it for this machine
	v.varEdit()
	v.varSet("/data")
	if !s.Called("todos.set_var[DUMPS /data map[at:") || !strings.Contains(strings.Join(s.Calls, "\n"), "DUMPS /data map[at:kb scope:machine]") {
		t.Errorf("a machine-only name's set: %v", s.Calls)
	}

	v.add()
	v.varSet("NEW = ../x")
	if !s.Called("todos.set_var[NEW ../x map[at:") {
		t.Errorf("a: %v", s.Calls)
	}
	v.varSet("") // no prompt open: nothing
	v.add()
	v.varSet("no-equals")
	if s.Notes[len(s.Notes)-1] != "todos: a variable is NAME=value" {
		t.Errorf("a without NAME=value: %v", s.Notes)
	}

	v.varMoved(1)
	v.remove()
	if len(s.Asked) == 0 {
		t.Fatal("d did not ask")
	}
	s.Yes()
	if !strings.Contains(strings.Join(s.Calls, "\n"), "todos.set_var[PROJ  map[at:kb scope:store]]") {
		t.Errorf("d's clear: %v", s.Calls)
	}

	v.showTab(archivedTab)
	v.stepTab(1)
	if v.tab != varsTab {
		t.Errorf("] after Archived: tab %d", v.tab)
	}
	v.stepTab(1)
	if v.tab != 0 || s.Sets["App.todoOnVars"] != false || s.Sets["App.todoActVarSet"] != false {
		t.Errorf("] after Variables: tab %d, %v", v.tab, s.Sets)
	}

	ro := varsStore(false)
	w := newTodos(ro)
	w.Panels()[0].Opened(nil)
	w.showTab(varsTab)
	w.varEdit()
	w.add()
	if len(ro.Opened) != 0 || ro.Sets["App.todoActVarSet"] != false {
		t.Errorf("a read-only store's variables: opened %v, %v", ro.Opened, ro.Sets)
	}
}
