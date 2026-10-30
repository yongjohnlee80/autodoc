package todo_test

import (
	"errors"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// kbFile is the kb workspace's variable file, at its root
// (docs/decisions/todos.md#where-a-variables-value-comes-from).
const kbFile = "/kb/.autodoc-vars.toml"

// varOf is name's entry in at's todos.vars.
func varOf(t *testing.T, e *env, at todo.At, name string) todo.Var {
	t.Helper()
	vars, err := e.m.Vars(ctx, at)
	if err != nil {
		t.Fatalf("vars %v: %v", at, err)
	}
	return find(vars, name)
}

// wsVarOf is name's entry in workspace's workspace.vars.
func wsVarOf(t *testing.T, m *todo.Manager, workspace, name string) todo.Var {
	t.Helper()
	vars, err := m.WorkspaceVars(ctx, workspace)
	if err != nil {
		t.Fatalf("workspace vars %s: %v", workspace, err)
	}
	return find(vars, name)
}

func find(vars []todo.Var, name string) todo.Var {
	for _, v := range vars {
		if v.Name == name {
			return v
		}
	}
	return todo.Var{Name: name}
}

// otherMachine is a second daemon over the same disk (the KB and its files travel by git) with its own
// journal (a machine's rows never travel).
func otherMachine(t *testing.T, e *env) *todo.Manager {
	t.Helper()
	return todo.New(e.roots, e.disk, newJournal(t), todo.WithHome("/Users/u"))
}

// folderRoute is a project folder whose todos are the kb store's (todos.set_dir), with the
// project's own AGENTS.md: a real project's layout, where the route bug was.
func folderRoute(t *testing.T, e *env) todo.At {
	t.Helper()
	e.disk.WriteFile("/src/proj/AGENTS.md", []byte("# proj: an agent file, not a KB\n"))
	at := todo.At{Root: "/src/proj"}
	if _, err := e.m.SetDir(ctx, at, kbStore); err != nil {
		t.Fatal(err)
	}
	return at
}

// outside is a claimed store outside every workspace: /out/.todo-list, a registered project's.
func outside(t *testing.T, e *env) todo.At {
	t.Helper()
	e.disk.MkdirAll("/out")
	if _, err := e.m.Register(ctx, "/out"); err != nil {
		t.Fatal(err)
	}
	out := todo.At{Root: "/out"}
	e.must(e.m.Claim(ctx, out))
	if _, err := e.m.Scaffold(ctx, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every route to a store resolves its workspace's variables: the workspace name and a folder whose
// todos are the store's (docs/decisions/todos.md#one-answer-from-every-route-to-a-store,
// docs/decisions/todos.md#variables-belong-to-the-workspace). $KB_ROOT is the workspace root and
// $HOME the daemon user's; $WORKSPACE and $PROJECT_ROOT are no longer derived. A refresh on the folder
// route writes no error for $KB_ROOT.
func TestEveryRouteResolvesTheWorkspacesVars(t *testing.T) {
	e := claimed(t)
	folder := folderRoute(t, e)
	e.disk.WriteFile("/kb/adrs/a.md", []byte("x"))
	viaKB, err := e.m.Vars(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	viaFolder, err := e.m.Vars(ctx, folder)
	if err != nil || !reflect.DeepEqual(viaKB, viaFolder) {
		t.Fatalf("the routes disagree:\nkb     %+v\nfolder %+v (%v)", viaKB, viaFolder, err)
	}
	ws := wsVarOf(t, e.m, "kb", "KB_ROOT")
	if v := varOf(t, e, folder, "KB_ROOT"); v.Value != "/kb" || v.Source != "derived" || ws != v {
		t.Errorf("KB_ROOT: folder %+v, workspace.vars %+v", v, ws)
	}
	if v := varOf(t, e, kb, "HOME"); v.Value != "/home/u" || !v.Builtin {
		t.Errorf("HOME %+v", v)
	}
	for _, n := range []string{"WORKSPACE", "PROJECT_ROOT"} {
		if v := varOf(t, e, kb, n); v.Value != "" || v.Builtin {
			t.Errorf("%s is derived: %+v", n, v)
		}
	}
	task, err := e.m.Add(ctx, folder, todo.Spec{Title: "t", ADR: []string{"/kb/adrs/a.md"}}, "")
	if err != nil || !slices.Equal(task.ADR, []string{"$KB_ROOT/adrs/a.md"}) {
		t.Errorf("symbolized on the folder route: %v %v", task.ADR, err)
	}
	e.disk.WriteFile(kbStore+"/open/h.md", []byte(taskFile("h", "open", "adr:\n  - $KB_ROOT/adrs/a.md")))
	if _, err := e.m.Refresh(ctx, folder); err != nil {
		t.Fatal(err)
	}
	if h := e.get("h"); len(h.Errors) != 0 {
		t.Errorf("a refresh on the folder route wrote errors: %+v", h.Errors)
	}
}

// One precedence (docs/decisions/todos.md#where-a-variables-value-comes-from): this machine's row,
// then the KB's file (relative to the KB root), then the derived built-in, then a global var, then
// the environment. An absolute value in the KB's file is refused, reported once, and the name falls
// through.
func TestThePrecedence(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbFile, []byte("[vars]\nPROJECT_ROOT = \"../src/p\"\nKB_ROOT = \".\"\nABS = \"/abs\"\nBOTH = \"rel\"\n"))
	e.must(e.m.SetVar(ctx, "BOTH", "/machine", todo.SetVarOpts{Workspace: "kb"}))
	e.must(e.m.SetVar(ctx, "GLOBAL", "/g"))
	e.must(e.m.SetVar(ctx, "ABS", "/global-abs"))
	for _, c := range []struct{ name, value, source string }{
		{"PROJECT_ROOT", "/src/p", "kb"},
		{"KB_ROOT", "/kb", "kb"}, // a KB-file override of a built-in, "." being the root itself
		{"BOTH", "/machine", "machine"},
		{"GLOBAL", "/g", "global"},
		{"ABS", "/global-abs", "global"}, // the file's absolute value refused: the global one answers
		{"HOME", "/home/u", "derived"},
	} {
		if v := varOf(t, e, kb, c.name); v.Value != c.value || v.Source != c.source {
			t.Errorf("%s = %q from %q, want %q from %q", c.name, v.Value, v.Source, c.value, c.source)
		}
	}
	varOf(t, e, kb, "ABS")
	if n := strings.Count(strings.Join(e.eventKinds(), ","), "todos.vars_refused"); n != 1 {
		t.Errorf("the refused absolute value reported %d times, want once: %v", n, e.eventKinds())
	}
}

// A KB file the format refuses is ignored whole, reported once, and its mending reported too; a file
// over the limit is refused, never read as its first part.
func TestARefusedKBFile(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbFile, []byte("[vars\n"))
	varOf(t, e, kb, "X")
	varOf(t, e, kb, "X")
	kinds := e.eventKinds()
	if c := strings.Count(strings.Join(kinds, ","), "todos.vars_invalid"); c != 1 {
		t.Errorf("a refused file reported %d times: %v", c, kinds)
	}
	e.disk.WriteFile(kbFile, []byte("[vars]\nX = \"x\"\n"))
	if v := varOf(t, e, kb, "X"); v.Value != "/kb/x" || !slices.Contains(e.eventKinds(), "todos.vars_valid") {
		t.Errorf("the mended file: %+v %v", v, e.eventKinds())
	}
	pad := "# " + strings.Repeat("x", 64<<10) + "\n"
	e.disk.WriteFile(kbFile, []byte(pad+"[vars]\nP = \"p\"\n"))
	if v := varOf(t, e, kb, "P"); v.Value != "" {
		t.Errorf("an oversized file was read, not refused: %+v", v)
	}
}

// The KB's file holds a top-level machine list and a [vars] table of variable names with values, and
// nothing else: each other shape is refused whole, its names read as unset, and reported.
func TestTheVariableFileFormat(t *testing.T) {
	e := claimed(t)
	reported := func() int { return strings.Count(strings.Join(e.eventKinds(), ","), "todos.vars_invalid") }
	for i, bad := range []string{
		"other = 1\n[vars]\nX = \"x\"\n",            // an unknown key
		"machine = [\"1BAD\"]\n[vars]\nX = \"x\"\n", // a machine name that is no variable name
		"[vars]\n\"a-b\" = \"y\"\nX = \"x\"\n",      // a variable name that is none
		"machine = [\"X\"]\n[vars]\nX = \"x\"\n",    // machine-only and given a value here
		"[vars]\nX = \"\"\n",                        // an empty value
	} {
		e.disk.WriteFile(kbFile, []byte(bad))
		if v := varOf(t, e, kb, "X"); v.Value != "" || v.Source == "kb" {
			t.Errorf("file %d was read: %+v", i, v)
		}
		if n := reported(); n != i+1 {
			t.Errorf("file %d: %d reports, want %d", i, n, i+1)
		}
	}
}

// EffectiveVars are a workspace's values as the index reads them, each name with a value, the
// environment's included; AllEffectiveVars hold every workspace's, and "" those of no workspace.
func TestEffectiveVars(t *testing.T) {
	e := claimed(t)
	e.m = todo.New(e.roots, e.disk, e.j, todo.WithHome("/home/u"),
		todo.WithEnv(func(n string) string { return map[string]string{"FROMENV": "/env"}[n] }))
	e.disk.WriteFile(kbFile, []byte("machine = [\"FROMENV\", \"UNSET\"]\n\n[vars]\nPROJECT_ROOT = \"../src\"\n"))
	e.must(e.m.SetVar(ctx, "G", "/g"))
	want := map[string]string{"KB_ROOT": "/kb", "HOME": "/home/u", "PROJECT_ROOT": "/src", "G": "/g", "FROMENV": "/env"}
	got, err := e.m.EffectiveVars(ctx, "/kb")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveVars %v %v\nwant %v", got, err, want)
	}
	if v := varOf(t, e, kb, "FROMENV"); v.Source != "env" || !v.Machine {
		t.Errorf("a machine-only name this machine has in its environment %+v", v)
	}
	all, err := e.m.AllEffectiveVars(ctx)
	if err != nil || !reflect.DeepEqual(all["/kb"], want) ||
		!reflect.DeepEqual(all[""], map[string]string{"HOME": "/home/u", "G": "/g"}) || len(all) != 2 {
		t.Errorf("AllEffectiveVars %v %v", all, err)
	}
}

// workspace.vars("") is no workspace's: $HOME and the global variables, with no $KB_ROOT, the same a
// path in no workspace resolves with in the index (AllEffectiveVars' ""). A client resolving a file no
// workspace owns reads them there (docs/decisions/todos.md#resolving-refs-in-every-document); a
// name no workspace's is unknown.
func TestTheVarsOfNoWorkspace(t *testing.T) {
	e := claimed(t)
	e.m = todo.New(e.roots, e.disk, e.j, todo.WithHome("/home/u"))
	e.must(e.m.SetVar(ctx, "G", "/g"))
	vars, err := e.m.WorkspaceVars(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		if v.Value != "" {
			got[v.Name] = v.Value
		}
	}
	all, err := e.m.AllEffectiveVars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"HOME": "/home/u", "G": "/g"}) || !reflect.DeepEqual(got, all[""]) {
		t.Errorf("workspace.vars(\"\") %v, the index's %v; want HOME and G alone, alike", got, all[""])
	}
	if v := find(vars, "G"); v.Source != "global" {
		t.Errorf("G's source %q, want global", v.Source)
	}
	if _, err := e.m.WorkspaceVars(ctx, "nope"); !errors.Is(err, todo.ErrNoWorkspace) {
		t.Errorf("an unknown workspace: %v, want ErrNoWorkspace", err)
	}
}

