package index

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// docs/decisions/search.md#unrooted-maps-in-pages: an unrooted map a page at a time, against the
// one-reply map (Map) as its oracle.

// pagesEnv is a workspace whose links cross page boundaries both ways, of every kind: wikilinks
// forward and back, a markdown link, a link repeated, a document naming itself, a link to nothing,
// related, and supersession both ways; tags and a type.
func pagesEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, options{})
	e.put(
		"a.md", "[[j]] [[b]] [[b]] [[a]] [[nowhere]] [e](e.md)\n",
		"b.md", "---\nrelated: [i.md]\ntags: [T1]\n---\n[[a]]\n",
		"c.md", "---\nsupersedes: [d.md]\n---\nc\n",
		"d.md", "---\nsuperseded_by: [c.md]\n---\nd\n",
		"e.md", "[[c]]\n",
		"f.md", "plain\n",
		"g.md", "[[a]] [[j]]\n",
		"h.md", "---\ntype: adr\n---\n[[g]]\n",
		"i.md", "[[b]] #T2\n",
		"j.md", "---\nrelated: [h.md]\n---\n[[a]]\n",
	)
	return e
}

// mapKinds are the kinds filters each cell crosses: every kind, one, several, supersession, none.
var mapKinds = map[string][]string{
	"every kind":    nil,
	"wikilink":      {"wikilink"},
	"markdown":      {"markdown"},
	"related":       {"related"},
	"wiki, related": {"related", "wikilink"},
	"supersession":  {"supersedes", "superseded_by"},
	"an empty list": {},
}

// allPages reads every page of the unrooted map at limit, failing on a page over the limit, a page
// short of it while more follow, or a next that never ends. It answers the pages joined, and the
// number of pages.
func allPages(t *testing.T, s *Store, kinds []string, limit int) (Map, int) {
	t.Helper()
	var all Map
	o := MapPageOpts{Kinds: kinds, Limit: limit}
	for pages := 1; ; pages++ {
		if pages > 10000 {
			t.Fatalf("kinds %v, limit %d: no end", kinds, limit)
		}
		p, err := s.MapPage(context.Background(), o)
		if err != nil {
			t.Fatalf("kinds %v, limit %d: %v", kinds, limit, err)
		}
		if lim := min(max(limit, 1), MaxMapPage); len(p.Nodes) > lim || (p.Next != "" && len(p.Nodes) != lim) {
			t.Fatalf("kinds %v, limit %d: a page of %d, next %q", kinds, limit, len(p.Nodes), p.Next)
		}
		all.Nodes = append(all.Nodes, p.Nodes...)
		all.Edges = append(all.Edges, p.Edges...)
		if p.Next == "" {
			return all, pages
		}
		o.After = p.Next
	}
}

// TestAPagedMapIsTheOneReplyMap (§5.2): paged to the end at limits 1, 7, 500 and the most, the
// pages' nodes and links joined are the one-reply map's, in its order, for every kinds filter.
func TestAPagedMapIsTheOneReplyMap(t *testing.T) {
	e := pagesEnv(t)
	for name, kinds := range mapKinds {
		want, err := e.store.Map(context.Background(), MapOpts{Kinds: kinds})
		if err != nil {
			t.Fatal(err)
		}
		if name == "every kind" && (len(want.Nodes) != 10 || len(want.Edges) < 12) {
			t.Fatalf("the workspace: %d nodes, %d links: %v", len(want.Nodes), len(want.Edges), want.Edges)
		}
		for _, limit := range []int{1, 7, 500, MaxMapPage} {
			got, pages := allPages(t, e.store, kinds, limit)
			if !reflect.DeepEqual(got.Nodes, want.Nodes) || !sameEdges(got.Edges, want.Edges) {
				t.Errorf("%s, limit %d (%d pages):\nnodes %v\nwant  %v\nedges %v\nwant  %v", name, limit, pages,
					got.Nodes, want.Nodes, got.Edges, want.Edges)
			}
			if limit == 1 && pages != len(want.Nodes) {
				t.Errorf("%s: %d pages at limit 1 for %d documents", name, pages, len(want.Nodes))
			}
		}
	}
	// links cross pages both ways: at limit 1, a.md's page names j.md before it came, and j.md's
	// names a.md after
	p, err := e.store.MapPage(context.Background(), MapPageOpts{Limit: 1})
	if err != nil || len(p.Nodes) != 1 || p.Nodes[0].Path != "a.md" || !slicesContains(p.Edges, Edge{"a.md", "j.md", "wikilink"}) {
		t.Errorf("the first page: %+v, %v", p, err)
	}
}

func slicesContains(es []Edge, e Edge) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}

// sameEdges is a and b equal, none and an empty list alike.
func sameEdges(a, b []Edge) bool { return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b) }

