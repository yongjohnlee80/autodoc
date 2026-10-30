package todo_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/todo"
)

// verb is the todos verb named name.
func verb(t *testing.T, m *todo.Manager, name string) module.Verb {
	t.Helper()
	for _, v := range m.Verbs() {
		if v.Verb == name {
			return v
		}
	}
	t.Fatalf("no verb %s", name)
	return module.Verb{}
}

// TestTheVerbsAreAModule: every todos verb checks, arrived in protocol 20 (migrate_refs in 22, set_kb and
// the workspace verbs in 24), takes where its store is first (the workspace verbs a workspace or a path),
// and the module's codes sit in its range; a module built with no dependencies lists them.
func TestTheVerbsAreAModule(t *testing.T) {
	m := todo.New(nil, nil, nil)
	if err := module.CheckCodes(m); err != nil {
		t.Fatal(err)
	}
	vs := m.Verbs()
	if len(vs) != 27 {
		t.Errorf("%d verbs", len(vs))
	}
	for _, v := range vs {
		if err := v.Check(); err != nil {
			t.Error(err)
		}
		since := int64(20)
		switch {
		case v.Verb == "todos.migrate_refs":
			since = 22
		case v.Verb == "todos.set_kb" || strings.HasPrefix(v.Verb, "workspace."):
			since = 24
		}
		if !strings.HasPrefix(v.Verb, "todos.") && !strings.HasPrefix(v.Verb, "workspace.") || v.Since != since || v.Scope != module.ScopeGlobal {
			t.Errorf("%s: since %d, scope %s", v.Verb, v.Since, v.Scope)
		}
		switch v.Verb {
		case "workspace.vars", "workspace.set_var":
			if v.Params[0].Name != "workspace" {
				t.Errorf("%s does not take its workspace first: %+v", v.Verb, v.Params)
			}
		case "workspace.of":
			if v.Params[0].Name != "path" {
				t.Errorf("%s does not take a path: %+v", v.Verb, v.Params)
			}
		case "todos.register", "todos.stores", "todos.set_var":
		default:
			if len(v.Params) == 0 || v.Params[0].Name != "at" || v.Params[0].Type != "workspace | {root}" {
				t.Errorf("%s does not take where its store is first: %+v", v.Verb, v.Params)
			}
		}
	}
	// what changes a store logs todos.changed, so every view refreshes; what does not, logs nothing
	for name, logs := range map[string]bool{"todos.add": true, "todos.update": true, "todos.status": true, "todos.assign": true,
		"todos.remove": true, "todos.refresh": true, "todos.scaffold": true, "todos.move_store": true, "todos.claim": true,
		"todos.list": false, "todos.get": false, "todos.register": false, "todos.ack": false, "todos.changes": false} {
		if got := verb(t, m, name).Logs(); got != logs {
			t.Errorf("%s logs: %v, want %v", name, got, logs)
		}
	}
	if a := verb(t, m, "todos.claim").Audience; a != module.AudienceUser {
		t.Errorf("todos.claim is for %s: only the cutover's release calls it", a)
	}
}

