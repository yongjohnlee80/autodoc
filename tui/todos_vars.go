package tui

import (
	"fmt"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE VARIABLES TAB (ADR 1792015069 §2.3): the store's variables as todos.vars answers them, the same
// from every route to the store: each one's value, and where it came from (this machine, the store's
// file, derived from the store's place, a global var, the environment). e sets the one under the
// cursor in the store's file, which travels with the KB (a machine-only name: for this machine), m
// for this machine only, a adds one (NAME=value) to the file, d clears the value set here. A built-in
// overridden and then cleared is derived again.

// varsTab is the tab after the statuses' tabs.
var varsTab = len(todoBuckets)

// varRowOf is a variable as the tab's row.
func varRowOf(_ int, v map[string]any) rowOf {
	value := str(v, "value")
	switch {
	case value == "" && v["machine"] == true:
		value = "(set per machine: none here)"
	case value == "":
		value = "(unset)"
	}
	from := str(v, "source")
	if v["machine"] == true && from == "" {
		from = "machine"
	}
	return rowOf{"key": str(v, "name"), "name": "$" + str(v, "name"), "value": value, "from": from}
}

// newVarRows is the tab's model.
func newVarRows() *rows[map[string]any] {
	return newRows(tuidecl.NewListModel("key", "name", "value", "from"), varRowOf)
}

// readVars reads the store shown's variables into the tab.
func (t *todos) readVars() {
	s, gen := t.cur, t.gen
	if s == nil {
		return
	}
	t.call("todos.vars", []any{s.at}, &t.varsSeq, func(v any) {
		if gen != t.gen {
			return
		}
		var vars []map[string]any
		for _, x := range asList(v) {
			vars = append(vars, asMap(x))
		}
		keep := str(t.varAt(t.varCursor), "name")
		t.vars.Reset(vars)
		at := -1
		for i, x := range vars {
			if str(x, "name") == keep {
				at = i
			}
		}
		if at < 0 && len(vars) > 0 {
			at = min(max(t.varCursor, 0), len(vars)-1)
		}
		t.varCursor = at
		t.c.Set("App.todoVarsIndex", -1)
		t.c.Set("App.todoVarsIndex", at)
		if t.tab == varsTab {
			t.showDetail()
		}
	})
}

// varAt is the tab's row i; nil for none.
func (t *todos) varAt(i int) map[string]any {
	x, _ := t.vars.At(i)
	return x
}

// varMoved is the tab's cursor moving.
func (t *todos) varMoved(i int) {
	if i < 0 && t.vars.Len() > 0 {
		return // selectRow's move away for a moment, as in moved
	}
	t.varCursor = i
	if t.tab == varsTab {
		t.showDetail()
	}
}

// varDetail is the details of the variable under the cursor.
func varDetail(v map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## $%s\n\n", str(v, "name"))
	if val := str(v, "value"); val != "" {
		fmt.Fprintf(&b, "- value: %s\n- from: %s\n", val, str(v, "source"))
	} else {
		b.WriteString("- value: none here\n")
	}
	if d := str(v, "derived"); d != "" {
		fmt.Fprintf(&b, "- derived from the store's place: %s\n", d)
	}
	if v["builtin"] == true {
		b.WriteString("- built in: derived per store; a value set here overrides it\n")
	}
	if v["machine"] == true {
		b.WriteString("- machine-only: its value is set on each machine, never in the store's file\n")
	}
	return b.String()
}

// syncVarActions sets the action row on the Variables tab: set (store's file, or this machine for a
// machine-only name), set for this machine, add, and clear a value set here.
func (t *todos) syncVarActions() {
	s := t.cur
	writable := s != nil && s.claimed
	v := t.varAt(t.varCursor)
	src := str(v, "source")
	t.c.Set("App.todoActVarSet", writable && v != nil)
	t.c.Set("App.todoActVarAdd", writable)
	t.c.Set("App.todoActVarClear", writable && v != nil && (src == "store" || src == "machine" || src == "global"))
}

// varEdit is e (and Enter) on the tab: a value for the variable under the cursor asked for, its
// value filled in; kept in the store's file, or for this machine when the name is machine-only.
func (t *todos) varEdit() { t.askVar("store") }

// varMachine is m: a value for the variable under the cursor, for this machine only.
func (t *todos) varMachine() { t.askVar("machine") }

// askVar opens the prompt for the variable under the cursor, its value to be kept in scope.
func (t *todos) askVar(scope string) {
	s, v := t.cur, t.varAt(t.varCursor)
	if t.readOnly(s) || v == nil || t.tab != varsTab {
		return
	}
	if v["machine"] == true {
		scope = "machine"
	}
	where := "the store's file"
	if scope == "machine" {
		where = "this machine"
	}
	t.varEditing = &todoVarEdit{store: s, name: str(v, "name"), scope: scope}
	t.c.Set("App.todoVarTitle", fmt.Sprintf("$%s, kept in %s", str(v, "name"), where))
	t.c.Set("App.todoVarText", str(v, "value"))
	t.c.Open("todoVar")
}

// varAdd is a on the tab: NAME=value asked for, kept in the store's file.
func (t *todos) varAdd() {
	s := t.cur
	if t.readOnly(s) || t.tab != varsTab {
		return
	}
	t.varEditing = &todoVarEdit{store: s, scope: "store"}
	t.c.Set("App.todoVarTitle", "a variable, kept in the store's file: NAME=value")
	t.c.Set("App.todoVarText", "")
	t.c.Open("todoVar")
}

// todoVarEdit is the variable the prompt is for: its store, name ("" while adding) and scope.
type todoVarEdit struct {
	store       *todoStore
	name, scope string
}

// varSet keeps the value typed for the variable asked about (NAME=value while adding); an empty value
// clears it.
func (t *todos) varSet(text string) {
	t.c.Close("todoVar")
	e := t.varEditing
	t.varEditing = nil
	if e == nil {
		return
	}
	name, value := e.name, strings.TrimSpace(text)
	if name == "" {
		n, v, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(n) == "" {
			t.c.Notify("todos: a variable is NAME=value")
			return
		}
		name, value = strings.TrimPrefix(strings.TrimSpace(n), "$"), strings.TrimSpace(v)
	}
	t.setVar(e.store, name, value, e.scope)
}

// varClear is d on the tab: the value set here for the variable under the cursor removed (asked
// first), where it was set: the store's file, this machine, or the global vars.
func (t *todos) varClear() {
	s, v := t.cur, t.varAt(t.varCursor)
	if t.readOnly(s) || v == nil {
		return
	}
	scope, name := str(v, "source"), str(v, "name")
	if scope != "store" && scope != "machine" && scope != "global" {
		return
	}
	t.c.Ask(Question{Title: "clear the variable?", Text: fmt.Sprintf("Clear $%s's value set in %s?", name, scope),
		OnYes: func() { t.setVar(s, name, "", scope) }})
}

// setVar is todos.set_var for store s, then the tab read again.
func (t *todos) setVar(s *todoStore, name, value, scope string) {
	opts := map[string]any{"scope": scope}
	if scope != "global" {
		opts["at"] = s.at
	}
	t.call("todos.set_var", []any{name, value, opts}, nil, func(any) {
		if value == "" {
			t.c.Notify(fmt.Sprintf("todos: $%s cleared (%s)", name, scope))
		} else {
			t.c.Notify(fmt.Sprintf("todos: $%s set (%s)", name, scope))
		}
		if s == t.cur {
			t.readVars()
		}
	})
}
