package index

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// modes is a test's Modes: graph-only by path prefix, the todo stores by directory, and the
// workspaces' variables by the longest directory prefix ("" for every path), each changeable while
// the indexer runs.
type modes struct {
	mu     sync.Mutex
	graph  []string                     // path prefixes indexed graph-only
	stores map[string]*TodoStore        // by directory prefix ("a/.todo-list/")
	vars   map[string]map[string]string // by directory prefix
}

func (m *modes) mode(p string) Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	var md Mode
	for _, g := range m.graph {
		if strings.HasPrefix(p, g) {
			md.GraphOnly = true
		}
	}
	for dir, st := range m.stores {
		if strings.HasPrefix(p, dir) {
			md.Store = st
		}
	}
	best := -1
	for dir, vs := range m.vars {
		if strings.HasPrefix(p, dir) && len(dir) > best {
			best, md.Vars = len(dir), NewVars(vs)
		}
	}
	return md
}

func (m *modes) set(fn func(m *modes)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m)
}

// liveChunks is how many live chunks the document at p has.
func (e *env) liveChunks(p string) int {
	e.t.Helper()
	var n int
	if err := scanOne(context.Background(), e.raw, &n, `SELECT COUNT(*) FROM chunk c JOIN document d ON d.id = c.doc_id
		WHERE d.path = ? AND c.gen_to IS NULL`, p); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// revalidate re-indexes what the indexer would cut differently now, and waits for it.
func (e *env) revalidate(done func() bool) {
	e.t.Helper()
	if err := e.ix.Revalidate(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	e.eventually("revalidated", func() bool { return done() && e.pendingJobs() == 0 })
}

// TestAGraphOnlyDocumentIsInTheGraphNotInSearch (docs/decisions/todos.md#graph-only-indexing): a
// graph-only document has its row, frontmatter, links, relations and backlinks, and no chunk, so no
// search finds it. Its mode is part of what the indexer compares: a change of mode alone re-indexes
// it, its chunks added, or dead in the generation that flips.
func TestAGraphOnlyDocumentIsInTheGraphNotInSearch(t *testing.T) {
	m := &modes{graph: []string{".todo-list/"}}
	e := newEnv(t, options{Modes: m.mode})
	e.put(".todo-list/open/t.md", "---\ntitle: zanzibar task\nrelated: [[k]]\n---\nzanzibar lives here\n",
		"k.md", "# K\n\nsee [[t]]\n")

	if n := e.liveChunks(".todo-list/open/t.md"); n != 0 {
		t.Errorf("a graph-only document has %d live chunks, want none", n)
	}
	if hits := hitPaths(e.search("zanzibar", QueryOpts{})); len(hits) != 0 {
		t.Errorf("search found a graph-only document: %v", hits)
	}
	eq(t, "its relations", e.kinds(".todo-list/open/t.md"), []string{"related [[k]] -> k.md"})
	back, err := e.store.Backlinks(context.Background(), ".todo-list/open/t.md", nil)
	if err != nil || len(back) != 1 || back[0].Path != "k.md" {
		t.Errorf("its backlinks %+v, %v; want k.md's", back, err)
	}
	if fm, ok, err := e.store.Version(context.Background(), ".todo-list/open/t.md"); err != nil || !ok || fm == "" {
		t.Errorf("its document row: %v %v %v", fm, ok, err)
	}

	m.set(func(m *modes) { m.graph = nil })
	e.revalidate(func() bool { return len(hitPaths(e.search("zanzibar", QueryOpts{}))) == 1 })
	if n := e.liveChunks(".todo-list/open/t.md"); n == 0 {
		t.Error("full again, it has no chunk")
	}

	m.set(func(m *modes) { m.graph = []string{".todo-list/"} })
	e.revalidate(func() bool { return e.liveChunks(".todo-list/open/t.md") == 0 })
	if hits := hitPaths(e.search("zanzibar", QueryOpts{})); len(hits) != 0 {
		t.Errorf("graph-only again, search still finds it: %v", hits)
	}
	eq(t, "its relations stay", e.kinds(".todo-list/open/t.md"), []string{"related [[k]] -> k.md"})
	if hits := hitPaths(e.search("see", QueryOpts{})); len(hits) != 1 || hits[0] != "k.md" {
		t.Errorf("a full document beside it: %v", hits)
	}
}

// TestBlockedNamesATaskOfItsOwnStore (docs/decisions/todos.md#task-relations-and-external-links): a
// task answers to its store's name for it, and "blocked" looks the id up in the task's own store
// only, so another store's task of the same id never captures it. The link follows the blocker as
// it comes, moves and goes, the task that names it unchanged.
func TestBlockedNamesATaskOfItsOwnStore(t *testing.T) {
	m := &modes{stores: map[string]*TodoStore{"a/.todo-list/": {ID: "A"}, "b/.todo-list/": {ID: "B"}}}
	e := newEnv(t, options{Modes: m.mode})
	e.put("a/.todo-list/open/x.md", "---\nblocked: [y, \"waits for the release\"]\n---\nx\n",
		"b/.todo-list/open/y.md", "B's y")
	eq(t, "before A's y", e.kinds("a/.todo-list/open/x.md"), []string{"blocked y -> -"})
	e.put("a/.todo-list/open/y.md", "A's y")
	eq(t, "A's y came after x", e.kinds("a/.todo-list/open/x.md"), []string{"blocked y -> a/.todo-list/open/y.md"})
	e.remove("a/.todo-list/open/y.md")
	e.put("a/.todo-list/deferred/y.md", "A's y, deferred")
	eq(t, "moved to deferred", e.kinds("a/.todo-list/open/x.md"), []string{"blocked y -> a/.todo-list/deferred/y.md"})
	e.remove("a/.todo-list/deferred/y.md")
	eq(t, "removed: B's y is not A's", e.kinds("a/.todo-list/open/x.md"), []string{"blocked y -> -"})

	// a document in no store makes no blocked relation
	e.put("notes/n.md", "---\nblocked: [y]\n---\nn\n")
	eq(t, "a note's blocked", e.kinds("notes/n.md"), nil)
}

// TestEveryDocumentsVariableRefs (docs/decisions/todos.md#resolving-refs-in-every-document,
// generalizing docs/decisions/todos.md#task-relations-and-external-links): a
// document's $NAME ref, a relation or a markdown link, task or note, resolves through the variables
// of the workspace it belongs to: a path in the indexed workspace is that document, one outside it an
// external link, which is not unresolved; an unknown name is unresolved, never the workspace root.
// An absolute ref is read the same way. A nested workspace's document reads its own workspace's
// $KB_ROOT, not the indexing one's.
func TestEveryDocumentsVariableRefs(t *testing.T) {
	m := &modes{stores: map[string]*TodoStore{"kb/.todo-list/": {ID: "S"}},
		vars: map[string]map[string]string{"": {"KB_ROOT": "/ws"}, "kb/": {"KB_ROOT": "/ws/kb", "PROJECT_ROOT": "/src/lm"}}}
	e := newEnv(t, options{Modes: m.mode, Root: "/ws"})
	// z.md at the root: an unknown variable read as the root would reach it
	e.put("kb/adrs/a.md", "a", "kb/reviews/r.md", "r", "adrs/b.md", "b", "z.md", "the root's z",
		"kb/.todo-list/open/t.md", "---\nadr:\n  - $KB_ROOT/adrs/a.md\n  - $PROJECT_ROOT/lm/x.md\n  - $NOPE/z.md\n"+
			"  - /ws/kb/adrs/a.md\n  - /elsewhere/q.md\nreview:\n  - $KB_ROOT/reviews/r.md\n---\nt\n",
		"notes/n.md", "---\nrelated: [$KB_ROOT/adrs/b.md, $NOPE/z.md]\n---\nn\n",
		"kb/notes/m.md", "---\nrelated: [$KB_ROOT/adrs/a.md]\n---\nsee [x]($PROJECT_ROOT/lm/x.md) and [r]($KB_ROOT/reviews/r.md#top)\n")
	eq(t, "the task's", e.kinds("kb/.todo-list/open/t.md"), []string{
		"adr $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
		"adr $PROJECT_ROOT/lm/x.md -> -",
		"adr $NOPE/z.md -> -",
		"adr /ws/kb/adrs/a.md -> kb/adrs/a.md",
		"adr /elsewhere/q.md -> -",
		"review $KB_ROOT/reviews/r.md -> kb/reviews/r.md",
	})
	eq(t, "a note of the indexed workspace", e.kinds("notes/n.md"), []string{
		"related $KB_ROOT/adrs/b.md -> adrs/b.md",
		"related $NOPE/z.md -> -",
	})
	eq(t, "a note of the nested workspace", e.kinds("kb/notes/m.md"), []string{
		"markdown [x]($PROJECT_ROOT/lm/x.md) -> -",
		"markdown [r]($KB_ROOT/reviews/r.md#top) -> kb/reviews/r.md",
		"related $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
	})
	var external []string
	for _, p := range []string{"kb/.todo-list/open/t.md", "kb/notes/m.md"} {
		ls, err := e.store.Links(context.Background(), p, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range ls {
			if l.External != "" {
				external = append(external, l.Raw+" => "+l.External)
			}
		}
	}
	eq(t, "external", external, []string{"$PROJECT_ROOT/lm/x.md => /src/lm/lm/x.md", "/elsewhere/q.md => /elsewhere/q.md",
		"[x]($PROJECT_ROOT/lm/x.md) => /src/lm/lm/x.md"})
	eq(t, "unresolved: the unknown variable alone", e.unresolved(), []string{
		"kb/.todo-list/open/t.md $NOPE/z.md missing", "notes/n.md $NOPE/z.md missing"})

	// the workspace's variables changed: the documents naming a changed one are indexed again, no
	// file changed, and only they: kb/adrs/a.md names none, kb/notes/m.md names KB_ROOT and
	// PROJECT_ROOT, the task NOPE too, and the other workspace's note is in another workspace
	e.put("lm/x.md", "x", "kb/z.md", "z")
	m.set(func(m *modes) {
		m.vars["kb/"] = map[string]string{"KB_ROOT": "/ws/kb", "PROJECT_ROOT": "/ws", "NOPE": "/ws/kb"}
	})
	outdated, err := e.ix.outdated(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "outdated by the change", outdated, []string{"kb/.todo-list/open/t.md", "kb/notes/m.md"})
	e.revalidate(func() bool {
		return strings.Join(e.kinds("kb/.todo-list/open/t.md"), "|") == strings.Join([]string{
			"adr $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
			"adr $PROJECT_ROOT/lm/x.md -> lm/x.md",
			"adr $NOPE/z.md -> kb/z.md",
			"adr /ws/kb/adrs/a.md -> kb/adrs/a.md",
			"adr /elsewhere/q.md -> -",
			"review $KB_ROOT/reviews/r.md -> kb/reviews/r.md",
		}, "|") && strings.Join(e.kinds("kb/notes/m.md"), "|") == strings.Join([]string{
			"markdown [x]($PROJECT_ROOT/lm/x.md) -> lm/x.md",
			"markdown [r]($KB_ROOT/reviews/r.md#top) -> kb/reviews/r.md",
			"related $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
		}, "|")
	})
	// a variable no document names outdates none
	m.set(func(m *modes) { m.vars["kb/"]["SPECS"] = "/ws/specs" })
	if outdated, err := e.ix.outdated(context.Background()); err != nil || len(outdated) != 0 {
		t.Errorf("a variable no document names outdated %v %v", outdated, err)
	}
	// the other workspace's documents kept theirs
	eq(t, "the indexed workspace's note", e.kinds("notes/n.md"), []string{
		"related $KB_ROOT/adrs/b.md -> adrs/b.md",
		"related $NOPE/z.md -> -",
	})

	// with no variables given, a note's $NAME root is the workspace root, as before
	// docs/decisions/todos.md#variables-belong-to-the-workspace
	m.set(func(m *modes) { m.vars = nil })
	e.revalidate(func() bool {
		return strings.Join(e.kinds("notes/n.md"), "|") == "related $KB_ROOT/adrs/b.md -> adrs/b.md|related $NOPE/z.md -> z.md"
	})
}

// varLink reads a markdown link's $NAME destination through the variables: inside the root, the
// workspace path when the workspace takes that kind of file, else nothing; outside, an external
// link; an unknown name, unresolved. A destination that is no $NAME ref, or no variables, is the
// ordinary reading's (ok false).
func TestVarLink(t *testing.T) {
	vars := NewVars(map[string]string{"KB_ROOT": "/ws", "OUT": "/elsewhere"})
	md := func(p string) bool { return strings.HasSuffix(p, ".md") }
	for _, c := range []struct {
		dest string
		vars *Vars
		ok   bool
		want DocLink
	}{
		{"$KB_ROOT/a/b.md#Sec%20One", vars, true, DocLink{Raw: "r", Name: "a/b.md", Anchor: "Sec One", Kind: LinkMarkdown}},
		{"$OUT/x.md", vars, true, DocLink{Raw: "r", Name: externalPrefix + "/elsewhere/x.md", Kind: LinkMarkdown, Target: "/elsewhere/x.md"}},
		{"$NOPE/q.md", vars, true, DocLink{Raw: "r", Name: "$NOPE/q.md", Kind: LinkMarkdown}},
		{"$KB_ROOT/pic.png", vars, true, DocLink{}}, // a file the workspace does not take: left out
		{"$5.md", vars, false, DocLink{}},           // no variable name
		{"plain/a.md", vars, false, DocLink{}},
		{"$KB_ROOT/a.md", nil, false, DocLink{}}, // no variables: the ordinary reading
	} {
		got, ok := varLink("r", c.dest, c.vars, roots{"/ws"}, md)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v %v, want %+v %v", c.dest, got, ok, c.want, c.ok)
		}
	}
}

// TestARefIsInsideTheRootByWholeSegments: a $NAME or absolute ref is inside the workspace root when
// it lies under it by whole segments, in whichever form the root and the ref are written (the OS's
// own or the slash form), on every OS: a name beginning ".." is inside, a ".." segment that leaves
// the root and a sibling sharing the root's prefix are outside, and the root itself is no document.
// A path outside is an external link in the slash form.
func TestARefIsInsideTheRootByWholeSegments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kb") // the OS's own form
	sibling := filepath.Join(filepath.Dir(root), "kbx")
	for _, c := range []struct {
		abs  string
		rel  string
		want bool
	}{
		{filepath.Join(root, "a", "b.md"), "a/b.md", true},
		{paths.Slash(filepath.Join(root, "a", "b.md")), "a/b.md", true},
		{filepath.Join(root, "..x", "b.md"), "..x/b.md", true},
		{filepath.Join(root, "..", "b.md"), "", false},
		{filepath.Join(sibling, "b.md"), "", false},
		{paths.Slash(filepath.Join(sibling, "b.md")), "", false},
		{filepath.Dir(root), "", false},
		{root, "", false},
	} {
		for _, r := range []string{root, paths.Slash(root)} {
			if rel, ok := inside(r, c.abs); ok != c.want || rel != c.rel {
				t.Errorf("inside(%q, %q) = %q, %v; want %q, %v", r, c.abs, rel, ok, c.rel, c.want)
			}
		}
	}
	if _, ok := inside("", filepath.Join(root, "a.md")); ok {
		t.Error("no root holds a path")
	}

	vars := NewVars(map[string]string{"KB_ROOT": root, "OUT": sibling})
	md := func(p string) bool { return strings.HasSuffix(p, ".md") }
	out := paths.Join(paths.Slash(sibling), "x.md")
	for dest, want := range map[string]DocLink{
		"$KB_ROOT/a/b.md":      {Raw: "r", Name: "a/b.md", Kind: LinkMarkdown},
		"$KB_ROOT/..x/b.md":    {Raw: "r", Name: "..x/b.md", Kind: LinkMarkdown},
		"$OUT/x.md":            {Raw: "r", Name: externalPrefix + out, Kind: LinkMarkdown, Target: out},
		"$KB_ROOT/../kbx/x.md": {Raw: "r", Name: externalPrefix + out, Kind: LinkMarkdown, Target: out},
	} {
		if got, ok := varLink("r", dest, vars, options{Root: root}.roots(), md); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v %v, want %+v", dest, got, ok, want)
		}
	}
}

