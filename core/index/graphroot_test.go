package index

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/graph"
)

// oracleRooted is a rooted graph.map as it was answered before AP-6: golib's Neighborhood over the
// workspace's whole graph of the given kinds (here, the unrooted map's, which reads it whole), the
// documents it reaches and every edge it keeps.
func oracleRooted(t *testing.T, s *Store, root string, depth int, kinds []string) Map {
	t.Helper()
	all, err := s.Map(context.Background(), MapOpts{Kinds: kinds})
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New[string]()
	byPath := map[string]MapNode{}
	for _, n := range all.Nodes {
		g.AddNode(n.Path)
		byPath[n.Path] = n
	}
	for _, e := range all.Edges {
		g.Add(graph.Edge[string]{Src: e.Src, Dst: e.Dst, Kind: e.Kind})
	}
	sub := g.Neighborhood(root, min(max(depth, 1), 2), graph.Both, graph.Filter{})
	var m Map
	for p := range sub.Nodes() {
		m.Nodes = append(m.Nodes, byPath[p])
	}
	for e := range sub.Edges(graph.Filter{}) {
		m.Edges = append(m.Edges, Edge{e.Src, e.Dst, e.Kind})
	}
	sortMap(&m)
	return m
}

func sortMap(m *Map) {
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Path < m.Nodes[j].Path })
	sort.Slice(m.Edges, func(i, j int) bool {
		a, b := m.Edges[i], m.Edges[j]
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		return a.Kind < b.Kind
	})
}

// TestARootedMapIsTheNeighbourhoodOfTheWholeGraph (AP-6): over a generated workspace of linked,
// tagged and superseding documents, a rooted map at every depth and kinds filter is exactly the
// neighbourhood of its root in the workspace's whole graph: its documents (with their titles, types,
// tags and supersession) and its links.
func TestARootedMapIsTheNeighbourhoodOfTheWholeGraph(t *testing.T) {
	e := newEnv(t, options{})
	r := rand.New(rand.NewPCG(7, 9))
	const n = 40
	var files []string
	for i := range n {
		var front, body []string
		if r.IntN(3) == 0 {
			front = append(front, fmt.Sprintf("supersedes: [d%02d.md]", r.IntN(n)))
		}
		if r.IntN(4) == 0 {
			front = append(front, fmt.Sprintf("superseded_by: [d%02d.md]", r.IntN(n)))
		}
		if r.IntN(2) == 0 {
			front = append(front, fmt.Sprintf("tags: [t%d, u%d]", r.IntN(4), r.IntN(3)))
		}
		front = append(front, fmt.Sprintf("type: k%d", r.IntN(3)))
		for range r.IntN(4) {
			body = append(body, fmt.Sprintf("[[d%02d]]", r.IntN(n)))
		}
		if r.IntN(5) == 0 {
			body = append(body, fmt.Sprintf("[[d%02d]] [[d%02d]]", i, i)) // a link to itself, twice
		}
		files = append(files, fmt.Sprintf("d%02d.md", i),
			"---\n"+strings.Join(front, "\n")+"\n---\n# D\n\n"+strings.Join(body, " ")+"\n")
	}
	e.put(files...)
	ctx := context.Background()
	for i := 0; i < n; i += 3 {
		root := fmt.Sprintf("d%02d.md", i)
		for _, depth := range []int{0, 1, 2, 5} {
			for _, kinds := range [][]string{nil, {LinkWikilink}, {"supersedes", "superseded_by"}} {
				got, err := e.store.Map(ctx, MapOpts{Root: root, Depth: depth, Kinds: kinds})
				if err != nil {
					t.Fatal(err)
				}
				if want := oracleRooted(t, e.store, root, depth, kinds); !reflect.DeepEqual(got, want) {
					t.Fatalf("%s depth %d kinds %v:\n got  %+v\n want %+v", root, depth, kinds, got, want)
				}
			}
		}
	}
}

// wholeReads records the statements that read the link, tag or document table with no IN list (or
// equality, for a list of one) on the column a neighbourhood narrows it by: a read of the table
// whole.
type wholeReads struct {
	dao.NopHook
	mu    sync.Mutex
	whole []string
}

func (c *wholeReads) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	narrowed := map[string][]string{
		"link":     {`"link"."src_doc" IN (`, `"link"."dst_doc" IN (`, `"link"."src_doc" = `, `"link"."dst_doc" = `},
		"doc_tag":  {`"doc_tag"."doc_id" IN (`, `"doc_tag"."doc_id" = `},
		"document": {`"document"."id" IN (`, `"document"."id" = `, `"document"."path" = `},
	}
	cols, ok := narrowed[q.Table]
	if !ok || (q.Op != dao.OpSelect && q.Op != dao.OpGet) {
		return nil
	}
	for _, c := range cols {
		if strings.Contains(q.SQL, c) {
			return nil
		}
	}
	c.mu.Lock()
	c.whole = append(c.whole, q.SQL)
	c.mu.Unlock()
	return nil
}

