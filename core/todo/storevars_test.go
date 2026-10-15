package todo_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// varOf is name's entry in at's todos.vars.
func varOf(t *testing.T, e *env, at todo.At, name string) todo.Var {
	t.Helper()
	vars, err := e.m.Vars(ctx, at)
	if err != nil {
		t.Fatalf("vars %v: %v", at, err)
	}
	for _, v := range vars {
		if v.Name == name {
			return v
		}
	}
	return todo.Var{Name: name}
}

// folderRoute is a project folder whose todos are the kb store's (todos.set_dir), with the
// project's own AGENTS.md, which is no KB marker: LabelManager's layout, where the bug was.
func folderRoute(t *testing.T, e *env) todo.At {
	t.Helper()
	e.disk.WriteFile("/src/proj/AGENTS.md", []byte("# proj: an agent file, not a KB\n"))
	at := todo.At{Root: "/src/proj"}
	if _, err := e.m.SetDir(ctx, at, kbStore); err != nil {
		t.Fatal(err)
	}
	return at
}

// Every route to a store resolves the same variables and writes the same refs: the workspace name
// and a folder whose todos are the store's (ADR 1792015069 §2.1). A refresh on the folder route
// writes no error for $KB_ROOT, which the route alone could not resolve.
func TestEveryRouteResolvesTheStoresVars(t *testing.T) {
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
	if v := varOf(t, e, folder, "KB_ROOT"); v.Value != "/kb" {
		t.Errorf("the folder route's KB_ROOT: %+v", v)
	}
	task, err := e.m.Add(ctx, folder, todo.Spec{Title: "t", ADR: []string{"/kb/adrs/a.md"}})
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

// $KB_ROOT is the nearest directory at or above the store's home whose AGENTS.md declares type: kb
// in its frontmatter. A project's own AGENTS.md makes nothing a KB.
func TestKBRootComesOnlyFromAKBMarker(t *testing.T) {
	e := claimed(t)
	for _, root := range []string{"/src/proj", "/kb/sub", "/notes/q"} {
		e.disk.MkdirAll(root + "/.todo-list/open")
		if _, err := e.m.Register(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	e.disk.WriteFile("/src/proj/AGENTS.md", []byte("# proj\n\ntype: kb\n"))           // not frontmatter
	e.disk.WriteFile("/notes/AGENTS.md", []byte("---\ntype: \"kb\"\n---\n# notes\n")) // quoted
	for root, want := range map[string]string{"/src/proj": "", "/kb/sub": "/kb", "/notes/q": "/notes"} {
		if v := varOf(t, e, todo.At{Root: root}, "KB_ROOT"); v.Value != want || v.Derived != want {
			t.Errorf("%s: KB_ROOT %+v, want %q", root, v, want)
		}
		if v := varOf(t, e, todo.At{Root: root}, "WORKSPACE"); v.Value != root {
			t.Errorf("%s: WORKSPACE %+v: the store's home is its parent", root, v)
		}
	}
}

// A store's variable file: relative values are the store directory's; a file override wins over the
// derived built-in, which todos.vars still shows. A file the format refuses is ignored whole and
// reported once; mending it is reported too.
func TestTheStoresVariableFile(t *testing.T) {
	e := claimed(t)
	file := kbStore + "/.autodoc-vars.toml"
	e.disk.WriteFile(file, []byte("machine = [\"DUMPS\"]\n\n[vars]\nPROJECT_ROOT = \"../../src/p\"\nWORKSPACE = \"/w\"\n"))
	if v := varOf(t, e, kb, "PROJECT_ROOT"); v.Value != "/src/p" || v.Source != "store" {
		t.Errorf("a relative value: %+v", v)
	}
	if v := varOf(t, e, kb, "WORKSPACE"); v.Value != "/w" || v.Source != "store" || v.Derived != "/kb" {
		t.Errorf("an overridden built-in: %+v", v)
	}
	if v := varOf(t, e, kb, "DUMPS"); !v.Machine || v.Value != "" {
		t.Errorf("a machine-only name: %+v", v)
	}
	for _, bad := range []string{
		"[vars]\nmachine = [\"DUMPS\"]\n",         // machine under [vars]: a variable named machine
		"extra = 1\n[vars]\nP = \"/p\"\n",         // a key the format has not
		"machine = [\"P\"]\n[vars]\nP = \"/p\"\n", // machine-only, yet given a value here
		"[vars]\n1BAD = \"/p\"\n",
		"[vars]\nP = \"\"\n",
		"[vars\n",
	} {
		e.disk.WriteFile(file, []byte(bad))
		if v := varOf(t, e, kb, "WORKSPACE"); v.Value != "/kb" || v.Source != "derived" {
			t.Errorf("%q was not ignored whole: %+v", bad, v)
		}
	}
	e.disk.WriteFile(file, []byte("[vars\n"))
	varOf(t, e, kb, "WORKSPACE")
	varOf(t, e, kb, "WORKSPACE")
	e.disk.WriteFile(file, []byte("[vars]\nP = \"/p\"\n"))
	varOf(t, e, kb, "P")
	kinds := strings.Join(e.eventKinds(), " ")
	if n := strings.Count(kinds, "todos.vars_invalid"); n != 6 {
		t.Errorf("%d vars_invalid events, want one per distinct error (6): %s", n, kinds)
	}
	if !strings.HasSuffix(kinds, "todos.vars_valid") {
		t.Errorf("mending the file was not reported: %s", kinds)
	}
}

// A machine-only name with no value here is never an errors[] entry: it is a fact about this machine.
// Any other name no layer defines is, the same on every machine.
func TestMachineOnlyNamesAreNeverPersistedAsErrors(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("machine = [\"DUMPS\"]\n"))
	e.disk.WriteFile(kbStore+"/open/m.md", []byte(taskFile("m", "open", "adr:\n  - $DUMPS/x.md\n  - $NOPE/y.md")))
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	m := e.get("m")
	if len(m.Errors) != 1 || m.Errors[0].Field != "adr[1]" || m.Errors[0].Code != todo.CodeUnresolved {
		t.Errorf("errors %+v: want only adr[1] unresolved", m.Errors)
	}
	e.disk.WriteFile("/data/x.md", []byte("x"))
	e.must(e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine}))
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/data" || v.Source != "machine" || !v.Machine {
		t.Errorf("a machine value: %+v", v)
	}
	// this machine's value comes first, even over a value the file gained by hand since
	e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("[vars]\nDUMPS = \"/from/the/file\"\n"))
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/data" || v.Source != "machine" {
		t.Errorf("the file's value won over this machine's: %+v", v)
	}
}

