package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// TestTheDaemonServesTheTodosModule: the daemon's module list is the todo system over the store's
// journal and the served workspaces, and what the todo system records beyond its verbs lands in
// sys.events.
func TestTheDaemonServesTheTodosModule(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	kb := t.TempDir()
	deps := &moduleDeps{store: db, workspaces: rpc.Fixed(rpc.Static(rpc.WorkspaceInfo{Name: "kb", Root: kb})), journal: db.TodoJournal(), log: logger.Nop{}}
	ms := modules(deps)
	if len(ms) != 1 || ms[0].Name() != "todos" {
		t.Fatalf("modules %v", ms)
	}
	m := ms[0].(*todo.Manager)
	// the served workspace resolves through the daemon's roots and disk
	r, err := m.Resolve(ctx, todo.At{Workspace: "kb"})
	if err != nil || r.Store != filepath.Join(mustEval(t, kb), ".todo-list") {
		t.Fatalf("kb's store: %+v %v", r, err)
	}
	// registering a project root is an event the todo system records itself
	if _, err := m.Register(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	events, _, _, err := db.Events(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		found = found || e.Kind == "todos.register"
	}
	if !found {
		t.Errorf("no todos.register in sys.events: %+v", events)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	q, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
