package daemon

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TestAPathsModeIsTheFirstRuleItMatches (docs/decisions/todos.md#which-paths-are-graph-only): a
// user's graph_only pattern makes a path graph-only whatever todo_search says; a todo store's path,
// by the registry or the default entry, is graph-only unless todo_search is on; anything else is
// full. A store path has its store in every mode.
func TestAPathsModeIsTheFirstRuleItMatches(t *testing.T) {
	m, _ := open(t)
	ws := t.TempDir() // an absolute root on any OS
	m.stores.Store(newRegistry(TodoRegistry{Stores: []TodoStore{{ID: "S", Dir: filepath.Join(ws, "tasks"), KB: ws}},
		Vars: map[string]map[string]string{ws: {"KB_ROOT": ws}}}))
	h := &modes{m: m}
	h.setRoot(ws)
	type want struct {
		path  string
		graph bool
		store string
	}
	for _, c := range []struct {
		name   string
		list   []string
		search bool
		want   []want
	}{
		{"todo_search off", nil, false, []want{
			{".todo-list/open/a.md", true, ""}, {"p/.todo-list/open/a.md", true, ""}, {"tasks/open/a.md", true, "S"},
			{"notes/a.md", false, ""}}},
		{"the default: todo_search on", nil, true, []want{
			{".todo-list/open/a.md", false, ""}, {"tasks/open/a.md", false, "S"}, {"notes/a.md", false, ""}}},
		{"a user's pattern beats todo_search", []string{"drafts/**", workspace.TodoPattern}, true, []want{
			{"drafts/a.md", true, ""}, {".todo-list/open/a.md", false, ""}, {"tasks/open/a.md", false, "S"}}},
		{"a user's pattern over a store", []string{"tasks/**"}, true, []want{{"tasks/open/a.md", true, "S"}}},
		{"the default entry removed", []string{"drafts/**"}, false, []want{
			{".todo-list/open/a.md", false, ""}, {"tasks/open/a.md", true, "S"}}},
		{"an empty list", []string{}, false, []want{{".todo-list/open/a.md", false, ""}, {"tasks/open/a.md", true, "S"}}},
	} {
		h.set(c.list, c.search)
		for _, w := range c.want {
			md := h.mode(w.path)
			id := ""
			if md.Store != nil {
				id = md.Store.ID
			}
			if md.GraphOnly != w.graph || id != w.store {
				t.Errorf("%s: %s: graph-only %v, store %q; want %v, %q", c.name, w.path, md.GraphOnly, id, w.graph, w.store)
			}
		}
	}
}

// TestAStoreIsFoundWhateverFormItsPathTakes: the todo system keeps its directories in the slash
// form (core/paths), a workspace's root and a test's directory are in the OS's own, and a document
// is compared with its store and its workspace's variables in one form, exactly. A path under a
// store is found, given in either form against a registry given in either; a sibling sharing the
// store's prefix ("kb" and "kbx") is not, and neither is one whose name differs only in case: a
// path's identity is the filesystem's, never case-folded.
func TestAStoreIsFoundWhateverFormItsPathTakes(t *testing.T) {
	base := t.TempDir() // the OS's own form
	dir := filepath.Join(base, "kb")
	for _, reg := range []struct {
		form string
		r    TodoRegistry
	}{
		{"the slash form", TodoRegistry{Stores: []TodoStore{{ID: "S", Dir: paths.Slash(dir), KB: paths.Slash(base)}},
			Vars: map[string]map[string]string{paths.Slash(base): {"KB_ROOT": "base"}}}},
		{"the OS's own form", TodoRegistry{Stores: []TodoStore{{ID: "S", Dir: dir, KB: base}},
			Vars: map[string]map[string]string{base: {"KB_ROOT": "base"}}}},
	} {
		m := &Workspaces{}
		m.stores.Store(newRegistry(reg.r))
		in := filepath.Join(dir, "open", "t.md")
		for _, c := range []struct {
			abs   string
			store bool
		}{
			{in, true},
			{paths.Slash(in), true},
			{dir, true},
			{filepath.Join(base, "kbx", "open", "t.md"), false},
			{paths.Slash(filepath.Join(base, "kbx", "open", "t.md")), false},
			{filepath.Join(base, "KB", "open", "t.md"), false},
		} {
			st, vars := m.storeAt(c.abs)
			if (st != nil) != c.store {
				t.Errorf("registry in %s: %s: store %+v, want found=%v", reg.form, c.abs, st, c.store)
			}
			if got := vars.Get("KB_ROOT"); got != "base" {
				t.Errorf("registry in %s: %s: KB_ROOT %q, want the workspace's", reg.form, c.abs, got)
			}
		}
		h := &modes{m: m}
		h.setRoot(base)
		h.set(nil, true)
		for p, want := range map[string]string{"kb/open/t.md": "S", "kbx/open/t.md": "", "KB/open/t.md": ""} {
			id := ""
			if md := h.mode(p); md.Store != nil {
				id = md.Store.ID
			}
			if id != want {
				t.Errorf("registry in %s: mode(%q): store %q, want %q", reg.form, p, id, want)
			}
		}
		// an exclude reaching the store is said for the workspace holding it, and for no workspace
		// whose root only shares its prefix ("k" and "kb") or is the store itself
		exclude := []string{"kb/**", "**/open/**"}
		for root, want := range map[string][]string{base: exclude, filepath.Join(base, "k"): nil, dir: nil} {
			if got := m.todoExcluded(root, exclude); !slices.Equal(got, want) {
				t.Errorf("registry in %s: root %s: excluded %v, want %v", reg.form, root, got, want)
			}
		}
	}
}

// stores is a test's todo registry, changed while the daemon runs.
type stores struct {
	mu    sync.Mutex
	list  []TodoStore
	vars  map[string]map[string]string
	files []string
}

func (s *stores) read(context.Context) (TodoRegistry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TodoRegistry{Stores: slices.Clone(s.list), Vars: maps.Clone(s.vars), Files: slices.Clone(s.files)}, nil
}

func (s *stores) setVars(vars map[string]map[string]string) {
	s.mu.Lock()
	s.vars = vars
	s.mu.Unlock()
}

// TestADocumentsVariablesAreItsWorkspaces
// (docs/decisions/todos.md#resolving-refs-in-every-document): the owner rule workspace.of answers.
// A store's path reads its recorded workspace's variables, wherever the store lies; any other path
// the deepest workspace's holding it; a path in none, those of no workspace. With no variables in
// the registry, none.
func TestADocumentsVariablesAreItsWorkspaces(t *testing.T) {
	g := newRegistry(TodoRegistry{
		Stores: []TodoStore{{ID: "out", Dir: "/out/.todo-list", KB: "/ws"}, {ID: "in", Dir: "/ws/nested/.todo-list", KB: "/ws"}},
		Vars:   map[string]map[string]string{"": {"HOME": "/h"}, "/ws": {"KB_ROOT": "/ws"}, "/ws/nested": {"KB_ROOT": "/ws/nested"}},
	})
	m := &Workspaces{}
	m.stores.Store(g)
	for _, c := range []struct{ abs, kb string }{
		{"/out/.todo-list/open/t.md", "/ws"},       // linked with todos.set_kb
		{"/ws/nested/.todo-list/open/t.md", "/ws"}, // its owner recorded before the nested workspace
		{"/ws/nested/notes/n.md", "/ws/nested"},
		{"/ws/notes/n.md", "/ws"},
		{"/elsewhere/n.md", ""},
	} {
		_, vars := m.storeAt(c.abs)
		if want := g.modes[c.kb]; vars != want {
			t.Errorf("%s: the variables of %v, want %s's", c.abs, vars, c.kb)
		}
	}
	m.stores.Store(newRegistry(TodoRegistry{Stores: g.stores}))
	if _, vars := m.storeAt("/ws/notes/n.md"); vars != nil {
		t.Errorf("no variables in the registry: %v", vars)
	}
}

// TestAddingOrRemovingAWorkspaceRereadsTheTodoRegistry
// (docs/decisions/todos.md#which-workspace-a-store-belongs-to): a store recorded to a workspace
// belongs to it while it is served and to none once Workspaces.Remove takes it, and the modes'
// cached registry follows both changes with no other wake: Remove and Add read it again.
func TestAddingOrRemovingAWorkspaceRereadsTheTodoRegistry(t *testing.T) {
	m, _ := open(t)
	root, out := t.TempDir(), t.TempDir()
	task := filepath.Join(out, "tasks", "open", "t.md")
	// the todo system's answer: the store outside every workspace is linked to root's, which owns it
	// and has variables only while it is served
	reg := func(context.Context) (TodoRegistry, error) {
		r := TodoRegistry{Vars: map[string]map[string]string{"": {"HOME": "/h"}}}
		kb := ""
		for _, w := range m.List() {
			r.Vars[w.Root()] = map[string]string{"KB_ROOT": w.Root()}
			if w.Root() == root {
				kb = root
			}
		}
		r.Stores = []TodoStore{{ID: "S", Dir: filepath.Join(out, "tasks"), KB: kb}}
		return r, nil
	}
	m.SetTodoStores(reg)
	kbRoot := func() string { _, vars := m.storeAt(task); return vars.Get("KB_ROOT") }
	if got := kbRoot(); got != "" {
		t.Fatalf("before the workspace exists: KB_ROOT %q", got)
	}
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Add reads the registry again: the store's workspace is served", func() bool { return kbRoot() == root })
	if err := m.Remove(context.Background(), "kb"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Remove reads the registry again: the store belongs to none", func() bool { return kbRoot() == "" })
}

// TestAVariableChangeReindexesWithoutAnEdit
// (docs/decisions/todos.md#resolving-refs-in-every-document): a note's $NAME relation resolves
// through its workspace's variables, and a change to them re-resolves it, the note unchanged: one
// the todo system says, and an edit to a variable file it names, which no call says.
func TestAVariableChangeReindexesWithoutAnEdit(t *testing.T) {
	m, _ := open(t)
	root := realTemp(t)
	reg := &stores{}
	reg.setVars(map[string]map[string]string{root: {"PROJECT_ROOT": filepath.Join(root, "a")}})
	reg.files = []string{filepath.Join(root, ".autodoc-vars.toml")}
	m.SetTodoStores(reg.read)
	writeAll(t, root, map[string]string{"notes/n.md": "---\nrelated: [$PROJECT_ROOT/x.md]\n---\nn\n",
		"a/x.md": "# A\n", "b/x.md": "# B\n"})
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 3)
	w, _ := m.Get("kb")
	target := func() string {
		ls, err := w.Index().Store().Links(context.Background(), "notes/n.md", nil)
		if err != nil || len(ls) != 1 || !ls[0].Resolved {
			return ""
		}
		return ls[0].Path
	}
	eventually(t, "the relation reaches a/x.md", func() bool { return target() == "a/x.md" })
	reg.setVars(map[string]map[string]string{root: {"PROJECT_ROOT": filepath.Join(root, "b")}})
	m.TodoStoresChanged()
	eventually(t, "the relation reaches b/x.md", func() bool { return target() == "b/x.md" })

	reg.setVars(map[string]map[string]string{root: {"PROJECT_ROOT": filepath.Join(root, "a")}})
	writeAll(t, root, map[string]string{".autodoc-vars.toml": "[vars]\nPROJECT_ROOT = \"a\"\n"})
	eventually(t, "the polled edit: the relation reaches a/x.md again", func() bool { return target() == "a/x.md" })
}

func (s *stores) set(list ...TodoStore) {
	s.mu.Lock()
	s.list = list
	s.mu.Unlock()
}

// hits are the paths the workspace's search finds for q.
func hits(t *testing.T, m *Workspaces, name, q string) []string {
	t.Helper()
	w, ok := m.Get(name)
	if !ok || w.Index() == nil {
		t.Fatalf("%s is not served", name)
	}
	res, err := w.Index().Store().Search(context.Background(), q, index.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range res.Hits {
		out = append(out, h.Path)
	}
	return out
}

// realTemp is a new temporary directory by the path the disk gives it (its symlinks resolved; on
// Windows its long names): the spelling the todo registry holds for a root or a store, which a
// hand-made registry here must hold too.
func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := canonicalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeAll(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAStoreChangeReindexesWithoutAnEdit (docs/decisions/todos.md#which-paths-are-graph-only): in a
// workspace with todo_search off, a crawled directory becomes a todo store (a set_dir to a store
// not named .todo-list): its tasks leave search, their graph links stay, and no file changed. The
// store removed, they are back in search.
func TestAStoreChangeReindexesWithoutAnEdit(t *testing.T) {
	m, _ := open(t)
	reg := &stores{}
	m.SetTodoStores(reg.read)
	root := realTemp(t)
	writeAll(t, root, map[string]string{"notes/n.md": "# N\n\nsee [[t]]\n",
		"tasks/open/t.md": "---\nrelated: [[n]]\n---\nzanzibar task\n"})
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 2)
	if err := m.Configure(context.Background(), "kb", store.Changes{TodoSearch: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if got := hits(t, m, "kb", "zanzibar"); !slices.Equal(got, []string{"tasks/open/t.md"}) {
		t.Fatalf("before it is a store: %v", got)
	}

	reg.set(TodoStore{ID: "S", Dir: filepath.Join(root, "tasks")})
	m.TodoStoresChanged()
	eventually(t, "the store's task leaves search", func() bool { return len(hits(t, m, "kb", "zanzibar")) == 0 })
	w, _ := m.Get("kb")
	ls, err := w.Index().Store().Links(context.Background(), "tasks/open/t.md", nil)
	if err != nil || len(ls) != 1 || ls[0].Path != "notes/n.md" {
		t.Errorf("its links %+v, %v; want its relation to notes/n.md", ls, err)
	}
	back, err := w.Index().Store().Backlinks(context.Background(), "tasks/open/t.md", nil)
	if err != nil || len(back) != 1 || back[0].Path != "notes/n.md" {
		t.Errorf("its backlinks %+v, %v", back, err)
	}

	reg.set()
	m.TodoStoresChanged()
	eventually(t, "no longer a store, it is in search", func() bool {
		return slices.Equal(hits(t, m, "kb", "zanzibar"), []string{"tasks/open/t.md"})
	})
}

// TestTodoSearchAndGraphOnlyApplyWithoutARestart: a workspace's todo tasks are in search by default
// (docs/decisions/todos.md#tasks-stay-in-search-by-default); workspace.configure's todo_search and
// graph_only re-index the paths whose mode changed, the workspace's indexer as it was, and
// workspace.list reports them.
func TestTodoSearchAndGraphOnlyApplyWithoutARestart(t *testing.T) {
	m, db := open(t)
	m.SetTodoStores((&stores{}).read)
	root := t.TempDir()
	writeAll(t, root, map[string]string{".todo-list/open/t.md": "zanzibar task\n", "a.md": "alpha\n"})
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 2)
	if got := hits(t, m, "kb", "zanzibar"); !slices.Equal(got, []string{".todo-list/open/t.md"}) {
		t.Fatalf("a .todo-list task is not in search by default: %v", got)
	}
	w, _ := m.Get("kb")
	ix := w.Index()

	if err := m.Configure(context.Background(), "kb", store.Changes{TodoSearch: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "todo_search off: the task leaves search", func() bool { return len(hits(t, m, "kb", "zanzibar")) == 0 })
	if err := m.Configure(context.Background(), "kb", store.Changes{TodoSearch: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "todo_search on: the task is in search", func() bool {
		return slices.Equal(hits(t, m, "kb", "zanzibar"), []string{".todo-list/open/t.md"})
	})
	if w, _ := m.Get("kb"); w.Index() != ix {
		t.Error("todo_search restarted the workspace")
	}

	list := []string{".todo-list/**"} // a user's own pattern: graph-only whatever todo_search says
	if err := m.Configure(context.Background(), "kb", store.Changes{GraphOnly: &list}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a user's graph_only pattern takes it out again", func() bool { return len(hits(t, m, "kb", "zanzibar")) == 0 })
	got, search, _ := w.(*view).TodoModes()
	if !slices.Equal(got, list) || !search {
		t.Errorf("TodoModes %v %v", got, search)
	}
	stored, storedSearch, err := db.WorkspaceModes(context.Background(), w.(*view).s.id)
	if err != nil || !slices.Equal(stored, list) || !storedSearch {
		t.Errorf("the store holds %v %v %v", stored, storedSearch, err)
	}
	if err := m.Configure(context.Background(), "kb", store.Changes{GraphOnly: &[]string{"[bad"}}); err == nil {
		t.Error("an invalid graph_only pattern was saved")
	}
}

// TestAnExcludedStoreIsSaid (docs/decisions/todos.md#excluded-todo-stores-are-reported): a
// workspace whose exclude keeps a todo store out of the index is reported, with the pattern, by
// workspace.list and in an event at start.
func TestAnExcludedStoreIsSaid(t *testing.T) {
	m, db := open(t)
	root := realTemp(t)
	writeAll(t, root, map[string]string{".todo-list/open/t.md": "t\n", "a.md": "a\n"})
	reg := &stores{}
	reg.set(TodoStore{ID: "S", Dir: filepath.Join(root, ".todo-list")})
	m.SetTodoStores(reg.read)
	if _, err := db.AddWorkspace(context.Background(), "kb", root, nil, []string{".git/**", ".todo-list/**"}); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenAll(); err != nil {
		t.Fatal(err)
	}
	w, _ := m.Get("kb")
	if _, _, excluded := w.(*view).TodoModes(); !slices.Equal(excluded, []string{".todo-list/**"}) {
		t.Errorf("excluded %v", excluded)
	}
	evs, _, _, err := db.Events(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(evs, func(e store.Event) bool { return e.Kind == "workspace.todo_excluded" && e.Workspace == "kb" }) {
		t.Errorf("no workspace.todo_excluded event: %+v", evs)
	}
	// a store the exclude does not reach is not reported
	reg.set(TodoStore{ID: "S", Dir: filepath.Join(root, "elsewhere")})
	m.TodoStoresChanged()
	eventually(t, "the moved store is not excluded", func() bool {
		_, _, excluded := w.(*view).TodoModes()
		return len(excluded) == 0
	})
}

// A view whose workspace has no modes yet (it never started) reports the defaults.
func TestAViewWithoutModesReportsTheDefaults(t *testing.T) {
	m, _ := open(t)
	v := &view{m: m, s: &served{id: 999, root: "/nowhere"}, name: "gone"}
	list, search, excluded := v.TodoModes()
	if !slices.Equal(list, workspace.DefaultGraphOnly()) || !search || excluded != nil {
		t.Errorf("TodoModes %v %v %v", list, search, excluded)
	}
}

// TestTodoStoresSettledWaitsForTheReadOfAChange: a todo verb that made a store waits on
// TodoStoresSettled, and it returns only once the reader has read every change told before it
// (and revalidated): not while that read is under way, and not after a read that failed, which is
// tried again (storesRetry) and settles only when it succeeds. With nothing pending it returns at
// once, with no reader running (no registry given) it never waits, and a wait whose ctx ends while
// the reader is held answers that.
func TestTodoStoresSettledWaitsForTheReadOfAChange(t *testing.T) {
	ctx := context.Background()
	idle, _ := open(t)
	idle.TodoStoresChanged()
	if err := idle.TodoStoresSettled(ctx); err != nil {
		t.Fatalf("no reader: %v, want an answer at once", err)
	}

	m, _ := open(t)
	type read struct {
		entered chan struct{} // closed as the reader starts this read
		release chan struct{} // this read answers once it is closed
		fail    bool
		reg     TodoRegistry // what it answers
	}
	var mu sync.Mutex
	var plan []*read // the reads after SetTodoStores's own, in order
	var reads atomic.Int32
	var failRevalidation atomic.Bool
	m.revalidateAll = func() error { // set before the reader starts
		if failRevalidation.Swap(false) {
			return errors.New("revalidation failed")
		}
		return nil
	}
	m.SetTodoStores(func(context.Context) (TodoRegistry, error) {
		n := int(reads.Add(1)) - 2 // SetTodoStores's own first read is -1: it answers at once
		if n < 0 {
			return TodoRegistry{}, nil
		}
		mu.Lock()
		r := plan[n]
		mu.Unlock()
		close(r.entered)
		<-r.release
		if r.fail {
			return TodoRegistry{}, errors.New("the registry could not be read")
		}
		return r.reg, nil
	})
	next := func(fail bool) *read {
		r := &read{entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
		mu.Lock()
		plan = append(plan, r)
		mu.Unlock()
		return r
	}
	if err := m.TodoStoresSettled(ctx); err != nil {
		t.Fatalf("nothing pending: %v", err)
	}
	settled := func() chan error {
		done := make(chan error, 1)
		go func() { done <- m.TodoStoresSettled(ctx) }()
		return done
	}
	notYet := func(what string, done chan error) {
		if want := m.storesWant.Load(); m.storesDone.Load() >= want {
			t.Fatalf("%s: the change is marked read (%d of %d) before its read succeeded", what, m.storesDone.Load(), want)
		}
		select {
		case err := <-done:
			t.Fatalf("%s: settled (%v) before its read succeeded", what, err)
		default:
		}
	}

	held := next(false)
	m.TodoStoresChanged()
	done := settled()
	<-held.entered // the reader is in its read of the change
	notYet("the read under way", done)
	close(held.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	failing, retried := next(true), next(false)
	m.TodoStoresChanged()
	done = settled()
	<-failing.entered
	close(failing.release) // the read fails
	<-retried.entered      // and is tried again: until it answers, nothing settled
	notYet("a failed read", done)
	close(retried.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// a new registry read whose revalidation fails: the retry reads the same registry, and still
	// revalidates before it settles
	moved := TodoRegistry{Vars: map[string]map[string]string{"/kb": {"PROJECT_ROOT": "/src"}}}
	changedRead := next(false)
	changedRead.reg = moved
	sameRead := next(false)
	sameRead.reg = moved
	failRevalidation.Store(true)
	m.TodoStoresChanged()
	done = settled()
	<-changedRead.entered
	close(changedRead.release) // read, published, and its revalidation fails
	<-sameRead.entered         // tried again: the same registry
	notYet("a failed revalidation", done)
	close(sameRead.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.storesRevalidate {
		t.Error("the revalidation is still marked to do again after it succeeded")
	}

	stuck := next(false)
	stuck.reg = moved
	m.TodoStoresChanged()
	<-stuck.entered
	bounded, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.TodoStoresSettled(bounded); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait while the reader is held: %v, want context.Canceled", err)
	}
	close(stuck.release)
}

// aliasOf is another spelling of the directory real, which the disk resolves to it: a symlink to it
// where the OS lets this process make one. ok is false, with why, where it does not.
func aliasOf(t *testing.T, real string) (alias string, ok bool) {
	t.Helper()
	alias = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, alias); err != nil {
		t.Logf("no symlink to %s could be made: %v", real, err)
		return "", false
	}
	return alias, true
}

// aliasFixture is a workspace's files under real, and the todo registry as the todo system gives
// it: every path where the disk says it is. The note names a document through $KB_ROOT, and the
// store's task holds a word no other file does.
func aliasFixture(t *testing.T, m *Workspaces, real string) *stores {
	t.Helper()
	writeAll(t, real, map[string]string{"notes/n.md": "---\nrelated: [$KB_ROOT/a/x.md]\n---\nn\n", "a/x.md": "# A\n",
		"tasks/open/t.md": "zanzibar task\n"})
	reg := &stores{}
	reg.set(TodoStore{ID: "S", Dir: filepath.Join(real, "tasks"), KB: real})
	reg.setVars(map[string]map[string]string{real: {"KB_ROOT": real}})
	m.SetTodoStores(reg.read)
	return reg
}

// servedThroughAnAlias checks a workspace served under name, whose root was configured as another
// spelling of the directory the registry names: its store's task is the store's (graph-only with
// todo_search off, so out of search), and its note's $KB_ROOT relation is its own document.
func servedThroughAnAlias(t *testing.T, m *Workspaces, name string) {
	t.Helper()
	indexed(t, m, name, 3)
	if err := m.Configure(context.Background(), name, store.Changes{TodoSearch: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the task is its store's: with todo_search off it leaves search", func() bool { return len(hits(t, m, name, "zanzibar")) == 0 })
	w, _ := m.Get(name)
	eventually(t, "the note's $KB_ROOT relation is the workspace's a/x.md", func() bool {
		ls, err := w.Index().Store().Links(context.Background(), "notes/n.md", nil)
		return err == nil && len(ls) == 1 && ls[0].Resolved && ls[0].Path == "a/x.md"
	})
}

// TestAWorkspaceConfiguredThroughASymlinkFindsItsStoreAndVariables
// (docs/decisions/todos.md#which-paths-are-graph-only,
// docs/decisions/todos.md#resolving-refs-in-every-document): the todo registry names every root and
// store where the disk says it is. A workspace configured through a symlink to its root is asked,
// once as it starts, where that leads, and its paths are compared with the registry in that
// spelling: its store and its variables are found, and an exclude reaching the store is said. It is
// listed under the root as configured.
func TestAWorkspaceConfiguredThroughASymlinkFindsItsStoreAndVariables(t *testing.T) {
	m, db := open(t)
	real := realTemp(t)
	alias, ok := aliasOf(t, real)
	if !ok {
		t.Skip("this process may not make a symlink here: the other spellings of a root are checked natively on Windows")
	}
	aliasFixture(t, m, real)
	if _, err := db.AddWorkspace(context.Background(), "kb", alias, nil, []string{".git/**", "tasks/done/**"}); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenAll(); err != nil {
		t.Fatal(err)
	}
	w, _ := m.Get("kb")
	if w.Root() != alias {
		t.Errorf("listed under %q; want the root as configured, %q", w.Root(), alias)
	}
	servedThroughAnAlias(t, m, "kb")
	if _, _, excluded := w.(*view).TodoModes(); len(excluded) != 0 {
		t.Errorf("excluded %v: tasks/done/** reaches no task of the store", excluded)
	}
	if err := m.SetPatterns(context.Background(), "kb", workspace.DefaultInclude(), []string{".git/**", "tasks/**"}); err != nil {
		t.Fatal(err)
	}
	w, _ = m.Get("kb")
	if _, _, excluded := w.(*view).TodoModes(); !slices.Equal(excluded, []string{"tasks/**"}) {
		t.Errorf("excluded %v; want tasks/**, which keeps the store out of the index", excluded)
	}
}

// TestARetargetedRootIsReadAgainAtTheNextStart: where a workspace's root leads is asked at each
// start, not kept from the first. A symlink configured as the root and then pointed at another
// directory serves, from its next start, that directory's store and variables: the modes and the
// index hold the same spelling again.
func TestARetargetedRootIsReadAgainAtTheNextStart(t *testing.T) {
	m, _ := open(t)
	first, second := realTemp(t), realTemp(t)
	alias, ok := aliasOf(t, first)
	if !ok {
		t.Skip("this process may not make a symlink here")
	}
	reg := aliasFixture(t, m, first)
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: alias}); err != nil {
		t.Fatal(err)
	}
	servedThroughAnAlias(t, m, "kb")

	// the link now leads to second, which holds the same files; the registry names the store and
	// the variables there, as the todo system would after resolving it
	writeAll(t, second, map[string]string{"notes/n.md": "---\nrelated: [$KB_ROOT/a/x.md]\n---\nn\n", "a/x.md": "# A\n",
		"tasks/open/t.md": "zanzibar task\n"})
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	reg.set(TodoStore{ID: "S", Dir: filepath.Join(second, "tasks"), KB: second})
	reg.setVars(map[string]map[string]string{second: {"KB_ROOT": second}})
	// a restart: the patterns set again start the workspace anew
	if err := m.SetPatterns(context.Background(), "kb", workspace.DefaultInclude(), []string{".git/**"}); err != nil {
		t.Fatal(err)
	}
	m.TodoStoresChanged()
	h := modesOf(t, m, "kb")
	if got := *h.root.Load(); got != paths.Slash(second) {
		t.Fatalf("after the restart the modes look under %q; want where the link leads now, %q", got, paths.Slash(second))
	}
	eventually(t, "the task is the second directory's store's", func() bool {
		md := h.mode("tasks/open/t.md")
		return md.Store != nil && md.Store.ID == "S" && md.Vars.Get("KB_ROOT") == second
	})
	w, _ := m.Get("kb")
	eventually(t, "the note's relation is the workspace's document again", func() bool {
		ls, err := w.Index().Store().Links(context.Background(), "notes/n.md", nil)
		return err == nil && len(ls) == 1 && ls[0].Resolved && ls[0].Path == "a/x.md"
	})
}

// modesOf are the served workspace name's modes.
func modesOf(t *testing.T, m *Workspaces, name string) *modes {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.served[name]
	if !ok || m.modes[s.id] == nil {
		t.Fatalf("%s is not served", name)
	}
	return m.modes[s.id]
}

// TestASiblingInAnotherCaseIsAnotherWorkspace: on a disk that tells kb from KB, a workspace at KB
// has none of kb's stores or variables: paths are compared exactly, where the disk says they are,
// never case-folded.
func TestASiblingInAnotherCaseIsAnotherWorkspace(t *testing.T) {
	m, _ := open(t)
	base := realTemp(t)
	lower, upper := filepath.Join(base, "kb"), filepath.Join(base, "KB")
	for _, d := range []string{lower, upper} {
		if err := os.Mkdir(d, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	a, err1 := os.Stat(lower)
	b, err2 := os.Stat(upper)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if os.SameFile(a, b) {
		t.Skip("this disk holds kb and KB as one folder: there is no sibling to tell apart")
	}
	aliasFixture(t, m, lower) // the registry: kb's store and variables
	writeAll(t, upper, map[string]string{"notes/n.md": "---\nrelated: [$KB_ROOT/a/x.md]\n---\nn\n", "a/x.md": "# A\n",
		"tasks/open/t.md": "zanzibar task\n"})
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "upper", Root: upper}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "upper", 3)
	if err := m.Configure(context.Background(), "upper", store.Changes{TodoSearch: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	h := modesOf(t, m, "upper")
	if md := h.mode("tasks/open/t.md"); md.Store != nil || md.Vars.Get("KB_ROOT") != "" || md.GraphOnly {
		t.Errorf("KB's task has kb's mode: store %+v, KB_ROOT %q, graph-only %v", md.Store, md.Vars.Get("KB_ROOT"), md.GraphOnly)
	}
	if got := hits(t, m, "upper", "zanzibar"); !slices.Equal(got, []string{"tasks/open/t.md"}) {
		t.Errorf("KB's task in search: %v; want it there, in no store", got)
	}
	w, _ := m.Get("upper")
	ls, err := w.Index().Store().Links(context.Background(), "notes/n.md", nil)
	// with no variables of its own, KB reads $KB_ROOT from its own root, as a workspace always
	// has; given kb's variables it would name kb's a/x.md, a file outside KB
	if err != nil || len(ls) != 1 || !ls[0].Resolved || ls[0].Path != "a/x.md" || ls[0].External != "" {
		t.Errorf("KB's note's $KB_ROOT relation %+v, %v; want KB's own a/x.md: KB has none of kb's variables", ls, err)
	}
}
