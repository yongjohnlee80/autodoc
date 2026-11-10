package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// scriptedGraph answers the graph verbs: a.md links to b.md, c.md links back to it, and at depth 2
// b.md links on to d.md.
func scriptedGraph(c *featuretest.ScriptedCore) {
	c.Answers["graph.links"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "b.md", "raw": "b", "kind": "wikilink", "resolved": true}}, nil
	}
	c.Answers["graph.backlinks"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "c.md", "raw": "a", "kind": "wikilink", "resolved": true}}, nil
	}
	c.Answers["graph.unresolved"] = func([]any) (any, error) { return []any{}, nil }
	c.Answers["graph.neighborhood"] = func([]any) (any, error) {
		return map[string]any{"edges": []any{map[string]any{"src": "b.md", "dst": "d.md", "kind": "wikilink"}}}, nil
	}
}

// The Relations drawer, a feature of its own: a page shown reads its relations; the draft and a
// file in no workspace have none, and say why; the same document shown again keeps its rows while
// they are read, another document's go at once.
func TestTheRelationsFollowThePageShown(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	c.Deferred = true
	a := feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}}
	r.Shown(a)
	if title := c.Sets["App.relationsTitle"]; title != "links · reading…" {
		t.Fatalf("while a.md's relations are read the drawer says %q", title)
	}
	c.Deliver(0)
	if got := r.labels(); !slices.Equal(got, []string{"links (1)", "  b.md", "backlinks (1)", "  c.md"}) {
		t.Fatalf("a.md's rows %q", got)
	}
	if c.Sets["App.relationsTitle"] != "links (2) · d depth" || r.at != 1 {
		t.Fatalf("title %q, cursor on row %d: want 2 neighbours and the cursor on b.md", c.Sets["App.relationsTitle"], r.at)
	}
	r.Shown(a) // read again (a reload): its rows stay until the new ones come
	if r.rows.Len() != 4 {
		t.Fatal("the same document shown again lost its rows while they were read")
	}
	c.Deliver(0)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "z.md"}}) // another document: a.md's rows go at once
	if r.rows.Len() != 0 {
		t.Fatalf("z.md shown with a.md's rows %q", r.labels())
	}
	c.Deliver(0)

	r.Shown(feature.Page{})
	if r.rows.Len() != 0 || c.Sets["App.relationsTitle"] != "links" {
		t.Fatalf("the draft: rows %q, title %q", r.labels(), c.Sets["App.relationsTitle"])
	}
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "", Path: "/tmp/x.md"}, Outside: true})
	if c.Sets["App.relationsTitle"] != "links · "+outsideBadge {
		t.Fatalf("a file in no workspace: title %q", c.Sets["App.relationsTitle"])
	}
}

// d shows the second ring only when the drawer has the keyboard; Enter on a row opens its document.
func TestTheRelationsDepthAndOpen(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}})
	r.depth() // the keyboard elsewhere: d is not the drawer's
	if r.deep || slices.ContainsFunc(c.Calls, func(s string) bool { return strings.HasPrefix(s, "graph.neighborhood") }) {
		t.Fatal("d away from the drawer showed the second ring")
	}
	c.Focused = true
	r.depth()
	if got := r.labels(); !r.deep || !slices.Contains(got, "    › d.md  (wikilink →)") || !strings.Contains(c.Sets["App.relationsTitle"].(string), "depth 2") {
		t.Fatalf("depth 2: deep %v, rows %q, title %q", r.deep, got, c.Sets["App.relationsTitle"])
	}
	c.Views = nil
	r.open(0) // a heading: nothing opens, and the drawer stays
	if len(c.Views) != 0 || len(c.OpenedFiles) != 0 || len(c.Hidden) != 0 {
		t.Fatalf("a heading: views %v, files %v, hidden %v; want none", c.Views, c.OpenedFiles, c.Hidden)
	}
	// b.md, the drawer hidden, not closed (docs/decisions/editor.md#dock-clicks-hide-and-show)
	r.open(1)
	if !slices.Equal(c.OpenedFiles, []string{"kb:b.md"}) || !slices.Equal(c.Hidden, []string{"explorer"}) || len(c.Views) != 0 {
		t.Fatalf("opened files %v, hidden %v, views %v; want b.md, the drawer hidden", c.OpenedFiles, c.Hidden, c.Views)
	}
}