// TestATaskIsCheckedByItsStoresFormat
// (docs/decisions/todos.md#tasks-are-checked-by-the-todo-format): a task in a store is checked
// by the store's Check, the todo format's, never by the workspace's KB schema, so it has none of
// the KB's diagnostics and the format's facets. A file beside the store keeps the KB schema's. A
// store whose Check comes later indexes its unchanged task again, now checked by it.
// ValidateDocument answers what the index holds, for both.
func TestATaskIsCheckedByItsStoresFormat(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	format := func([]byte) schema.Result {
		return schema.Result{Facets: []schema.Facet{{Field: "status", Value: "open"}},
			Diagnostics: []schema.Diagnostic{{Field: "adr[0]", Line: 1, Rule: "todo-not-found", Message: "adr[0]: no such file"}}}
	}
	m := &modes{stores: map[string]*TodoStore{".todo-list/": {ID: "S"}}} // no Check yet: the KB schema's, as before
	e := newEnv(t, options{Schema: sv.get, Modes: m.mode})
	task, beside := "---\nid: t\nstatus: open\n---\nt\n", "---\nstatus: draft\n---\nn\n"
	e.write(".todo-list/open/t.md", task)
	e.write("n.md", beside)
	for _, p := range []string{".todo-list/open/t.md", "n.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	ctx := context.Background()
	if d, _ := e.store.Diagnostics(ctx, ".todo-list/open/t.md"); !hasRule(d, "type", schema.RuleRequired) {
		t.Fatalf("a store with no Check: the task's diagnostics %+v, want the KB schema's (type required among them)", d)
	}

	m.set(func(m *modes) { m.stores[".todo-list/"] = &TodoStore{ID: "S", Check: format} }) // a new store: a mode is read off the lock
	e.revalidate(func() bool {
		d, _ := e.store.Diagnostics(ctx, ".todo-list/open/t.md")
		return len(d) == 1 && d[0].Rule == "todo-not-found"
	})
	d, _ := e.store.Diagnostics(ctx, ".todo-list/open/t.md")
	if len(d) != 1 || d[0].Field != "adr[0]" || d[0].Rule != "todo-not-found" {
		t.Errorf("the task's diagnostics: %+v, want the format's alone", d)
	}
	if got, want := e.facets(".todo-list/open/t.md"), []string{"status=open"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the task's facets: %v, want the format's %v", got, want)
	}
	if d, _ := e.store.Diagnostics(ctx, "n.md"); len(d) != 1 || d[0].Rule != schema.RuleRequired {
		t.Errorf("the file beside the store: %+v, want the KB schema's (type required)", d)
	}
	kb, _ := sv.get()
	if r := e.ix.ValidateDocument(".todo-list/open/t.md", []byte(task), kb); len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != "todo-not-found" {
		t.Errorf("ValidateDocument of the task: %+v, want the format's", r.Diagnostics)
	}
	if r := e.ix.ValidateDocument("n.md", []byte(beside), kb); len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != schema.RuleRequired {
		t.Errorf("ValidateDocument of the file beside it: %+v, want the KB schema's", r.Diagnostics)
	}
}

// hasRule reports a diagnostic of rule on field among ds.
func hasRule(ds []schema.Diagnostic, field, rule string) bool {
	for _, d := range ds {
		if d.Field == field && d.Rule == rule {
			return true
		}
	}
	return false
}

// TestAModeChangedWhileATaskIsPreparedReachesItsRow: a task prepared under one mode (no todo store
// yet: the KB schema judges it) whose store is registered, and the index revalidated, before its row
// is written must not keep the old judgement: that revalidation could not see a row not yet
// written, so the commit looks again (modeMoved) and the task is indexed again, checked by the
// store's format (the daemon's registration race: a todos.add's store is published while the
// task's first index is in flight).
func TestAModeChangedWhileATaskIsPreparedReachesItsRow(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	format := func([]byte) schema.Result {
		return schema.Result{Diagnostics: []schema.Diagnostic{{Field: "adr[0]", Line: 1, Rule: "todo-not-found", Message: "adr[0]: no such file"}}}
	}
	m := &modes{stores: map[string]*TodoStore{}} // no store yet
	const task = ".todo-list/open/t.md"
	var once sync.Once
	var e *env
	e = newEnv(t, options{Schema: sv.get, Modes: m.mode, afterRead: func(p string) {
		if p != task {
			return
		}
		once.Do(func() { // its mode is read: the store is registered and revalidated before its row is
			m.set(func(m *modes) { m.stores[".todo-list/"] = &TodoStore{ID: "S", Check: format} })
			if err := e.ix.Revalidate(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}})
	e.write(task, "---\nid: t\nstatus: open\n---\nt\n")
	e.ix.Touch(task)
	ctx := context.Background()
	e.indexing("the task checked by its store's format", func() bool {
		d, _ := e.store.Diagnostics(ctx, task)
		return len(d) == 1 && d[0].Rule == "todo-not-found" && e.pendingJobs() == 0
	})
	if id := e.indexerOf(task); !strings.Contains(id, ";todo=S;fm=todo") {
		t.Errorf("the task's indexer identity %q: want the store's mode", id)
	}
}

// TestARefUnderEitherSpellingOfTheRootIsTheWorkspaces: the index compares a ref with the workspace's
// root as configured and with where it leads on the disk, each exactly and by whole segments. A ref
// under either is the workspace's; one spelled a third way, in another case, or under a sibling
// sharing a prefix is not; and where both spellings hold a ref (the configured root lies inside the
// folder it leads to) the longest decides the document it names.
func TestARefUnderEitherSpellingOfTheRootIsTheWorkspaces(t *testing.T) {
	base := t.TempDir()
	canon, alias := filepath.Join(base, "real", "kb"), filepath.Join(base, "link")
	nested := filepath.Join(canon, "alias") // configured inside the folder it leads to
	for _, c := range []struct {
		name              string
		configured, canon string
		abs, rel          string
		in                bool
	}{
		{"under where the root leads", alias, canon, filepath.Join(canon, "a", "b.md"), "a/b.md", true},
		{"under the root as configured", alias, canon, filepath.Join(alias, "a", "b.md"), "a/b.md", true},
		{"either, in the slash form", alias, canon, paths.Slash(filepath.Join(alias, "a", "b.md")), "a/b.md", true},
		{"a third spelling", alias, canon, filepath.Join(base, "other", "a.md"), "", false},
		{"another case of where it leads", alias, canon, filepath.Join(base, "real", "KB", "a.md"), "", false},
		{"another case of the configured root", alias, canon, filepath.Join(base, "LINK", "a.md"), "", false},
		{"a sibling sharing the configured root's prefix", alias, canon, filepath.Join(base, "linkx", "a.md"), "", false},
		{"a sibling sharing the canonical root's prefix", alias, canon, filepath.Join(base, "real", "kbx", "a.md"), "", false},
		{"both hold it: the longest decides", nested, canon, filepath.Join(nested, "x.md"), "x.md", true},
		{"both given, the shorter's own document", nested, canon, filepath.Join(canon, "x.md"), "x.md", true},
		{"one spelling given twice", canon, canon, filepath.Join(canon, "a.md"), "a.md", true},
		{"no canonical root: the configured one alone", alias, "", filepath.Join(alias, "a.md"), "a.md", true},
		{"no canonical root: where it leads is not known", alias, "", filepath.Join(canon, "a.md"), "", false},
		{"no root at all", "", "", filepath.Join(canon, "a.md"), "", false},
	} {
		r := options{Root: c.configured, Canon: c.canon}.roots()
		if rel, ok := r.inside(c.abs); ok != c.in || rel != c.rel {
			t.Errorf("%s: inside(%q) under %q = %q, %v; want %q, %v", c.name, c.abs, []string(r), rel, ok, c.rel, c.in)
		}
	}
	// a workspace's variable carries where the root leads: its ref is the workspace's only when the
	// index was given that spelling
	md := func(p string) bool { return strings.HasSuffix(p, ".md") }
	vars := NewVars(map[string]string{"KB_ROOT": canon})
	want := DocLink{Raw: "r", Name: "a/b.md", Kind: LinkMarkdown}
	if got, ok := varLink("r", "$KB_ROOT/a/b.md", vars, options{Root: alias, Canon: canon}.roots(), md); !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("a variable's ref, the index given both spellings: %+v %v; want %+v", got, ok, want)
	}
	abs := paths.Join(paths.Slash(canon), "a/b.md")
	ext := DocLink{Raw: "r", Name: externalPrefix + abs, Kind: LinkMarkdown, Target: abs}
	if got, ok := varLink("r", "$KB_ROOT/a/b.md", vars, options{Root: alias}.roots(), md); !ok || !reflect.DeepEqual(got, ext) {
		t.Errorf("a variable's ref, the index given the configured root alone: %+v %v; want the external link %+v", got, ok, ext)
	}
}

// TestRefsResolveUnderARootConfiguredThroughAnAlias: an indexer whose workspace was configured
// through another spelling of its root (WithRoot) and told where that leads (WithCanonicalRoot)
// reads a $KB_ROOT ref, whose value is the canonical spelling, and an absolute ref in either
// spelling as the workspace's documents; a ref in another case is an external link.
func TestRefsResolveUnderARootConfiguredThroughAnAlias(t *testing.T) {
	m := &modes{vars: map[string]map[string]string{"": {"KB_ROOT": "/real/ws"}}}
	e := newEnv(t, options{Modes: m.mode, Root: "/link", Canon: "/real/ws"})
	e.put("adrs/a.md", "a", "notes/n.md",
		"---\nrelated: [$KB_ROOT/adrs/a.md, /real/ws/adrs/a.md, /link/adrs/a.md, /LINK/adrs/a.md]\n---\nsee [a]($KB_ROOT/adrs/a.md)\n")
	eq(t, "the note's", e.kinds("notes/n.md"), []string{
		"markdown [a]($KB_ROOT/adrs/a.md) -> adrs/a.md",
		"related $KB_ROOT/adrs/a.md -> adrs/a.md",
		"related /real/ws/adrs/a.md -> adrs/a.md",
		"related /link/adrs/a.md -> adrs/a.md",
		"related /LINK/adrs/a.md -> -",
	})
	ls, err := e.store.Links(context.Background(), "notes/n.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	var external []string
	for _, l := range ls {
		if l.External != "" {
			external = append(external, l.Raw+" => "+l.External)
		}
	}
	eq(t, "external", external, []string{"/LINK/adrs/a.md => /LINK/adrs/a.md"})
}

// TestAnOlderRelationsReadingIsReadAgainOnStart: a Markdown document indexed under an older
// relations reading (document.indexer's ".rN") is read again when the indexer next starts, its
// file unchanged: the links the older reading left are replaced by this reading's, and its chunks,
// unchanged, keep their rows, so nothing is embedded again. A document already under this reading
// is not parsed.
func TestAnOlderRelationsReadingIsReadAgainOnStart(t *testing.T) {
	// with variables given, a relation's absolute ref is read against the root
	m := &modes{vars: map[string]map[string]string{"": {"KB_ROOT": "/ws"}}}
	e := newEnv(t, options{Modes: m.mode, Root: "/ws"})
	e.put("b.md", "# B\n\nbody\n", "a.md", "---\nrelated: [/ws/b.md, /elsewhere/q.md]\n---\n# A\n\nsee [[b]]\n")
	want := e.kinds("a.md")
	if len(want) != 3 {
		t.Fatalf("a.md's links before: %v; want the wikilink and two relations", want)
	}
	cur := e.indexerOf("a.md")
	old := strings.Replace(cur, "."+relationsVersion, ".r1", 1)
	if old == cur || relationsVersion == "r1" {
		t.Fatalf("a.md's identity %q names no relations reading newer than r1", cur)
	}
	_, before := e.alive("a.md")
	embeds := e.embedsOf("a.md")
	e.stop()
	// what an index written by the older reading holds: its identity, and link rows this reading
	// would not write
	e.exec("UPDATE document SET indexer = ? WHERE path = 'a.md'", old)
	e.exec("UPDATE link SET name = 'left-by-r1', dst_doc = NULL, target = 'C:\\left\\by\\r1' WHERE src_doc = (SELECT id FROM document WHERE path = 'a.md')")
	var left int
	if err := scanOne(context.Background(), e.raw, &left, "SELECT COUNT(*) FROM link WHERE name = 'left-by-r1' AND gen_to IS NULL"); err != nil || left != len(want) {
		t.Fatalf("the fixture: %d live links rewritten as the older reading's, %v; want %d", left, err, len(want))
	}
	e.stop = nil
	e.open(options{Modes: m.mode, Root: "/ws"})
	e.eventually("a.md read again under this reading", func() bool { return e.indexerOf("a.md") == cur && e.pendingJobs() == 0 })
	eq(t, "a.md's links, read again", e.kinds("a.md"), want)
	if got := e.ix.Parses(); got != 1 {
		t.Errorf("%d parses on the start: want 1, a.md alone (b.md is under this reading)", got)
	}
	_, after := e.alive("a.md")
	if len(after) == 0 || len(after) != len(before) {
		t.Fatalf("a.md's chunks before %+v, after %+v", before, after)
	}
	for i := range after {
		if after[i].genFrom != before[i].genFrom || after[i].body != before[i].body {
			t.Errorf("chunk %d before %+v, after %+v: an unchanged chunk keeps its row", i, before[i], after[i])
		}
	}
	if got := e.embedsOf("a.md"); !reflect.DeepEqual(got, embeds) {
		t.Errorf("a.md's chunk embeddings before %v, after %v: nothing is embedded again", embeds, got)
	}
}
