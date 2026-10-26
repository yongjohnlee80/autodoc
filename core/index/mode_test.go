package index

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

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

// TestAGraphOnlyDocumentIsInTheGraphNotInSearch (ADR 1792015212 §2.1): a graph-only document has
// its row, frontmatter, links, relations and backlinks, and no chunk, so no search finds it. Its
// mode is part of what the indexer compares: a change of mode alone re-indexes it, its chunks
// added, or dead in the generation that flips.
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

// TestBlockedNamesATaskOfItsOwnStore (ADR 1792015212 §2.4): a task answers to its store's name for
// it, and "blocked" looks the id up in the task's own store only, so another store's task of the
// same id never captures it. The link follows the blocker as it comes, moves and goes, the task
// that names it unchanged.
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

// TestEveryDocumentsVariableRefs (ADR 1792132105 §2.5, generalizing ADR 1792015212 §2.4): a
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

	// with no variables given, a note's $NAME root is the workspace root, as before ADR 1792132105
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
		got, ok := varLink("r", c.dest, c.vars, "/ws", md)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v %v, want %+v %v", c.dest, got, ok, c.want, c.ok)
		}
	}
}

// TestATaskIsCheckedByItsStoresFormat (ADR 1792015212 amendment 2): a task in a store is checked
// by the store's Check, the todo format's, never by the workspace's KB schema, so it has none of
// the KB's diagnostics and the format's facets. A file beside the store keeps the KB schema's. A
// store whose Check comes later indexes its unchanged task again, now checked by it.
// ValidateFrontmatter answers what the index holds, for both.
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
	if r := e.ix.ValidateFrontmatter(".todo-list/open/t.md", []byte(task), kb); len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != "todo-not-found" {
		t.Errorf("ValidateFrontmatter of the task: %+v, want the format's", r.Diagnostics)
	}
	if r := e.ix.ValidateFrontmatter("n.md", []byte(beside), kb); len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != schema.RuleRequired {
		t.Errorf("ValidateFrontmatter of the file beside it: %+v, want the KB schema's", r.Diagnostics)
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