// SetVar refuses what names no place to keep a value: an unknown scope, machine_only off the machine
// scope, and a KB or machine value with neither a workspace nor an at.
func TestSetVarRefusals(t *testing.T) {
	e := claimed(t)
	for name, o := range map[string]todo.SetVarOpts{
		"an unknown scope":          {Workspace: "kb", Scope: "bogus"},
		"machine_only in the file":  {Workspace: "kb", Scope: todo.ScopeKB, MachineOnly: true},
		"a kb value with no place":  {Scope: todo.ScopeKB},
		"a machine value, no place": {Scope: todo.ScopeMachine},
	} {
		if err := e.m.SetVar(ctx, "X", "/x", o); !is(err, todo.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.m.SetVar(ctx, "1BAD", "/x"); !is(err, todo.ErrInvalid) {
		t.Errorf("a bad name: %v", err)
	}
	if e.exists(kbFile) {
		t.Errorf("a refused set wrote the KB file:\n%s", e.read(kbFile))
	}
}

// A machine-only name with no value here is never an errors[] entry: it is a fact about this machine.
// Any other name no layer defines is, the same on every machine.
func TestMachineOnlyNamesAreNeverPersistedAsErrors(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbFile, []byte("machine = [\"DUMPS\"]\n"))
	e.disk.WriteFile(kbStore+"/open/m.md", []byte(taskFile("m", "open", "adr:\n  - $DUMPS/a.md\n  - $NOPE/b.md")))
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	m := e.get("m")
	if len(m.Errors) != 1 || m.Errors[0].Field != "adr[1]" || m.Errors[0].Code != todo.CodeUnresolved {
		t.Errorf("errors %+v, want only $NOPE's", m.Errors)
	}
	if v := varOf(t, e, kb, "DUMPS"); !v.Machine || v.Value != "" {
		t.Errorf("DUMPS %+v", v)
	}
}

// set_var's scopes: the KB's file (a value stored relative to the KB root, the file rendered stable
// and removed when empty), this machine's row (absolute; machine_only also declares the name in the
// file), global (never a built-in). todos.set_var with an at writes its store's workspace; one whose
// store belongs to no workspace is refused.
func TestSetVarScopes(t *testing.T) {
	e := claimed(t)
	e.must(e.m.SetVar(ctx, "DDEX", "/kb/docs/ddex", todo.SetVarOpts{At: &kb})) // scope store: the KB's file
	e.must(e.m.SetVar(ctx, "PROJECT_ROOT", "../src/p", todo.SetVarOpts{Workspace: "kb", Scope: todo.ScopeKB}))
	if got := e.read(kbFile); !strings.Contains(got, "DDEX = \"docs/ddex\"") || !strings.Contains(got, "PROJECT_ROOT = \"../src/p\"") {
		t.Errorf("the KB file:\n%s", got)
	}
	if err := e.m.SetVar(ctx, "HOMEY", "~/x", todo.SetVarOpts{Workspace: "kb", Scope: todo.ScopeKB}); !is(err, todo.ErrInvalid) {
		t.Errorf("a ~ value in the KB's file, which another machine's home would not match: %v", err)
	}
	e.must(e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{Workspace: "kb", MachineOnly: true}))
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/data" || v.Source != "machine" || !v.Machine {
		t.Errorf("DUMPS %+v", v)
	}
	if err := e.m.SetVar(ctx, "DUMPS", "rel", todo.SetVarOpts{Workspace: "kb", Scope: todo.ScopeKB}); !is(err, todo.ErrInvalid) {
		t.Errorf("a KB value for a machine-only name: %v", err)
	}
	if err := e.m.SetVar(ctx, "KB_ROOT", "/x"); !is(err, todo.ErrInvalid) {
		t.Errorf("a global built-in: %v", err)
	}
	if err := e.m.SetVar(ctx, "X", "y", todo.SetVarOpts{Workspace: "nope"}); !is(err, todo.ErrNoWorkspace) {
		t.Errorf("an unknown workspace: %v", err)
	}
	if err := e.m.SetVar(ctx, "X", "y", todo.SetVarOpts{Workspace: "kb", At: &kb}); !is(err, todo.ErrInvalid) {
		t.Errorf("a workspace and an at: %v", err)
	}
	for _, n := range []string{"DDEX", "PROJECT_ROOT"} {
		e.must(e.m.SetVar(ctx, n, "", todo.SetVarOpts{Workspace: "kb", Scope: todo.ScopeKB}))
	}
	if got := e.read(kbFile); strings.Contains(got, "[vars]") || !strings.Contains(got, `machine = ["DUMPS"]`) {
		t.Errorf("the file after removing its values:\n%s", got)
	}
	// a store outside every workspace is refused until linked
	out := outside(t, e)
	if err := e.m.SetVar(ctx, "X", "y", todo.SetVarOpts{At: &out}); !is(err, todo.ErrInvalid) {
		t.Errorf("a store in no workspace: %v", err)
	}
}