// TestARootedMapReadsOnlyItsNeighbourhood (AP-6): a rooted map, and graph.neighborhood through it,
// reads the links of its frontier, and the rows, tags and supersession of the documents it reached,
// never the workspace's every link, tag or document.
func TestARootedMapReadsOnlyItsNeighbourhood(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "---\ntags: [x]\nsupersedes: [b.md]\n---\n[[b]]\n", "b.md", "[[c]]", "c.md", "end",
		"far1.md", "---\ntags: [y]\n---\n[[far2]]", "far2.md", "[[far1]]")
	c := &wholeReads{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(c)}
	if _, err := s.Map(context.Background(), MapOpts{Root: "a.md", Depth: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Neighborhood(context.Background(), "a.md", 1, nil); err != nil {
		t.Fatal(err)
	}
	if len(c.whole) != 0 {
		t.Errorf("a rooted map read a table whole:\n%s", strings.Join(c.whole, "\n"))
	}
}

// TestARootedMapSeeksItsFrontiersLinks (AP-6): under no ANALYZE statistics, as in every store, each
// link statement of a rooted map is planned as a seek of its own key: link_src for a source list,
// link_dst for a destination list. A range over link_dst's resolved links filtered by source (what
// a "dst_doc IS NOT NULL" predicate made SQLite choose) reads the workspace's links, though the SQL
// names a frontier.
func TestARootedMapSeeksItsFrontiersLinks(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "---\nsupersedes: [b.md]\nsuperseded_by: [c.md]\n---\n[[b]] [[nowhere]]\n", "b.md", "[[c]]", "c.md", "end",
		"far1.md", "[[far2]]", "far2.md", "[[far1]] [[missing]]")
	capture := &sqlCapture{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(capture)}
	if _, err := s.Map(context.Background(), MapOpts{Root: "a.md", Depth: 2}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, q := range capture.stmts {
		if q.Table != "link" {
			continue
		}
		var want string
		switch {
		case strings.Contains(q.SQL, `"link"."src_doc" IN (`) || strings.Contains(q.SQL, `"link"."src_doc" = `):
			want = "USING INDEX link_src (workspace_id=? AND src_doc=?)"
		case strings.Contains(q.SQL, `"link"."dst_doc" IN (`) || strings.Contains(q.SQL, `"link"."dst_doc" = `):
			want = "USING INDEX link_dst (workspace_id=? AND dst_doc=?)"
		default:
			t.Errorf("a link statement names no frontier: %s", q.SQL)
			continue
		}
		seen[want] = true
		rows, err := e.raw.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q.SQL, q.Args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if all := strings.Join(plan, "\n"); !strings.Contains(all, want) {
			t.Errorf("planned otherwise than %q:\n%s\n%s", want, q.SQL, all)
		}
	}
	if len(seen) != 2 {
		t.Errorf("the rooted map's link statements sought %v; want both a source and a destination seek", seen)
	}
}

// failingSQL fails every statement whose SQL holds match, with errGraphRead.
type failingSQL struct {
	dao.NopHook
	match string
}

var errGraphRead = errors.New("the store failed")

func (f failingSQL) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	if strings.Contains(q.SQL, f.match) {
		return errGraphRead
	}
	return nil
}

// TestARootedMapWhoseReadFailsSaysSo: a rooted map whose frontier, link, document, tag or
// supersession read fails returns that failure, never a neighbourhood with a part missing.
func TestARootedMapWhoseReadFailsSaysSo(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "---\ntags: [x]\nsupersedes: [b.md]\n---\n[[b]]\n", "b.md", "[[c]]", "c.md", "end")
	for _, c := range []struct{ name, match string }{
		{"the frontier's outgoing links", `SELECT "link"."dst_doc" FROM "link"`},
		{"the frontier's incoming links", `SELECT "link"."src_doc" FROM "link"`},
		{"the links among the reached", `SELECT "link"."src_doc", "link"."dst_doc", "link"."kind" FROM "link"`},
		{"the documents reached", `"document"."id" IN (`},
		{"the supersession links", `"link"."kind" IN (`},
		{"the tags", `FROM "doc_tag"`},
	} {
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(failingSQL{match: c.match})}
		if _, err := s.Map(context.Background(), MapOpts{Root: "a.md", Depth: 2}); !errors.Is(err, errGraphRead) {
			t.Errorf("%s failed: the map said %v, want the failure", c.name, err)
		}
	}
}
