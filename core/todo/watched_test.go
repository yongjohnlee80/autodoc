package todo_test

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// storesChanged is how many times WithStoresChanged was told since the last call.
func (e *env) storesChanged() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.stores
	e.stores = 0
	return n
}

// TestTheRegistrySaysWhenItsStoresChange (docs/decisions/todos.md#which-paths-are-graph-only): a
// write that changes the stores the registry holds, their paths, or the variables they resolve
// tells WithStoresChanged (the daemon reads the stores once for every call made while it reads);
// one that changes none of them (a store's last use, a task) does not. The workspaces read the
// stores again on it.
func TestTheRegistrySaysWhenItsStoresChange(t *testing.T) {
	e := newEnv(t)
	e.must(e.m.Claim(ctx, kb))
	if n := e.storesChanged(); n == 0 {
		t.Error("the first claim made a store and said nothing")
	}
	if _, err := e.m.Scaffold(ctx, kb); err != nil {
		t.Fatal(err)
	}
	e.add(todo.Spec{ID: "t", Title: "t"})
	e.get("t")
	e.advance(48 * 3600e9)
	e.get("t")
	if n := e.storesChanged(); n != 0 {
		t.Errorf("tasks and a store's use said stores_changed %d times", n)
	}
	for _, c := range []struct {
		what string
		set  func() error
	}{
		{"a global var", func() error { return e.m.SetVar(ctx, "G", "/g") }},
		{"a global var cleared", func() error { return e.m.SetVar(ctx, "G", "") }},
		{"this machine's value", func() error {
			return e.m.SetVar(ctx, "M", "/m", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine})
		}},
	} {
		e.must(c.set())
		if n := e.storesChanged(); n == 0 {
			t.Errorf("%s: never told", c.what)
		}
	}
	e.must(e.m.SetVar(ctx, "G", "")) // already clear: nothing changed
	if n := e.storesChanged(); n != 0 {
		t.Errorf("a set that changed nothing: stores_changed %d times", n)
	}
	e.must(e.m.SetVar(ctx, "F", "/f", todo.SetVarOpts{At: &kb})) // the store's file
	if kinds := e.eventKinds(); !slices.Contains(kinds, "todos.vars") {
		t.Errorf("a store-file var logged %v, want todos.vars", kinds)
	}
	if n := e.storesChanged(); n == 0 {
		t.Error("a store-file var: never told")
	}
	e.must(e.m.SetVar(ctx, "F", "", todo.SetVarOpts{At: &kb})) // the file holds the machine list still
	e.must(e.m.SetVar(ctx, "M", "", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine}))
	e.storesChanged()
	if _, err := e.m.MoveStore(ctx, kb, "/kb/moved"); err != nil {
		t.Fatal(err)
	}
	if n := e.storesChanged(); n == 0 {
		t.Error("a store move said nothing")
	}
}