// An answer for a page no longer shown is dropped: the file kept as the draft (its workspace gone)
// does not get the gone file's rows when their read answers late.
func TestTheRelationsDropAStaleAnswer(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	c.Deferred = true
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}}) // read 0, held
	r.Shown(feature.Page{})                                             // the file kept as the draft
	c.Deliver(0)
	if r.rows.Len() != 0 || c.Sets["App.relationsTitle"] != "links" {
		t.Fatalf("the draft took the gone file's relations: rows %q, title %q", r.labels(), c.Sets["App.relationsTitle"])
	}
}

// Of two reads for one page (d pressed twice), the later one's rows are the ones shown, whatever
// order they answer in.
func TestTheLaterRelationsReadWins(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	c.Deferred = true
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}})
	c.Deliver(0)
	c.Focused = true
	r.depth() // depth 2: read 0, held
	r.depth() // depth 1 again: read 1, held
	c.Deliver(1, 0)
	if r.deep || slices.ContainsFunc(r.labels(), func(l string) bool { return strings.HasPrefix(l, "    › ") }) {
		t.Fatalf("the first d's late answer replaced the second's: deep %v, rows %q", r.deep, r.labels())
	}
}

// A stale or failed refresh must preserve the currently displayed relations: an answer for a file
// no longer shown, or a workspace no longer in use, and a read that failed.
func TestRelationsCallbackKeepsRowsForStaleOrFailedAnswers(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}})
	c.Deferred = true
	for _, scope := range []string{"document", "workspace", "graph.links", "graph.backlinks", "graph.unresolved", "graph.neighborhood"} {
		before, of := slices.Clone(r.rows.All()), r.of
		scriptedGraph(c)
		r.deep = scope == "graph.neighborhood"
		if strings.HasPrefix(scope, "graph.") {
			c.Answers[scope] = func([]any) (any, error) {
				return nil, errors.New(scope + " refused")
			}
		}
		r.load("kb", "a.md")
		switch scope {
		case "document":
			c.File++
		case "workspace":
			c.Epoch++
		}
		c.Deliver(0)
		if !slices.Equal(before, r.rows.All()) || r.of != of {
			t.Fatalf("%s answer replaced the current relations", scope)
		}
	}
	if !slices.Equal(c.Failures, []string{
		"links: graph.links refused", "links: graph.backlinks refused",
		"links: graph.unresolved refused", "links: graph.neighborhood refused",
	}) {
		t.Fatalf("refresh failure was not reported: %v", c.Failures)
	}
}

// A todo task's relations (docs/decisions/todos.md#task-relations-and-external-links): its review
// and blocked relations have their sections both ways, and a ref to a file outside the workspace is
// a row of its section that opens the file by its absolute path, never an unresolved link.
func TestATasksRelationsInTheDrawer(t *testing.T) {
	c := newScripted("kb")
	c.Answers["graph.links"] = func([]any) (any, error) {
		return []any{
			map[string]any{"path": "reviews/r.md", "raw": "$KB_ROOT/reviews/r.md", "kind": "review", "resolved": true},
			map[string]any{"path": "", "raw": "$PROJECT_ROOT/docs/d.md", "kind": "adr", "resolved": false, "external": "/src/p/docs/d.md"},
			map[string]any{"path": ".todo-list/open/u.md", "raw": "u", "kind": "blocked", "resolved": true},
			map[string]any{"path": "", "raw": "$NOPE/x.md", "kind": "adr", "resolved": false},
		}, nil
	}
	c.Answers["graph.backlinks"] = func([]any) (any, error) {
		return []any{map[string]any{"path": ".todo-list/open/w.md", "raw": "t", "kind": "blocked", "resolved": true}}, nil
	}
	c.Answers["graph.unresolved"] = func([]any) (any, error) { return []any{}, nil }
	r := newRelated(c)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: ".todo-list/open/t.md"}})
	want := []string{
		"adr (1)", "  /src/p/docs/d.md  (outside the workspace)",
		"review (1)", "  reviews/r.md",
		"blocked by (1)", "  .todo-list/open/u.md",
		"blocks (1)", "  .todo-list/open/w.md",
		"unresolved (1)", "  $NOPE/x.md  (adr, names no document)",
	}
	if got := r.labels(); !slices.Equal(got, want) {
		t.Fatalf("rows\n got %q\nwant %q", got, want)
	}
	r.open(1) // the external file: by its absolute path
	r.open(3) // the review: as the workspace's file
	if !slices.Equal(c.OpenedFiles, []string{":/src/p/docs/d.md", "kb:reviews/r.md"}) {
		t.Fatalf("opened %v", c.OpenedFiles)
	}
}

