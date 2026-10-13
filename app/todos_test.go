package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/sqlitejournal"
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
	deps := &moduleDeps{store: db, workspaces: rpc.Fixed(rpc.Static(rpc.WorkspaceInfo{Name: "kb", Root: kb})), journal: sqlitejournal.New(db), log: logger.Nop{}}
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

// TestTheDaemonRefreshesClaimedTodoStores: the daemon runs the daily refresh of claimed todo stores
// for as long as it serves (ADR 1791532020 §2.3). At its start it refreshes a claimed store never
// refreshed: a task completed long ago is archived with no client asking, and the todos.changed it
// logs names the workspace, as the verb's does. It stops with the daemon.
func TestTheDaemonRefreshesClaimedTodoStores(t *testing.T) {
	ctx := context.Background()
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	kb := mustEval(t, t.TempDir())
	cfg := writeConfig(t, sock, state, "", "kb="+kb)
	// the store claimed and scaffolded before the daemon starts, through the same todo system: its
	// refresh at the daemon's start is the only one
	db, err := store.Open(ctx, filepath.Join(state, config.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	deps := &moduleDeps{store: db, workspaces: rpc.Fixed(rpc.Static(rpc.WorkspaceInfo{Name: "kb", Root: kb})), journal: sqlitejournal.New(db), log: logger.Nop{}}
	modules(deps)
	if err := deps.todos.Claim(ctx, todo.At{Workspace: "kb"}); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.todos.Scaffold(ctx, todo.At{Workspace: "kb"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ts := "2020-01-01T09:00:00Z"
	task := "---\nid: \"old\"\nversion: 1\nstatus: completed\ntitle: \"Old\"\ncreated: \"" + ts + "\"\nupdated: \"" + ts +
		"\"\nstatus_changed: \"" + ts + "\"\ncompleted_at: \"" + ts + "\"\n---\n\n# Old\n"
	if err := os.WriteFile(filepath.Join(kb, ".todo-list", "completed", "old.md"), []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}

	d := start(t, cfg, sock)
	cli := dial(t, sock)
	var events []any
	logged := func() bool {
		events = call(t, cli, "sys.events", int64(0), int64(100)).(map[string]any)["events"].([]any)
		for _, e := range events {
			if e := e.(map[string]any); e["kind"] == "todos.changed" && e["workspace"] == "kb" {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(10 * time.Second); !logged(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no todos.changed for kb from the daily refresh: events %v\n%s", events, d.out)
		}
	}
	if got := call(t, cli, "todos.get", "kb", "old").(map[string]any); got["status"] != "archived" {
		t.Errorf("the task completed in 2020: %+v", got)
	}
	d.stop()
}
