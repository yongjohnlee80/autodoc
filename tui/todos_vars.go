package tui

import (
	"fmt"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE VARIABLES TAB (ADR 1792132105 §2.3): the variables of the store's workspace as todos.vars
// answers them, the same from every route to the store: each one's value, and where it came from
// (this machine, the KB's file, derived from the workspace, a global var, the environment), or a
// migration conflict waiting for a person. e sets the one under the cursor in the KB's file, which
// travels with the KB (a machine-only name: for this machine), m for this machine only, a adds one
// (NAME=value) to the file, d clears the value set here. A built-in overridden and then cleared is
// derived again.

// varsTab is the tab after the statuses' tabs.
var varsTab = len(todoBuckets)

// varRowOf is a variable as the tab's row, in c's language.
func varRowOf(c Core, v map[string]any) rowOf {
	value := str(v, "value")
	switch {
	case v["conflict"] == true && str(v, "source") == "conflict":
		value = c.Tr("autodoc.text.todos_vars.conflict_set_it_in_the_kb_s_file_or_machine_only")
	case value == "" && v["machine"] == true:
		value = c.Tr("autodoc.text.todos_vars.set_per_machine_none_here")
	case value == "":
		value = c.Tr("autodoc.text.todos_vars.unset")
	}
	from := str(v, "source")
	if v["machine"] == true && from == "" {
		from = "machine"
	}
	return rowOf{"key": str(v, "name"), "name": "$" + str(v, "name"), "value": value, "from": from}
}

// newVarRows is the tab's model, its rows in c's language.
func newVarRows(c Core) *rows[map[string]any] {
	return newRows(tuidecl.NewListModel("key", "name", "value", "from"), func(_ int, v map[string]any) rowOf { return varRowOf(c, v) })
}

// pointVarsAt moves the Variables tab's list to row at: away first, so the same row twice still
// moves it, as a reset of the rows puts it back at the top.
func (t *todos) pointVarsAt(at int) {
	t.c.Set("App.todoVarsIndex", -1)
	t.c.Set("App.todoVarsIndex", at)
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
		t.pointVarsAt(at)
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

// varDetail is the details of the variable under the cursor, in c's language.
func varDetail(c Core, v map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## $%s\n\n", str(v, "name"))
	if val := str(v, "value"); val != "" {
		b.WriteString(c.Trf("autodoc.text.todos_vars.value_from", val, str(v, "source")) + "\n")
	} else {
		b.WriteString(c.Tr("autodoc.text.todos_vars.value_none_here") + "\n")
	}
	if d := str(v, "derived"); d != "" {
		b.WriteString(c.Trf("autodoc.text.todos_vars.derived_from_the_workspace", d) + "\n")
	}
	if v["builtin"] == true {
		b.WriteString(c.Tr("autodoc.text.todos_vars.built_in_derived_per_workspace_a_value_set_here") + "\n")
	}
	if v["machine"] == true {
		b.WriteString(c.Tr("autodoc.text.todos_vars.machine_only_its_value_is_set_on_each_machine_ne") + "\n")
	}
	if v["conflict"] == true {
		b.WriteString(c.Tr("autodoc.text.todos_vars.in_conflict_the_workspace_s_stores_set_it_differ") + "\n")
	}
	return b.String()
}

// syncVarActions sets the action row on the Variables tab: set (the KB's file, or this machine for a
// machine-only name), set for this machine, add, and clear a value set here.
func (t *todos) syncVarActions() {
	s := t.cur
	writable := !t.readOnly(s)
	v := t.varAt(t.varCursor)
	src := str(v, "source")
	t.c.Set("App.todoActVarSet", writable && v != nil)
	t.c.Set("App.todoActVarAdd", writable)
	t.c.Set("App.todoActVarClear", writable && v != nil && clearable(src))
}

// clearable reports a source the tab clears a value from: one set here, not derived or from the
// environment. "store" is a KB-file value as a daemon before protocol 24 names it.
func clearable(src string) bool {
	return src == "kb" || src == "store" || src == "machine" || src == "global"
}

// varEdit is e (and Enter) on the tab: a value for the variable under the cursor asked for, its
// value filled in; kept in the KB's file, or for this machine when the name is machine-only.
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
	title := "autodoc.text.todos_vars.kept_in_the_kb_s_file" // a sentence each, as every language says them
	if scope == "machine" {
		title = "autodoc.text.todos_vars.kept_in_this_machine"
	}
	t.varEditing = &todoVarEdit{store: s, name: str(v, "name"), scope: scope}
	t.c.Set("App.todoVarTitle", t.c.Trf(title, str(v, "name")))
	t.c.Set("App.todoVarText", str(v, "value"))
	t.c.Open("todoVar")
}

// varAdd is a on the tab: NAME=value asked for, kept in the KB's file.
func (t *todos) varAdd() {
	s := t.cur
	if t.readOnly(s) || t.tab != varsTab {
		return
	}
	t.varEditing = &todoVarEdit{store: s, scope: "store"}
	t.c.Set("App.todoVarTitle", t.c.Tr("autodoc.text.todos_vars.a_variable_kept_in_the_kb_s_file_name_value"))
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
			t.c.Notify(t.c.Tr("autodoc.notice.todos_vars.todos_a_variable_is_name_value"))
			return
		}
		name, value = strings.TrimPrefix(strings.TrimSpace(n), "$"), strings.TrimSpace(v)
	}
	t.setVar(e.store, name, value, e.scope)
}

// varClear is d on the tab: the value set here for the variable under the cursor removed (asked
// first), where it was set: the KB's file, this machine, or the global vars.
func (t *todos) varClear() {
	s, v := t.cur, t.varAt(t.varCursor)
	if t.readOnly(s) || v == nil {
		return
	}
	scope, name := str(v, "source"), str(v, "name")
	if !clearable(scope) {
		return
	}
	if scope == "kb" {
		scope = "store" // todos.set_var's name for the store's workspace's file, at every protocol
	}
	t.c.Ask(Question{Title: t.c.Tr("autodoc.question.todos_vars.clear_the_variable"), Text: t.c.Trf("autodoc.question.todos_vars.clear_s_value_set_in", name, scope),
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
			t.c.Notify(t.c.Trf("autodoc.notice.todos_vars.todos_cleared", name, scope))
		} else {
			t.c.Notify(t.c.Trf("autodoc.notice.todos_vars.todos_set", name, scope))
		}
		if s == t.cur {
			t.readVars()
		}
	})
}