// TestTheVerbsServeTheManager: a todos verb called with the wire's params does what its Manager
// method does, and answers the module's codes.
func TestTheVerbsServeTheManager(t *testing.T) {
	e := claimed(t)
	call := func(name string, params ...any) (any, error) {
		return verb(t, e.m, name).Call(context.Background(), params)
	}
	out, err := call("todos.add", "kb", map[string]any{"title": "Write the module", "assignee": "agent:jarvis"})
	if err != nil {
		t.Fatal(err)
	}
	added := out.(todo.Task)
	if added.Status != "in-progress" || added.Assignee != "agent:jarvis" {
		t.Errorf("added %+v: an assignee makes it in-progress", added)
	}
	if out, err = call("todos.get", map[string]any{"root": "/kb"}, added.ID); err != nil || out.(todo.Task).Rev != added.Rev {
		t.Fatalf("get by {root}: %v %v", out, err)
	}
	if _, err = call("todos.update", "kb", added.ID, "stale-rev", map[string]any{"priority": "high"}); !errors.Is(err, todo.ErrStale) {
		t.Errorf("a stale rev: %v", err)
	}
	if _, err = call("todos.update", "kb", added.ID, added.Rev, map[string]any{"id": "x"}); !errors.Is(err, todo.ErrInvalid) {
		t.Errorf("a managed field patched: %v", err)
	}
	if out, err = call("todos.list", "kb"); err != nil || len(out.(todo.Listing).Tasks) != 1 {
		t.Fatalf("list: %v %v", out, err)
	}
	if _, err = call("todos.get", "nope", added.ID); !errors.Is(err, todo.ErrNoWorkspace) {
		t.Errorf("an unknown workspace: %v", err)
	}
	if _, err = call("todos.get", "kb"); !errors.Is(err, module.ErrInvalidParams) {
		t.Errorf("a missing id: %v", err)
	}
	if out, err = call("todos.remove", "kb", added.ID, added.Rev); err != nil || out != nil {
		t.Errorf("remove: %v %v", out, err)
	}
}

// TestAWriteLogsWhereAndWhat: todos.changed names the workspace and the task; a {root} has no
// workspace, so the detail carries it.
func TestAWriteLogsWhereAndWhat(t *testing.T) {
	m := todo.New(nil, nil, nil)
	for _, tc := range []struct {
		verb   string
		params []any
		result any
		want   module.Event
	}{
		{"todos.status", []any{"kb", "t1", "rev", "completed"}, nil, module.Event{Kind: "todos.changed", Workspace: "kb", Detail: "t1"}},
		{"todos.add", []any{"kb", map[string]any{"title": "x"}}, todo.Task{ID: "2026-10-11-x"}, module.Event{Kind: "todos.changed", Workspace: "kb", Detail: "2026-10-11-x"}},
		{"todos.status", []any{map[string]any{"root": "/p"}, "t1", "rev", "open"}, nil, module.Event{Kind: "todos.changed", Detail: "/p#t1"}},
		{"todos.refresh", []any{map[string]any{"root": "/p"}}, todo.RefreshSummary{}, module.Event{Kind: "todos.changed", Detail: "/p"}},
	} {
		got := verb(t, m, tc.verb).EventsOf(context.Background(), tc.params, tc.result)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s %v: %+v, want %+v", tc.verb, tc.params, got, tc.want)
		}
	}
}