// A store belongs to the deepest workspace containing it, recorded once: a workspace nested inside it
// later never takes it. A store outside every workspace has no $KB_ROOT until todos.set_kb links it,
// and set_kb moves a store. workspace.of answers the owner todos.vars uses.
func TestAStoresWorkspace(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile("/kb/notes/n.md", []byte("x"))
	if ws, root, err := e.m.Owner(ctx, kbStore+"/open/x.md"); err != nil || ws != "kb" || root != "/kb" {
		t.Errorf("a task's owner: %q %q %v", ws, root, err)
	}
	e.roots["inner"] = "/kb" + "/.todo-list" // a workspace nested inside, added after the store's owner was recorded
	if _, root, _ := e.m.Owner(ctx, kbStore+"/open/x.md"); root != "/kb" {
		t.Errorf("a nested workspace took the store: %q", root)
	}
	if ws, _, _ := e.m.Owner(ctx, "/kb/notes/n.md"); ws != "kb" {
		t.Errorf("a note's owner: %q", ws)
	}
	delete(e.roots, "inner")
	// a store recorded before docs/decisions/todos.md#variables-belong-to-the-workspace has no
	// workspace in its record: its first use records it
	e.must(e.j.UpdateSettings(ctx, func(s *todo.Settings) error {
		for id, r := range s.Stores {
			r.KB = ""
			s.Stores[id] = r
		}
		return nil
	}))
	if ws, _, _ := e.m.Owner(ctx, kbStore+"/open/x.md"); ws != "kb" {
		t.Errorf("an old record's owner: %q", ws)
	}
	if st, err := e.j.Settings(ctx); err != nil || st.Stores[e.storeID()].KB != "/kb" {
		t.Errorf("the old record's workspace was not recorded: %+v %v", st.Stores[e.storeID()], err)
	}
	if ws, root, _ := e.m.Owner(ctx, "/elsewhere/f.md"); ws != "" || root != "" {
		t.Errorf("a file in no workspace: %q %q", ws, root)
	}
	// an external store: no KB_ROOT, then linked, then moved
	out := outside(t, e)
	if v := varOf(t, e, out, "KB_ROOT"); v.Value != "" {
		t.Errorf("an unlinked store's KB_ROOT %+v", v)
	}
	e.must(e.m.SetKB(ctx, out, "kb"))
	if v := varOf(t, e, out, "KB_ROOT"); v.Value != "/kb" {
		t.Errorf("a linked store's KB_ROOT %+v", v)
	}
	if ws, _, _ := e.m.Owner(ctx, "/out/.todo-list/open/x.md"); ws != "kb" {
		t.Errorf("workspace.of disagrees with todos.vars: %q", ws)
	}
	e.roots["other"] = "/other"
	e.disk.MkdirAll("/other")
	e.must(e.m.SetKB(ctx, out, "other"))
	if v := varOf(t, e, out, "KB_ROOT"); v.Value != "/other" {
		t.Errorf("a moved store's KB_ROOT %+v", v)
	}
	if err := e.m.SetKB(ctx, out, "nope"); !is(err, todo.ErrNoWorkspace) {
		t.Errorf("set_kb to an unknown workspace: %v", err)
	}
}

