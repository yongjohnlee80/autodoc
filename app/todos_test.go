package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// TestAFreshWorkspacesStoreReadsEmpty: an agent's first todos call, on a workspace whose store is
// not made yet, is answered over --call as an empty store's, and makes nothing on disk; a root no
// one registered is refused, as before (ADR 1791532020 r13).
func TestAFreshWorkspacesStoreReadsEmpty(t *testing.T) {
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	kb, other := mustEval(t, t.TempDir()), mustEval(t, t.TempDir())
	cfg := writeConfig(t, sock, state, "", "kb="+kb)
	start(t, cfg, sock)
	callJSON := func(method, params string) (map[string]any, error) {
		var out bytes.Buffer
		if err := runCall(context.Background(), cfg, method, params, &out); err != nil {
			return nil, err
		}
		var v map[string]any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v: %s", method, err, out.String())
		}
		return v, nil
	}
	refused := func(err error) int64 {
		var ce *CallError
		if !errors.As(err, &ce) {
			return 0
		}
		return ce.Code
	}
	l, err := callJSON("todos.list", `["kb"]`)
	if err != nil {
		t.Fatalf("todos.list of a fresh workspace: %v", err)
	}
	if _, ok := l["tasks"].([]any); !ok || len(asSlice(l["tasks"])) != 0 || len(asSlice(l["malformed"])) != 0 || l["cursor"] != float64(0) {
		t.Errorf("todos.list of a fresh workspace: %v, want no tasks, no malformed, cursor 0", l)
	}
	if _, err := callJSON("todos.get", `["kb", "nope"]`); refused(err) != todo.CodeNoTask {
		t.Errorf("todos.get of a fresh workspace: %v, want no_task", err)
	}
	if c, err := callJSON("todos.changes", `["kb", "agent"]`); err != nil || len(asSlice(c["changes"])) != 0 || c["resync"] == true {
		t.Errorf("todos.changes of a fresh workspace: %v %v, want nothing", c, err)
	}
	if _, err := callJSON("todos.list", `[{"root": "`+other+`"}]`); refused(err) != todo.CodeNotAStore {
		t.Errorf("todos.list of a root no one registered: %v, want not_a_store", err)
	}
	for _, root := range []string{kb, other} {
		if _, err := os.Lstat(filepath.Join(root, ".todo-list")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a read made %s/.todo-list: %v", root, err)
		}
	}
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// TestATaskIsCheckedByTheTodoFormatNotTheKBSchema (ADR 1792015212 amendment 2): with a KB schema
// that requires a type, a task the daemon wrote has no diagnostic, from doc.validate or the index.
// A file outside the store still has the KB schema's.
func TestATaskIsCheckedByTheTodoFormatNotTheKBSchema(t *testing.T) {
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	kb := mustEval(t, t.TempDir())
	schemaSrc := "version: 1\nfrontmatter:\n  type: {type: string, enum: [note], required: true}\n"
	if err := os.WriteFile(filepath.Join(kb, "schema.yaml"), []byte(schemaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, sock, state, "", "kb="+kb)
	start(t, cfg, sock)
	ctx := context.Background()
	callJSON := func(method, params string) any {
		t.Helper()
		var out bytes.Buffer
		if err := runCall(ctx, cfg, method, params, &out); err != nil {
			t.Fatalf("%s %s: %v", method, params, err)
		}
		var v any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v: %s", method, err, out.String())
		}
		return v
	}
	callJSON("workspace.configure", `["kb", {"schema": "`+filepath.Join(kb, "schema.yaml")+`"}]`)
	task := callJSON("todos.add", `["kb", {"title": "The first task", "priority": "medium"}]`).(map[string]any)
	path := ".todo-list/" + task["path"].(string)
	src, err := os.ReadFile(filepath.Join(kb, path))
	if err != nil {
		t.Fatal(err)
	}
	validate := func(p, content string) []any {
		t.Helper()
		b, _ := json.Marshal([]any{"kb", p, content})
		r := callJSON("doc.validate", string(b)).(map[string]any)
		return asSlice(r["diagnostics"])
	}
	if d := validate(path, string(src)); len(d) != 0 {
		t.Errorf("doc.validate of the task: %v, want none (the todo format's judgement)", d)
	}
	if d := validate("note.md", "---\ntitle: x\n---\n"); len(d) != 1 {
		t.Errorf("doc.validate of a file outside the store: %v, want the KB schema's one (type required)", d)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		docs := callJSON("index.documents", `["kb", {"diagnosed": true, "limit": 50}]`).(map[string]any)
		listed := false
		for _, d := range asSlice(docs["docs"]) {
			listed = listed || d.(map[string]any)["path"] == path
		}
		indexed := callJSON("index.documents", `["kb", {"limit": 50}]`).(map[string]any)
		seen := false
		for _, d := range asSlice(indexed["docs"]) {
			seen = seen || d.(map[string]any)["path"] == path
		}
		if seen && !listed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the task indexed %v, diagnosed %v: want indexed and not diagnosed", seen, listed)
		}
	}
}