// graph.unresolved comes in pages (protocol 50): the drawer reads every page for the document's
// supersession loops, one on a later page as much as the first's, and asks for each by its offset.
func TestTheRelationsReadEveryPageOfTheUnresolved(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	var offsets []int64
	c.Answers["graph.unresolved"] = func(params []any) (any, error) {
		var offset int64
		if len(params) > 1 {
			offset, _ = feature.AsMap(params[1])["offset"].(int64)
		}
		offsets = append(offsets, offset)
		if offset == 0 {
			return map[string]any{"unresolved": []any{map[string]any{"src": "z.md", "raw": "y.md", "reason": "cycle"}},
				"count": int64(2), "next": int64(1)}, nil
		}
		return map[string]any{"unresolved": []any{map[string]any{"src": "a.md", "raw": "loop.md", "reason": "cycle"}},
			"count": int64(2)}, nil
	}
	r := newRelated(c)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}})
	if !slices.ContainsFunc(r.labels(), func(l string) bool { return strings.Contains(l, "loop.md") }) {
		t.Fatalf("a.md's loop on the second page is not shown: %q", r.labels())
	}
	if slices.ContainsFunc(r.labels(), func(l string) bool { return strings.Contains(l, "y.md") }) {
		t.Errorf("another document's loop is shown: %q", r.labels())
	}
	if !slices.Equal(offsets, []int64{0, 1}) {
		t.Errorf("pages asked at offsets %v, want 0 then 1", offsets)
	}
}

// A daemon whose pages never end: the drawer stops after 1000 calls of graph.unresolved, the same
// bound as Neovim's relations.load (tests/relations_spec.lua), never one more, and still shows the
// loops it read.
func TestTheRelationsReadAtMostAThousandPagesOfTheUnresolved(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	calls := 0
	c.Answers["graph.unresolved"] = func(params []any) (any, error) {
		calls++
		var offset int64
		if len(params) > 1 {
			offset, _ = feature.AsMap(params[1])["offset"].(int64)
		}
		return map[string]any{"unresolved": []any{map[string]any{"src": "a.md", "raw": "loop.md", "reason": "cycle"}},
			"count": int64(1 << 40), "next": offset + 1}, nil
	}
	r := newRelated(c)
	r.Shown(feature.Page{Ref: feature.FileRef{WS: "kb", Path: "a.md"}})
	if calls != 1000 {
		t.Errorf("graph.unresolved asked %d times, want 1000", calls)
	}
	if !slices.ContainsFunc(r.labels(), func(l string) bool { return strings.Contains(l, "loop.md") }) {
		t.Errorf("the loops read are not shown: %q", r.labels())
	}
}

// The links of the page read again (SPC l reads them while the drawer may already show them)
// leave the cursor on the row it was on, moved there by find or a key; another document's rows
// put it on their first document.
func TestReadingTheLinksAgainKeepsTheCursorsRow(t *testing.T) {
	rel := newRelated(newScripted("kb"))
	back := func(paths ...string) relations {
		var rl relations
		for _, p := range paths {
			rl.in = append(rl.in, relEdge{path: p, raw: "[[a]]", kind: "wikilink", resolved: true})
		}
		return rl
	}
	got := func() [4]int {
		rel.show("kb", "a.md", back("b.md", "x.md")) // the heading, b.md, x.md
		first := rel.at
		rel.moveTo(2)
		rel.show("kb", "a.md", back("b.md", "x.md"))
		kept := rel.at
		rel.show("kb", "a.md", back("x.md")) // the heading, x.md: the row moved up with it
		moved := rel.at
		rel.show("kb", "b.md", back("c.md", "x.md"))
		return [4]int{first, kept, moved, rel.at}
	}()
	if got != [4]int{1, 2, 1, 1} {
		t.Fatalf("the cursor's rows: first %d, read again %d, x.md moved up %d, another document's %d; want 1, 2, 1, 1", got[0], got[1], got[2], got[3])
	}
}