// A store recorded to a workspace that is then removed belongs to none, or to the deepest workspace
// still containing it, alike for todos.vars, workspace.of, todos.stores and the index's variables;
// the record stays, so adding the workspace back restores the link, a todos.set_kb one to an outside
// store included.
func TestAStoreOfARemovedWorkspace(t *testing.T) {
	e := claimed(t)
	out := outside(t, e)
	e.must(e.m.SetKB(ctx, out, "kb"))
	e.disk.MkdirAll("/w/in")
	e.roots["outer"], e.roots["inner"] = "/w", "/w/in"
	nested := todo.At{Workspace: "inner"}
	e.must(e.m.Claim(ctx, nested))
	if _, err := e.m.Scaffold(ctx, nested); err != nil {
		t.Fatal(err)
	}
	owners := func() map[string]string { // a store's path: its workspace, as todos.stores answers it
		t.Helper()
		list, err := e.m.Stores(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, s := range list {
			got[s.Path] = s.KB
		}
		return got
	}
	agree := func(what string, at todo.At, file, root string) {
		t.Helper()
		if v := varOf(t, e, at, "KB_ROOT"); v.Value != root {
			t.Errorf("%s: todos.vars KB_ROOT %q, want %q", what, v.Value, root)
		}
		if _, r, err := e.m.Owner(ctx, file); err != nil || r != root {
			t.Errorf("%s: workspace.of root %q (%v), want %q", what, r, err, root)
		}
		if kb := owners()[path.Dir(path.Dir(file))]; kb != root {
			t.Errorf("%s: todos.stores kb %q, want %q", what, kb, root)
		}
		all, err := e.m.AllEffectiveVars(ctx)
		if err != nil || all[root]["KB_ROOT"] != root && root != "" {
			t.Errorf("%s: the index has no variables for %q: %v %v", what, root, all, err)
		}
	}
	agree("linked", out, "/out/.todo-list/open/x.md", "/kb")
	agree("nested", nested, "/w/in/.todo-list/open/x.md", "/w/in")

	delete(e.roots, "kb")
	delete(e.roots, "inner")
	agree("its workspace removed", out, "/out/.todo-list/open/x.md", "")
	agree("its nested workspace removed: the outer one holds it", todo.At{Root: "/w/in"}, "/w/in/.todo-list/open/x.md", "/w")
	all, err := e.m.AllEffectiveVars(ctx)
	if _, stale := all["/kb"]; err != nil || stale {
		t.Errorf("the index keeps the removed workspace's variables: %v %v", all, err)
	}
	st, err := e.j.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range st.Stores {
		if rec.Path == "/out/.todo-list" && rec.KB != "/kb" || rec.Path == "/w/in/.todo-list" && rec.KB != "/w/in" {
			t.Errorf("a read rewrote the record: %+v", rec)
		}
	}

	e.roots["kb"], e.roots["inner"] = "/kb", "/w/in"
	agree("its workspace back", out, "/out/.todo-list/open/x.md", "/kb")
	agree("its nested workspace back", nested, "/w/in/.todo-list/open/x.md", "/w/in")
}

// The migration (docs/decisions/todos.md#migrating-store-variable-files): a store's old file's
// relative values are rebased into the KB's file, its absolute values become this machine's rows
// and machine-only names, its old machine values become rows, and the store's file is renamed only
// after; a second machine then resolves the portable names and reports the machine-only one.
func TestTheMigrationFoldsAStoresFile(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("machine = [\"LOGS\"]\n\n[vars]\nPROJECT_ROOT = \"../../src/p\"\nDUMPS = \"/data\"\n"))
	e.must(e.j.UpdateSettings(ctx, func(s *todo.Settings) error {
		for id := range s.Stores {
			s.MachineVars = map[string]map[string]string{id: {"LOGS": "/logs"}}
		}
		return nil
	}))
	e.restart() // what an upgraded daemon finds
	if v := varOf(t, e, kb, "PROJECT_ROOT"); v.Value != "/src/p" || v.Source != "kb" {
		t.Errorf("PROJECT_ROOT %+v", v)
	}
	got := e.read(kbFile)
	if !strings.Contains(got, `PROJECT_ROOT = "../src/p"`) || !strings.Contains(got, `"DUMPS"`) || !strings.Contains(got, `"LOGS"`) {
		t.Errorf("the KB file:\n%s", got)
	}
	if e.exists(kbStore+"/.autodoc-vars.toml") || !e.exists(kbStore+"/.autodoc-vars.toml.migrated") {
		t.Error("the store's file was not renamed")
	}
	for n, want := range map[string]string{"DUMPS": "/data", "LOGS": "/logs"} {
		if v := varOf(t, e, kb, n); v.Value != want || v.Source != "machine" || !v.Machine {
			t.Errorf("%s %+v", n, v)
		}
	}
	st, err := e.j.Settings(ctx)
	if err != nil || len(st.MachineVars) != 0 {
		t.Errorf("the store's old machine values stayed: %+v %v", st.MachineVars, err)
	}
	other := otherMachine(t, e)
	if v := wsVarOf(t, other, "kb", "PROJECT_ROOT"); v.Value != "/src/p" {
		t.Errorf("another machine's PROJECT_ROOT %+v", v)
	}
	if v := wsVarOf(t, other, "kb", "DUMPS"); v.Value != "" || !v.Machine {
		t.Errorf("another machine's DUMPS %+v: a machine-only name to set there", v)
	}
}

