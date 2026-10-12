package tui

import (
	"slices"
	"strings"
	"testing"
)

// scriptedGraph answers the graph verbs: a.md links to b.md, c.md links back to it, and at depth 2
// b.md links on to d.md.
func scriptedGraph(c *scriptedCore) {
	c.answers["graph.links"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "b.md", "raw": "b", "kind": "wikilink", "resolved": true}}, nil
	}
	c.answers["graph.backlinks"] = func([]any) (any, error) {
		return []any{map[string]any{"path": "c.md", "raw": "a", "kind": "wikilink", "resolved": true}}, nil
	}
	c.answers["graph.unresolved"] = func([]any) (any, error) { return []any{}, nil }
	c.answers["graph.neighborhood"] = func([]any) (any, error) {
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
	c.deferred = true
	a := Page{Ref: fileRef{"kb", "a.md"}}
	r.Shown(a)
	if title := c.set["App.relationsTitle"]; title != "relations · reading…" {
		t.Fatalf("while a.md's relations are read the drawer says %q", title)
	}
	c.deliver(0)
	if got := r.labels(); !slices.Equal(got, []string{"links (1)", "  b.md", "backlinks (1)", "  c.md"}) {
		t.Fatalf("a.md's rows %q", got)
	}
	if c.set["App.relationsTitle"] != "relations (2) · d depth" || r.at != 1 {
		t.Fatalf("title %q, cursor on row %d: want 2 neighbours and the cursor on b.md", c.set["App.relationsTitle"], r.at)
	}
	r.Shown(a) // read again (a reload): its rows stay until the new ones come
	if r.rows.Len() != 4 {
		t.Fatal("the same document shown again lost its rows while they were read")
	}
	c.deliver(0)
	r.Shown(Page{Ref: fileRef{"kb", "z.md"}}) // another document: a.md's rows go at once
	if r.rows.Len() != 0 {
		t.Fatalf("z.md shown with a.md's rows %q", r.labels())
	}
	c.deliver(0)

	r.Shown(Page{})
	if r.rows.Len() != 0 || c.set["App.relationsTitle"] != "relations" {
		t.Fatalf("the draft: rows %q, title %q", r.labels(), c.set["App.relationsTitle"])
	}
	r.Shown(Page{Ref: fileRef{"", "/tmp/x.md"}, Outside: true})
	if c.set["App.relationsTitle"] != "relations · "+outsideBadge {
		t.Fatalf("a file in no workspace: title %q", c.set["App.relationsTitle"])
	}
}

// d shows the second ring only when the drawer has the keyboard; Enter on a row and a digit on
// the jump card open documents; the card says when there are none.
func TestTheRelationsDepthAndJumps(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	r.openJumpCard() // nothing shown yet
	if len(c.notes) != 1 || !strings.Contains(c.notes[0], "no related documents") {
		t.Fatalf("an empty jump card said %q", c.notes)
	}
	r.Shown(Page{Ref: fileRef{"kb", "a.md"}})
	r.depth() // the keyboard elsewhere: d is not the drawer's
	if r.deep || slices.ContainsFunc(c.calls, func(s string) bool { return strings.HasPrefix(s, "graph.neighborhood") }) {
		t.Fatal("d away from the drawer showed the second ring")
	}
	c.focused = true
	r.depth()
	if got := r.labels(); !r.deep || !slices.Contains(got, "    › d.md  (wikilink →)") || !strings.Contains(c.set["App.relationsTitle"].(string), "depth 2") {
		t.Fatalf("depth 2: deep %v, rows %q, title %q", r.deep, got, c.set["App.relationsTitle"])
	}
	r.open(0) // a heading: nothing opens
	r.open(1) // b.md
	r.openJumpCard()
	r.jumpTo(2) // c.md, the second neighbour (d.md is under b.md, not a neighbour)
	if !slices.Equal(c.files, []string{"kb:b.md", "kb:c.md"}) || !slices.Equal(c.opened, []string{"jumpCard"}) || !slices.Equal(c.closed, []string{"jumpCard"}) {
		t.Fatalf("opened files %v, dialogs opened %v closed %v", c.files, c.opened, c.closed)
	}
}