// todos.set_var's scopes: the store's file (written, rendered stable, removed when empty), this
// machine (the daemon's, and the name declared machine-only in the file), and global (never a
// built-in). A store or machine write needs a writable store.
func TestSetVarScopes(t *testing.T) {
	e := claimed(t)
	file := kbStore + "/.autodoc-vars.toml"
	e.must(e.m.SetVar(ctx, "PROJ", "/p", todo.SetVarOpts{At: &kb}))
	e.must(e.m.SetVar(ctx, "WORKSPACE", "/w", todo.SetVarOpts{At: &kb, Scope: todo.ScopeStore}))
	if got := e.read(file); !strings.Contains(got, "[vars]\nPROJ = \"/p\"\nWORKSPACE = \"/w\"\n") {
		t.Errorf("the file:\n%s", got)
	}
	if v := varOf(t, e, kb, "WORKSPACE"); v.Value != "/w" || v.Derived != "/kb" {
		t.Errorf("a store override of a built-in: %+v", v)
	}
	e.must(e.m.SetVar(ctx, "LOGS", "/var/l", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine}))
	if got := e.read(file); !strings.Contains(got, "machine = [\"LOGS\"]") || strings.Contains(got, "/var/l") {
		t.Errorf("a machine value travels in the file, or its name is not declared:\n%s", got)
	}
	if err := e.m.SetVar(ctx, "LOGS", "/x", todo.SetVarOpts{At: &kb, Scope: todo.ScopeStore}); !is(err, todo.ErrInvalid) {
		t.Errorf("a store value for a machine-only name: %v", err)
	}
	other := todo.At{Root: "/src/other"}
	e.disk.MkdirAll("/src/other/.todo-list/open")
	if err := e.m.SetVar(ctx, "X", "/x", todo.SetVarOpts{At: &other}); !is(err, todo.ErrNotWritable) {
		t.Errorf("an unregistered root's store: %v", err)
	}
	if err := e.m.SetVar(ctx, "X", "/x", todo.SetVarOpts{Scope: todo.ScopeStore}); !is(err, todo.ErrInvalid) {
		t.Errorf("a store scope without an at: %v", err)
	}
	for _, n := range []string{"PROJ", "WORKSPACE"} {
		e.must(e.m.SetVar(ctx, n, "", todo.SetVarOpts{At: &kb}))
	}
	e.must(e.m.SetVar(ctx, "LOGS", "", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine}))
	if got := e.read(file); got == "" || strings.Contains(got, "[vars]") {
		t.Errorf("after clearing the values the file holds only the machine list:\n%s", got)
	}
	e.disk.WriteFile(file, []byte("machine = [\"LOGS\"]\n"))
	e.must(func() error {
		return e.m.SetVar(ctx, "PROJ", "/p", todo.SetVarOpts{At: &kb})
	}())
	e.must(e.m.SetVar(ctx, "PROJ", "", todo.SetVarOpts{At: &kb}))
	if !strings.Contains(e.read(file), "LOGS") {
		t.Error("clearing a value dropped the machine list")
	}
}