// Two stores of one workspace setting a name differently is a conflict: nothing is carried, both
// files stay, and the name is unresolved below this machine's row. A machine row overrides it here but
// never settles it; a KB-file value settles it, and so does declaring it machine-only; then the files
// are renamed and a second machine resolves the name, or reports it machine-only.
func TestAMigrationConflictWaitsForAPerson(t *testing.T) {
	for _, settle := range []string{"kb", "machine-only"} {
		t.Run(settle, func(t *testing.T) {
			e := claimed(t)
			folderStore := "/src/proj/todos"
			e.disk.MkdirAll(folderStore)
			at := todo.At{Root: "/src/proj"}
			if _, err := e.m.SetDir(ctx, at, folderStore); err != nil {
				t.Fatal(err)
			}
			e.must(e.m.Claim(ctx, at))
			if _, err := e.m.Scaffold(ctx, at); err != nil {
				t.Fatal(err)
			}
			e.must(e.m.SetKB(ctx, at, "kb"))
			e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("[vars]\nP = \"../../a\"\nQ = \"../q\"\n"))
			e.disk.WriteFile(folderStore+"/.autodoc-vars.toml", []byte("[vars]\nP = \"../../../b\"\n"))
			e.restart()
			if v := varOf(t, e, kb, "P"); v.Value != "" || !v.Conflict || v.Source != "conflict" {
				t.Errorf("a conflicted name answered %+v", v)
			}
			if !slices.Contains(e.eventKinds(), "todos.vars_conflict") || e.exists(kbFile) {
				t.Errorf("events %v; the KB file written though in conflict: %v", e.eventKinds(), e.exists(kbFile))
			}
			e.must(e.m.SetVar(ctx, "P", "/here", todo.SetVarOpts{Workspace: "kb"}))
			if v := varOf(t, e, kb, "P"); v.Value != "/here" || v.Source != "machine" {
				t.Errorf("a machine row over a conflict %+v", v)
			}
			if !e.exists(kbStore+"/.autodoc-vars.toml") || !e.exists(folderStore+"/.autodoc-vars.toml") {
				t.Fatal("a machine row settled the conflict: the stores' files were renamed")
			}
			if v := wsVarOf(t, otherMachine(t, e), "kb", "P"); v.Value != "" {
				t.Errorf("another machine resolves the unsettled name %+v", v)
			}
			switch settle {
			case "kb":
				e.must(e.m.SetVar(ctx, "P", "/src/a", todo.SetVarOpts{Workspace: "kb", Scope: todo.ScopeKB}))
			default:
				e.must(e.m.SetVar(ctx, "P", "/here", todo.SetVarOpts{Workspace: "kb", MachineOnly: true}))
			}
			if v := varOf(t, e, kb, "P"); v.Conflict {
				t.Errorf("still in conflict %+v", v)
			}
			if e.exists(kbStore+"/.autodoc-vars.toml") || e.exists(folderStore+"/.autodoc-vars.toml") {
				t.Error("settled, the stores' files were not renamed")
			}
			if v := varOf(t, e, kb, "Q"); v.Value != "/kb/q" {
				t.Errorf("the unconflicted name was not carried: %+v", v)
			}
			o := wsVarOf(t, otherMachine(t, e), "kb", "P")
			switch settle {
			case "kb":
				if o.Value != "/src/a" {
					t.Errorf("another machine's settled P %+v", o)
				}
			default:
				if o.Value != "" || !o.Machine || o.Conflict {
					t.Errorf("another machine's machine-only P %+v", o)
				}
			}
		})
	}
}