// TestEveryVerbAnswers: each todos verb, called with the wire's params, reaches its Manager method
// and answers what that answers.
func TestEveryVerbAnswers(t *testing.T) {
	e := claimed(t)
	call := func(name string, params ...any) any {
		t.Helper()
		out, err := verb(t, e.m, name).Call(context.Background(), params)
		if err != nil {
			t.Fatalf("%s %v: %v", name, params, err)
		}
		return out
	}
	added := call("todos.add", "kb", map[string]any{"title": "Every verb"}).(todo.Task)
	if r := call("todos.resolve", "kb").(todo.Resolved); !r.Claimed {
		t.Errorf("resolve %+v", r)
	}
	call("todos.open", "kb", map[string]any{"kb_root": "/kb"})
	if s := call("todos.stores").([]todo.StoreInfo); len(s) == 0 {
		t.Error("no stores")
	}
	if vs := call("todos.vars", "kb").([]todo.Var); len(vs) == 0 {
		t.Error("no vars")
	}
	call("todos.set_var", "PROJECTS", "/home/u/projects")
	// an at inside the options map decodes as a verb's first param does: a name or a {root}
	call("todos.set_var", "PROJECT_ROOT", "/kb", map[string]any{"at": "kb"})
	call("todos.set_var", "LOGS", "/l", map[string]any{"at": map[string]any{"root": "/kb"}, "scope": "machine"})
	byName := map[string]todo.Var{}
	for _, v := range call("todos.vars", "kb").([]todo.Var) {
		byName[v.Name] = v
	}
	if byName["PROJECT_ROOT"].Source != "kb" || byName["LOGS"].Source != "machine" {
		t.Errorf("set_var's options: %+v %+v", byName["PROJECT_ROOT"], byName["LOGS"])
	}
	// a session from before protocol 24 reads a KB-file value's source as the store's file's was
	old, err := verb(t, e.m, "todos.vars").Call(module.WithProtocol(context.Background(), 23), []any{"kb"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range old.([]todo.Var) {
		if v.Name == "PROJECT_ROOT" && v.Source != "store" {
			t.Errorf("protocol 23's PROJECT_ROOT %+v", v)
		}
	}
	call("workspace.set_var", "kb", "DUMPS", "/data", map[string]any{"machine_only": true})
	call("workspace.set_var", "kb", "SPECS", "specs", map[string]any{"scope": "kb"})
	byName = map[string]todo.Var{}
	for _, v := range call("workspace.vars", "kb").([]todo.Var) {
		byName[v.Name] = v
	}
	if d, s := byName["DUMPS"], byName["SPECS"]; d.Value != "/data" || !d.Machine || s.Value != "/kb/specs" || s.Source != "kb" {
		t.Errorf("workspace.set_var: %+v %+v", d, s)
	}
	if o := call("workspace.of", "/kb/.todo-list/open/x.md").(todo.Owned); o.Workspace != "kb" || o.Root != "/kb" {
		t.Errorf("workspace.of %+v", o)
	}
	// "" is no workspace's: a file no workspace owns resolves with the global variables, not a KB's
	none := map[string]todo.Var{}
	for _, v := range call("workspace.vars", "").([]todo.Var) {
		none[v.Name] = v
	}
	if g, kbr := none["PROJECTS"], none["KB_ROOT"]; g.Value != "/home/u/projects" || g.Source != "global" || kbr.Value != "" {
		t.Errorf("workspace.vars(\"\"): PROJECTS %+v, KB_ROOT %+v; want the global, and no KB_ROOT", g, kbr)
	}
	if p := call("todos.migrate_refs", "kb", map[string]any{"plan": true}).(todo.MigrateResult); p.Rewritten != 0 {
		t.Errorf("a plan rewrote: %+v", p)
	}
	updated := call("todos.update", "kb", added.ID, added.Rev, map[string]any{"priority": "high"}).(todo.Task)
	assigned := call("todos.assign", "kb", added.ID, updated.Rev, "agent:jarvis", "yours", "auto-agents:p1").(todo.Task)
	statused := call("todos.status", "kb", added.ID, assigned.Rev, "completed").(todo.Task)
	if statused.Status != "completed" || statused.Assignee != "agent:jarvis" {
		t.Errorf("after the writes %+v", statused)
	}
	if s := call("todos.refresh", "kb").(todo.RefreshSummary); s.Scanned == 0 {
		t.Errorf("refresh %+v", s)
	}
	page := call("todos.changes", "kb", "auto-core", int64(10)).(todo.ChangesPage)
	if len(page.Changes) == 0 {
		t.Fatal("no changes for a new consumer")
	}
	// the assign's scope, as the verb was given it (protocol 30)
	if i := slices.IndexFunc(page.Changes, func(c todo.Change) bool { return c.Kind == todo.KindAssign }); i < 0 || page.Changes[i].Scope != "auto-agents:p1" {
		t.Errorf("the assign's change: %d %+v; want its scope", i, page.Changes)
	}
	call("todos.ack", "kb", "auto-core", page.Changes[len(page.Changes)-1].Seq)
	call("todos.remove", "kb", added.ID, statused.Rev)
	e.disk.WriteFile("/kb/.todo-list/open/bad.md", []byte("not a task\n"))
	call("todos.remove_malformed", "kb", "open/bad.md")
	root := "/proj"
	e.disk.MkdirAll(root)
	if out := call("todos.register", root).(map[string]bool); !out["new"] {
		t.Errorf("register %v", out)
	}
	call("todos.claim", map[string]any{"root": root}, map[string]any{})
	call("todos.scaffold", map[string]any{"root": root})
	call("todos.set_kb", map[string]any{"root": root}, "kb")
	if o := call("workspace.of", root+"/.todo-list/open/x.md").(todo.Owned); o.Workspace != "kb" {
		t.Errorf("set_kb did not link the store: %+v", o)
	}
	e.disk.MkdirAll("/elsewhere")
	call("todos.set_dir", map[string]any{"root": root}, "")
	call("todos.move_store", map[string]any{"root": root}, "/elsewhere/.todo-list")
	call("todos.release", map[string]any{"root": root})
}

// TestAReaderThatDoesntDeclareScopesGetsNoScopedAssignee: todos.changes without scoped answers a
// scoped assignment without its assignee, so a notifier that ignores scopes (one still running from
// before an upgrade) can't deliver it in every project; an unscoped assignment keeps its assignee,
// and a consumer that declares scoped gets every change as it is (protocol 33).
func TestAReaderThatDoesntDeclareScopesGetsNoScopedAssignee(t *testing.T) {
	e := claimed(t)
	call := func(name string, params ...any) any {
		t.Helper()
		out, err := verb(t, e.m, name).Call(context.Background(), params)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	a := call("todos.add", "kb", map[string]any{"title": "scoped"}).(todo.Task)
	b := call("todos.add", "kb", map[string]any{"title": "unscoped"}).(todo.Task)
	call("todos.assign", "kb", a.ID, a.Rev, "agent:jarvis", "", "auto-agents:p1")
	call("todos.assign", "kb", b.ID, b.Rev, "agent:zen")

	assigns := func(page todo.ChangesPage) map[string]todo.Change {
		out := map[string]todo.Change{}
		for _, c := range page.Changes {
			if c.Kind == todo.KindAssign {
				out[c.ID] = c
			}
		}
		return out
	}
	old := assigns(call("todos.changes", "kb", "old-notifier", int64(50)).(todo.ChangesPage))
	if c := old[a.ID]; c.To != "" || c.Scope != "auto-agents:p1" {
		t.Errorf("an undeclared reader got the scoped assign %+v: want no to, the scope kept", c)
	}
	if c := old[b.ID]; c.To != "agent:zen" {
		t.Errorf("an undeclared reader got the unscoped assign %+v: want its to", c)
	}
	scoped := assigns(call("todos.changes", "kb", "scoped-notifier", int64(50), true).(todo.ChangesPage))
	if c := scoped[a.ID]; c.To != "agent:jarvis" || c.Scope != "auto-agents:p1" {
		t.Errorf("a reader that declares scoped got %+v: want the change as it is", c)
	}
}

// TestForUnscopedReaderWithholdsOnlyScopedAssignees: a resync's rows lose a scoped assignee and keep
// an unscoped one, other changes are untouched, and the page it was made from is not changed.
func TestForUnscopedReaderWithholdsOnlyScopedAssignees(t *testing.T) {
	page := todo.ChangesPage{
		Changes: []todo.Change{
			{Seq: 1, Kind: todo.KindAssign, To: "agent:x", Scope: "auto-agents:p1"},
			{Seq: 2, Kind: todo.KindAssign, To: "agent:y"},
			{Seq: 3, Kind: todo.KindStatus, To: "completed", Scope: "auto-agents:p1"},
		},
		Snapshot: []todo.SnapshotRow{{ID: "a", Assignee: "agent:x", Scope: "auto-agents:p1"}, {ID: "b", Assignee: "agent:y"}},
	}
	got := page.ForUnscopedReader()
	if got.Changes[0].To != "" || got.Changes[1].To != "agent:y" || got.Changes[2].To != "completed" {
		t.Errorf("changes %+v: only the scoped assign loses its to", got.Changes)
	}
	if got.Snapshot[0].Assignee != "" || got.Snapshot[1].Assignee != "agent:y" {
		t.Errorf("snapshot %+v: only the scoped row loses its assignee", got.Snapshot)
	}
	if page.Changes[0].To != "agent:x" || page.Snapshot[0].Assignee != "agent:x" {
		t.Error("the original page was changed")
	}
}