// A claim from auto-core records the project it names as the store's PROJECT_ROOT when the store is
// inside that project (LabelManager's KB), so the cutover writes $PROJECT_ROOT/…; a store outside it
// (a global KB several projects share) records none (TestEachRootsClaimRewritesItsOwnLegacyRefs).
func TestAClaimRecordsTheProjectItsStoreIsIn(t *testing.T) {
	e := newEnv(t)
	e.roots["lm"] = "/lm/.auto-agents/kb"
	lm := todo.At{Workspace: "lm"}
	e.disk.WriteFile("/lm/.auto-agents/kb/AGENTS.md", []byte(kbAgents))
	e.disk.WriteFile("/lm/docs/d.md", []byte("x"))
	e.disk.WriteFile("/lm/.auto-agents/kb/.todo-list/open/l.md", []byte(taskFile("l", "open", "adr:\n  - $WORKSPACE/docs/d.md")))
	e.must(e.m.Claim(ctx, lm, todo.ClaimOpts{LegacyWorkspace: "/lm"}))
	if v := varOf(t, e, lm, "PROJECT_ROOT"); v.Value != "/lm" || v.Source != "store" ||
		!strings.Contains(e.read("/lm/.auto-agents/kb/.todo-list/.autodoc-vars.toml"), `PROJECT_ROOT = "../../.."`) {
		t.Errorf("PROJECT_ROOT %+v", v)
	}
	l, err := e.m.Get(ctx, lm, "l")
	if err != nil || !slices.Equal(l.ADR, []string{"$PROJECT_ROOT/docs/d.md"}) {
		t.Errorf("the cutover's ref %v %v", l.ADR, err)
	}
}

