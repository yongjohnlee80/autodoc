package daemon

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TestAPathsModeIsTheFirstRuleItMatches (docs/decisions/todos.md#which-paths-are-graph-only): a
// user's graph_only pattern makes a path graph-only whatever todo_search says; a todo store's path,
// by the registry or the default entry, is graph-only unless todo_search is on; anything else is
// full. A store path has its store in every mode.
func TestAPathsModeIsTheFirstRuleItMatches(t *testing.T) {
	m, _ := open(t)
	m.stores.Store(newRegistry(TodoRegistry{Stores: []TodoStore{{ID: "S", Dir: "/ws/tasks", KB: "/ws"}},
		Vars: map[string]map[string]string{"/ws": {"KB_ROOT": "/ws"}}}))
	h := &modes{m: m, root: "/ws"}
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
	root := t.TempDir()
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
	root := t.TempDir()
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
	root := t.TempDir()
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