// TestEachLinkComesOnce (§5.3): across a whole paged map, no link comes twice (a.md links b.md
// twice: one edge, as the one-reply map has it) and every link comes, on its source's page.
func TestEachLinkComesOnce(t *testing.T) {
	e := pagesEnv(t)
	want, err := e.store.Map(context.Background(), MapOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 3} {
		seen := map[Edge]int{}
		o := MapPageOpts{Limit: limit}
		for {
			p, err := e.store.MapPage(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			on := map[string]bool{}
			for _, n := range p.Nodes {
				on[n.Path] = true
			}
			for _, ed := range p.Edges {
				seen[ed]++
				if !on[ed.Src] {
					t.Errorf("limit %d: %v came on a page without its source", limit, ed)
				}
			}
			if p.Next == "" {
				break
			}
			o.After = p.Next
		}
		for _, ed := range want.Edges {
			if seen[ed] != 1 {
				t.Errorf("limit %d: %v came %d times", limit, ed, seen[ed])
			}
		}
		if len(seen) != len(want.Edges) {
			t.Errorf("limit %d: %d distinct links, the map has %d", limit, len(seen), len(want.Edges))
		}
	}
}

// TestAMapCursorIsForItsOwnKinds (§5.4): a cursor from other kinds (every kind and a list differ, as
// do two lists) is refused, as is one not a cursor; the same kinds in another order are the same
// map; no after starts it.
func TestAMapCursorIsForItsOwnKinds(t *testing.T) {
	e := pagesEnv(t)
	ctx := context.Background()
	first, err := e.store.MapPage(ctx, MapPageOpts{Kinds: []string{"wikilink", "related"}, Limit: 2})
	if err != nil || first.Next == "" || first.Nodes[0].Path != "a.md" {
		t.Fatalf("a first page: %+v, %v", first, err)
	}
	for name, o := range map[string]MapPageOpts{
		"every kind":      {After: first.Next},
		"another list":    {Kinds: []string{"wikilink"}, After: first.Next},
		"an empty list":   {Kinds: []string{}, After: first.Next},
		"not a cursor":    {Kinds: []string{"wikilink", "related"}, After: "c.md"},
		"a broken cursor": {Kinds: []string{"wikilink", "related"}, After: mapCursorPrefix + "!!"},
	} {
		if _, err := e.store.MapPage(ctx, o); !errors.Is(err, ErrBadMapCursor) {
			t.Errorf("%s: %v, want ErrBadMapCursor", name, err)
		}
	}
	same, err := e.store.MapPage(ctx, MapPageOpts{Kinds: []string{"related", "wikilink", "related"}, Limit: 2, After: first.Next})
	if err != nil || len(same.Nodes) != 2 || same.Nodes[0].Path != "c.md" {
		t.Errorf("the same kinds in another order: %+v, %v", same, err)
	}
	if p, err := e.store.MapPage(ctx, MapPageOpts{Limit: 1}); err != nil || p.Nodes[0].Path != "a.md" {
		t.Errorf("no after: %+v, %v", p, err)
	}
	// the limit is at most MaxMapPage, and DefaultMapPage when not asked
	if p, err := e.store.MapPage(ctx, MapPageOpts{}); err != nil || len(p.Nodes) != 10 || p.Next != "" {
		t.Errorf("the default page: %d nodes, next %q, %v", len(p.Nodes), p.Next, err)
	}
}

// TestAMapPageSeeksByPath: under no ANALYZE statistics, a page's documents are a search of
// document's (workspace_id, path) key after the cursor's path, with no OFFSET and no sort of its
// own, and its links a search of link_src for the page's documents.
func TestAMapPageSeeksByPath(t *testing.T) {
	e := pagesEnv(t)
	capture := &sqlCapture{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(capture)}
	first, err := s.MapPage(context.Background(), MapPageOpts{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	capture.stmts = nil
	if _, err := s.MapPage(context.Background(), MapPageOpts{Limit: 3, After: first.Next}); err != nil {
		t.Fatal(err)
	}
	var docs, links int
	for _, q := range capture.stmts {
		if strings.Contains(strings.ToUpper(q.SQL), "OFFSET") {
			t.Errorf("a statement skips by OFFSET: %s", q.SQL)
		}
		plan := e.plan(q.SQL, q.Args...)
		switch {
		case q.Table == "document" && strings.Contains(q.SQL, "ORDER BY"):
			docs++
			if !strings.Contains(q.SQL, `"document"."path" > `) || !strings.Contains(plan, "SEARCH document USING ") ||
				strings.Contains(plan, "USE TEMP B-TREE") {
				t.Errorf("the page's documents are not a seek by path:\n%s\n%s", q.SQL, plan)
			}
		case q.Table == "link" && strings.Contains(q.SQL, "JOIN"): // the page's links, their destinations' paths joined
			links++
			if !strings.Contains(plan, "USING INDEX link_src (workspace_id=? AND src_doc=?)") {
				t.Errorf("the page's links are not a search of link_src:\n%s\n%s", q.SQL, plan)
			}
		}
		if strings.Contains(plan, "SCAN document") || strings.Contains(plan, "SCAN link") {
			t.Errorf("a page's statement scans:\n%s\n%s", q.SQL, plan)
		}
	}
	if docs != 1 || links != 1 {
		t.Errorf("%d document and %d link statements for one page", docs, links)
	}
}

// TestAMapPageWhoseReadFailsSaysSo: a page's documents, supersession, tags or links failing to read
// fails the page, never a page with a part missing.
func TestAMapPageWhoseReadFailsSaysSo(t *testing.T) {
	e := pagesEnv(t)
	for _, c := range []struct{ name, match string }{
		{"the documents", `ORDER BY "document"."path"`},
		{"the supersession", `"link"."kind" IN (`},
		{"the tags", `FROM "doc_tag"`},
		{"the links", "JOIN"},
	} {
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(failingSQL{match: c.match})}
		if _, err := s.MapPage(context.Background(), MapPageOpts{Kinds: []string{"wikilink"}, Limit: 4}); !errors.Is(err, errGraphRead) {
			t.Errorf("%s failing: %v", c.name, err)
		}
	}
}

// An empty workspace's map is one empty page.
func TestAnEmptyMapIsOnePage(t *testing.T) {
	e := newEnv(t, options{})
	p, err := e.store.MapPage(context.Background(), MapPageOpts{})
	if err != nil || len(p.Nodes) != 0 || len(p.Edges) != 0 || p.Next != "" {
		t.Errorf("%+v, %v", p, err)
	}
}