// The kb_roots older clients left on a store's routes move into the store's file once, when they
// agree and differ from what the location derives, and are then cleared; routes that disagree are
// reported and left as they are.
func TestCarryKBRoots(t *testing.T) {
	setRoots := func(e *env, kbRoots map[string]string) {
		t.Helper()
		e.must(e.j.UpdateSettings(ctx, func(s *todo.Settings) error {
			if s.Roots == nil {
				s.Roots = map[string]todo.RootRecord{}
			}
			for root, v := range kbRoots {
				rr := s.Roots[root]
				rr.Root, rr.KBRoot = root, v
				if root != "/kb" {
					rr.Override = kbStore
				}
				s.Roots[root] = rr
			}
			return nil
		}))
	}
	roots := func(e *env) map[string]string {
		st, _ := e.j.Settings(ctx)
		out := map[string]string{}
		for r, rr := range st.Roots {
			if rr.KBRoot != "" {
				out[r] = rr.KBRoot
			}
		}
		return out
	}
	file := kbStore + "/.autodoc-vars.toml"

	e := claimed(t)
	folderRoute(t, e)
	setRoots(e, map[string]string{"/kb": "/elsewhere", "/src/proj": "/elsewhere"})
	e.m.CarryKBRoots(ctx)
	if v := varOf(t, e, kb, "KB_ROOT"); v.Value != "/elsewhere" || v.Source != "store" || !strings.Contains(e.read(file), `KB_ROOT = "../../elsewhere"`) {
		t.Errorf("an agreeing kb_root was not carried: %+v\n%s", v, e.read(file))
	}
	if r := roots(e); len(r) != 0 {
		t.Errorf("routes keep their kb_roots: %v", r)
	}

	e = claimed(t)
	setRoots(e, map[string]string{"/kb": "/kb"})
	e.m.CarryKBRoots(ctx)
	if e.exists(file) || len(roots(e)) != 0 {
		t.Errorf("a kb_root equal to the derived one wrote a file or stayed: %v", roots(e))
	}

	// a refused file cannot take the carried value: the routes keep theirs until it is mended, and
	// the next start carries it
	e = claimed(t)
	e.disk.WriteFile(file, []byte("[vars\n"))
	setRoots(e, map[string]string{"/kb": "/elsewhere"})
	e.m.CarryKBRoots(ctx)
	if r := roots(e); r["/kb"] != "/elsewhere" || !slices.Contains(e.eventKinds(), "todos.vars_carry_blocked") || e.read(file) != "[vars\n" {
		t.Fatalf("a refused file: roots %v, events %v, file %q", r, e.eventKinds(), e.read(file))
	}
	e.disk.WriteFile(file, []byte("[vars]\nP = \"/p\"\n"))
	e.m.CarryKBRoots(ctx)
	if v := varOf(t, e, kb, "KB_ROOT"); v.Value != "/elsewhere" || len(roots(e)) != 0 {
		t.Errorf("after mending the file: %+v, roots %v", v, roots(e))
	}

	e = claimed(t)
	folderRoute(t, e)
	setRoots(e, map[string]string{"/kb": "/a", "/src/proj": "/b"})
	e.m.CarryKBRoots(ctx)
	if e.exists(file) || len(roots(e)) != 2 || !slices.Contains(e.eventKinds(), "todos.vars_carry_conflict") {
		t.Errorf("disagreeing routes: file %v, roots %v, events %v", e.exists(file), roots(e), e.eventKinds())
	}
}

// A machine value whose declaration the store's file cannot take is not kept either: no machine may
// hold a value its peers would read as an undefined name.
func TestAFailedMachineDeclarationKeepsNoValue(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/.autodoc-vars.toml", []byte("[vars\n"))
	if err := e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine}); err == nil {
		t.Fatal("a machine value set though its declaration could not be written")
	}
	st, err := e.j.Settings(ctx)
	if err != nil || len(st.MachineVars) != 0 {
		t.Errorf("a failed declaration kept the value: %+v %v", st.MachineVars, err)
	}
}

// Making a portable variable machine-only rewrites the store's file first; if this machine's value
// then cannot be kept, the file is put back, so the portable value is never lost.
func TestAFailedMachineValueRestoresTheFile(t *testing.T) {
	e := claimed(t)
	file := kbStore + "/.autodoc-vars.toml"
	e.disk.WriteFile(file, []byte("[vars]\nDUMPS = \"/old\"\n"))
	e.j.Fail = func(op string) error {
		if op == "UpdateSettings" {
			return errors.New("the settings write failed")
		}
		return nil
	}
	err := e.m.SetVar(ctx, "DUMPS", "/data", todo.SetVarOpts{At: &kb, Scope: todo.ScopeMachine})
	e.j.Fail = nil
	if err == nil {
		t.Fatal("a failed settings write reported success")
	}
	if v := varOf(t, e, kb, "DUMPS"); v.Value != "/old" || v.Source != "store" || v.Machine {
		t.Errorf("the portable value was lost: %+v\n%s", v, e.read(file))
	}
}

// A variable file over the limit is refused whole, never read as its first part.
func TestAnOversizedVariableFileIsRefused(t *testing.T) {
	e := claimed(t)
	file := kbStore + "/.autodoc-vars.toml"
	pad := "# " + strings.Repeat("x", 64<<10) + "\n"
	e.disk.WriteFile(file, []byte(pad+"[vars]\nP = \"/p\"\n"))
	if v := varOf(t, e, kb, "P"); v.Value != "" || !slices.Contains(e.eventKinds(), "todos.vars_invalid") {
		t.Errorf("an oversized file was read, not refused: %+v, events %v", v, e.eventKinds())
	}
	e.disk.WriteFile(file, []byte(pad[:60<<10]+"\n[vars]\nP = \"/p\"\n"))
	if v := varOf(t, e, kb, "P"); v.Value != "/p" {
		t.Errorf("a file under the limit: %+v", v)
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
