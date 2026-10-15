package index

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// modes is a test's Modes: graph-only by path prefix, and the todo stores by directory, each
// changeable while the indexer runs.
type modes struct {
	mu     sync.Mutex
	graph  []string              // path prefixes indexed graph-only
	stores map[string]*TodoStore // by directory prefix ("a/.todo-list/")
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

// TestATasksVariableRefs (ADR 1792015212 §2.4): a task's $VAR ref resolves through its store's
// variables: a path in the workspace is that document, one outside it an external link, which is
// not unresolved; an unknown variable is unresolved, never the workspace root. An absolute ref is
// read the same way. A document in no store keeps today's reading of $VAR, root-relative.
func TestATasksVariableRefs(t *testing.T) {
	m := &modes{stores: map[string]*TodoStore{"kb/.todo-list/": {ID: "S",
		Vars: map[string]string{"KB_ROOT": "/ws/kb", "PROJECT_ROOT": "/src/lm"}}}}
	e := newEnv(t, options{Modes: m.mode, Root: "/ws"})
	e.put("kb/adrs/a.md", "a", "kb/reviews/r.md", "r", "adrs/b.md", "b",
		"kb/.todo-list/open/t.md", "---\nadr:\n  - $KB_ROOT/adrs/a.md\n  - $PROJECT_ROOT/lm/x.md\n  - $NOPE/z.md\n"+
			"  - /ws/kb/adrs/a.md\n  - /elsewhere/q.md\nreview:\n  - $KB_ROOT/reviews/r.md\n---\nt\n",
		"notes/n.md", "---\nrelated: [$KB_ROOT/adrs/b.md]\n---\nn\n")
	eq(t, "the task's", e.kinds("kb/.todo-list/open/t.md"), []string{
		"adr $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
		"adr $PROJECT_ROOT/lm/x.md -> -",
		"adr $NOPE/z.md -> -",
		"adr /ws/kb/adrs/a.md -> kb/adrs/a.md",
		"adr /elsewhere/q.md -> -",
		"review $KB_ROOT/reviews/r.md -> kb/reviews/r.md",
	})
	ls, err := e.store.Links(context.Background(), "kb/.todo-list/open/t.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	var external []string
	for _, l := range ls {
		if l.External != "" {
			external = append(external, l.Raw+" => "+l.External)
		}
	}
	eq(t, "external", external, []string{"$PROJECT_ROOT/lm/x.md => /src/lm/lm/x.md", "/elsewhere/q.md => /elsewhere/q.md"})
	eq(t, "unresolved: the unknown variable alone", e.unresolved(), []string{"kb/.todo-list/open/t.md $NOPE/z.md missing"})
	eq(t, "a note in no store", e.kinds("notes/n.md"), []string{"related $KB_ROOT/adrs/b.md -> adrs/b.md"})

	// the store's variables changed: its tasks are indexed again, no file changed
	m.set(func(m *modes) {
		m.stores["kb/.todo-list/"] = &TodoStore{ID: "S", Vars: map[string]string{"KB_ROOT": "/ws/kb", "PROJECT_ROOT": "/ws",
			"NOPE": "/ws/kb"}}
	})
	e.put("lm/x.md", "x", "kb/z.md", "z")
	e.revalidate(func() bool {
		return strings.Join(e.kinds("kb/.todo-list/open/t.md"), "|") == strings.Join([]string{
			"adr $KB_ROOT/adrs/a.md -> kb/adrs/a.md",
			"adr $PROJECT_ROOT/lm/x.md -> lm/x.md",
			"adr $NOPE/z.md -> kb/z.md",
			"adr /ws/kb/adrs/a.md -> kb/adrs/a.md",
			"adr /elsewhere/q.md -> -",
			"review $KB_ROOT/reviews/r.md -> kb/reviews/r.md",
		}, "|")
	})
}