// conflicted is an env whose kb workspace holds P in conflict, two stores having set it differently,
// over a manager whose environment has P too.
func conflicted(t *testing.T, envP string) *env {
	t.Helper()
	e := claimed(t)
	folderStore := "/src/proj/todos"
	e.disk.MkdirAll(folderStore)
	at := todo.At{Root: "/src/proj"}
	if _, err := e.m.SetDir(ctx, at, folderStore); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.Claim(ctx, at))
	if _, err := e.m.Scaffold(ctx, at); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.SetKB(ctx, at, "kb"))
	e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("[vars]\nP = \"../../a\"\n"))
	e.disk.WriteFile(folderStore+"/.autodoc-vars.toml", []byte("[vars]\nP = \"../../../b\"\n"))
	e.m = todo.New(e.roots, e.disk, e.j, todo.WithHome("/home/u"),
		todo.WithEnv(func(n string) string { return map[string]string{"P": envP}[n] }))
	return e
}

// A name held in conflict is unresolved in a task's refs as in todos.vars and the index: the
// environment never answers for it, and the error says what settles it. With no conflict, the
// environment answers, so the refusal is the conflict's.
func TestAConflictedNameNeverFallsToTheEnvironment(t *testing.T) {
	e := conflicted(t, "/environment")
	e.disk.WriteFile("/environment/a.md", []byte("x"))
	if v := varOf(t, e, kb, "P"); !v.Conflict || v.Value != "" {
		t.Fatalf("P is not in conflict: %+v", v)
	}
	e.disk.WriteFile(kbStore+"/open/c.md", []byte(taskFile("c", "open", "adr:\n  - $P/a.md")))
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	c := e.get("c")
	if len(c.Errors) != 1 || c.Errors[0].Code != todo.CodeUnresolved || !strings.Contains(c.Errors[0].Message, "conflict") {
		t.Errorf("a conflicted $P resolved through the environment: %+v", c.Errors)
	}

	// the control: no conflict, the environment answers
	o := claimed(t)
	o.m = todo.New(o.roots, o.disk, o.j, todo.WithHome("/home/u"),
		todo.WithEnv(func(n string) string { return map[string]string{"P": "/environment"}[n] }))
	o.disk.WriteFile("/environment/a.md", []byte("x"))
	o.disk.WriteFile(kbStore+"/open/c.md", []byte(taskFile("c", "open", "adr:\n  - $P/a.md")))
	if _, err := o.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	if c := o.get("c"); len(c.Errors) != 0 {
		t.Errorf("the environment's $P did not answer with no conflict: %+v", c.Errors)
	}
}

// At start, every store's workspace is recorded: the containing one, or, for a store outside every
// workspace, the workspace an older route's kb_root named; the routes' kb_roots are cleared.
func TestMigrateVarsRecordsOwnersAndClearsRouteKBRoots(t *testing.T) {
	e := claimed(t)
	out := outside(t, e)
	e.must(e.j.UpdateSettings(ctx, func(s *todo.Settings) error {
		if s.Roots == nil {
			s.Roots = map[string]todo.RootRecord{}
		}
		r := s.Roots["/out"]
		r.Root, r.KBRoot = "/out", "/kb"
		s.Roots["/out"] = r
		return nil
	}))
	e.m.MigrateVars(ctx)
	if v := varOf(t, e, out, "KB_ROOT"); v.Value != "/kb" {
		t.Errorf("the route's kb_root did not link the store: %+v", v)
	}
	st, err := e.j.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for root, r := range st.Roots {
		if r.KBRoot != "" {
			t.Errorf("%s kept its kb_root %q", root, r.KBRoot)
		}
	}
}

// A claim from auto-core records the project it names as the KB's PROJECT_ROOT, relative to the KB
// root, when the store is inside that project, so the cutover writes $PROJECT_ROOT/….
func TestAClaimRecordsTheProjectItsStoreIsIn(t *testing.T) {
	e := newEnv(t)
	e.roots["lm"] = "/lm/.auto-agents/kb"
	lm := todo.At{Workspace: "lm"}
	e.disk.WriteFile("/lm/.auto-agents/kb/AGENTS.md", []byte(kbAgents))
	e.disk.WriteFile("/lm/docs/d.md", []byte("x"))
	e.disk.WriteFile("/lm/.auto-agents/kb/.todo-list/open/l.md", []byte(taskFile("l", "open", "adr:\n  - $WORKSPACE/docs/d.md")))
	e.must(e.m.Claim(ctx, lm, todo.ClaimOpts{LegacyWorkspace: "/lm"}))
	if v := varOf(t, e, lm, "PROJECT_ROOT"); v.Value != "/lm" || v.Source != "kb" ||
		!strings.Contains(e.read("/lm/.auto-agents/kb/.autodoc-vars.toml"), `PROJECT_ROOT = "../.."`) {
		t.Errorf("PROJECT_ROOT %+v", v)
	}
	l, err := e.m.Get(ctx, lm, "l")
	if err != nil || !slices.Equal(l.ADR, []string{"$PROJECT_ROOT/docs/d.md"}) {
		t.Errorf("the cutover's ref %v %v", l.ADR, err)
	}
	// a claim from a session started in the KB names the KB, not the project it serves
	k := claimedFrom(t, "/kb")
	if v := varOf(t, k, kb, "PROJECT_ROOT"); v.Value != "" || k.exists(kbFile) {
		t.Errorf("a claim from the KB recorded PROJECT_ROOT %+v\n%s", v, k.read(kbFile))
	}
	k = claimedFrom(t, "/kb/notes")
	if v := varOf(t, k, kb, "PROJECT_ROOT"); v.Value != "" {
		t.Errorf("a claim from inside the KB recorded PROJECT_ROOT %+v", v)
	}
}

// claimedFrom is an env whose kb store, holding a task, was claimed by a session whose project root
// was legacy: a store with no directory yet leaves its claim's work to Scaffold.
func claimedFrom(t *testing.T, legacy string) *env {
	t.Helper()
	e := newEnv(t)
	e.disk.MkdirAll(legacy)
	e.disk.WriteFile(kbStore+"/open/k.md", []byte(taskFile("k", "open")))
	e.must(e.m.Claim(ctx, kb, todo.ClaimOpts{LegacyWorkspace: legacy}))
	return e
}

// A machine-only declaration whose KB file cannot take it keeps no row either: no machine may hold a
// value its peers would read as an undefined name.
func TestAFailedMachineDeclarationKeepsNoValue(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbFile, []byte("[vars\n"))
	if err := e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{Workspace: "kb", MachineOnly: true}); err == nil {
		t.Fatal("a machine value set though its declaration could not be written")
	}
	st, err := e.j.Settings(ctx)
	if err != nil || len(st.WorkspaceVars) != 0 {
		t.Errorf("a failed declaration kept the value: %+v %v", st.WorkspaceVars, err)
	}
}

// Making a portable variable machine-only rewrites the KB's file first; if this machine's row then
// cannot be kept, the file is put back, so the portable value is never lost.
func TestAFailedMachineValueRestoresTheFile(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbFile, []byte("[vars]\nDUMPS = \"old\"\n"))
	e.j.Fail = func(op string) error {
		if op == "UpdateSettings" {
			// a hand edit lands between the declaration and the settings write: it stays
			if got := e.read(kbFile); !strings.Contains(got, `machine = ["DUMPS"]`) {
				t.Errorf("not declared before the settings write:\n%s", got)
			}
			e.disk.WriteFile(kbFile, []byte("machine = [\"DUMPS\"]\n\n[vars]\nEXTRA = \"new\"\n"))
			return errors.New("the settings write failed")
		}
		return nil
	}
	err := e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{Workspace: "kb", MachineOnly: true})
	e.j.Fail = nil
	if err == nil {
		t.Fatal("a failed settings write reported success")
	}
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/kb/old" || v.Source != "kb" || v.Machine {
		t.Errorf("the portable value was lost: %+v\n%s", v, e.read(kbFile))
	}
	if v := varOf(t, e, kb, "EXTRA"); v.Value != "/kb/new" {
		t.Errorf("undoing the declaration erased an edit made in between: %+v\n%s", v, e.read(kbFile))
	}
}

// A failed machine value's undo never lands between another set_var's two writes: the second call
// waits for the first to finish (its undo included), then declares and keeps its own value.
func TestMachineSetVarsDoNotInterleave(t *testing.T) {
	e := claimed(t)
	later := make(chan error, 1)
	failed := false
	e.j.Fail = func(op string) error {
		if op != "UpdateSettings" || failed {
			return nil
		}
		failed = true // the first call's settings write fails, while a second call starts
		go func() {
			later <- e.m.SetVar(ctx, "DUMPS", "/later", todo.SetVarOpts{Workspace: "kb", MachineOnly: true})
		}()
		select {
		case err := <-later: // it finished in between: the undo that follows would land after it
			later <- err
		case <-time.After(300 * time.Millisecond): // it waits for this call, as it must
		}
		return errors.New("the settings write failed")
	}
	if err := e.m.SetVar(ctx, "DUMPS", "/first", todo.SetVarOpts{Workspace: "kb", MachineOnly: true}); err == nil {
		t.Fatal("the failed first call reported success")
	}
	if err := <-later; err != nil {
		t.Fatalf("the second call: %v", err)
	}
	e.j.Fail = nil
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/later" || v.Source != "machine" || !v.Machine {
		t.Errorf("the second call's value lost its declaration: %+v\n%s", v, e.read(kbFile))
	}
}

// todos.migrate_refs: a plan lists every $WORKSPACE ref and where it exists, proposing only a ref
// found under the project alone; an apply rewrites exactly the listed refs still as planned, each
// task one journaled rewrite, and skips the rest.
func TestMigrateRefsPlansThenAppliesTheReviewedList(t *testing.T) {
	e := claimed(t)
	if _, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true}); !is(err, todo.ErrInvalid) {
		t.Errorf("a plan without PROJECT_ROOT: %v", err)
	}
	e.must(e.m.SetVar(ctx, "PROJECT_ROOT", "/src/proj", todo.SetVarOpts{At: &kb}))
	for _, p := range []string{"/src/proj/only-p.md", "/kb/only-w.md", "/src/proj/both.md", "/kb/both.md"} {
		e.disk.WriteFile(p, []byte("x"))
	}
	refs := []string{"$WORKSPACE/only-p.md", "$WORKSPACE/only-w.md", "$WORKSPACE/both.md", "$WORKSPACE/none.md", "$KB_ROOT/only-w.md"}
	e.disk.WriteFile(kbStore+"/open/r.md", []byte(taskFile("r", "open", "adr:\n  - "+strings.Join(refs, "\n  - "))))
	plan, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Plan: true})
	if err != nil || len(plan.Candidates) != 4 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	var suggested []todo.RefCandidate
	for _, c := range plan.Candidates {
		if c.Suggested {
			suggested = append(suggested, c)
		}
	}
	if len(suggested) != 1 || suggested[0].Ref != refs[0] || suggested[0].To != "$PROJECT_ROOT/only-p.md" {
		t.Errorf("suggested %+v", suggested)
	}
	stale := plan.Candidates[2]
	stale.Ref = "$WORKSPACE/changed.md"
	res, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{Apply: []todo.RefCandidate{suggested[0], stale}})
	if err != nil || res.Rewritten != 1 || len(res.Skipped) != 1 || res.Skipped[0].Ref != stale.Ref {
		t.Fatalf("apply %+v %v", res, err)
	}
	want := append([]string{"$PROJECT_ROOT/only-p.md"}, refs[1:]...)
	if r := e.get("r"); !slices.Equal(r.ADR, want) {
		t.Errorf("adr %v\nwant %v", r.ADR, want)
	}
	n := 0
	for _, c := range e.rows() {
		if strings.HasPrefix(c.Reason, "migrate_refs:") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d migrate_refs rows, want 1", n)
	}
	if _, err := e.m.MigrateRefs(ctx, kb, todo.MigrateOpts{}); !is(err, todo.ErrInvalid) {
		t.Errorf("neither plan nor apply: %v", err)
	}
}
