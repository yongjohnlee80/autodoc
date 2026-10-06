package index

import (
	"context"
	"reflect"
	"testing"
)

// kinds lists a file's links as "kind raw -> path", "-" for unresolved.
func (e *env) kinds(p string) []string {
	e.t.Helper()
	ls, err := e.store.Links(context.Background(), p, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, l := range ls {
		to := l.Path
		if !l.Resolved {
			to = "-"
		}
		out = append(out, l.Kind+" "+l.Raw+" -> "+to)
	}
	return out
}

const adr19 = "---\ntype: adr\nnumber: \"0019\"\n---\n# Old\n"

// TestRelationsBecomeTypedLinks: each relation value that names a document is a link of its
// field's kind, after the body's links; a URL and prose make none.
func TestRelationsBecomeTypedLinks(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("adrs/0019-old.md", adr19, "adrs/a.md", "a", "y.md", "y",
		"x.md", "---\nsupersedes: 0019\namends:\n  - adrs/a.md §2\nrelated: [[y]]\n"+
			"sources:\n  - https://example.com/x\n  - \"Johno 2026-10-06 (chat): keep it simple\"\n---\nSee [[y]].\n")
	eq(t, "x.md's links", e.kinds("x.md"), []string{
		"wikilink [[y]] -> y.md",
		"supersedes 0019 -> adrs/0019-old.md",
		"amends adrs/a.md §2 -> adrs/a.md",
		"related [[y]] -> y.md",
	})
	ls, err := e.store.Links(context.Background(), "x.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ls[2].Anchor != "2" {
		t.Errorf("the amends link's anchor is %q, want the section after §", ls[2].Anchor)
	}
}

// TestAnADRNumberWakesOnArrival: a relation naming a decision record by number resolves when the
// record arrives, whichever way the number is padded, and comes loose when the record's number
// changes.
func TestAnADRNumberWakesOnArrival(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("x.md", "---\nadr: \"0021\"\n---\nbody\n")
	eq(t, "before the record", e.kinds("x.md"), []string{"adr 0021 -> -"})
	e.put("adrs/r.md", "---\ntype: adr\nnumber: 21\n---\n# R\n")
	eq(t, "the record arrived", e.kinds("x.md"), []string{"adr 0021 -> adrs/r.md"})
	e.put("adrs/r.md", "---\ntype: adr\nnumber: 22\n---\n# R\n")
	eq(t, "its number changed", e.kinds("x.md"), []string{"adr 0021 -> -"})
	e.put("adrs/r.md", "---\ntype: note\nnumber: 21\n---\n# R\n")
	eq(t, "a document not of type adr has no number", e.kinds("x.md"), []string{"adr 0021 -> -"})
}

// TestARelationPathTriesTheRootFirst: a relation path names the path from the workspace root
// first, then from the document's folder. The root wins when both exist, and the folder's takes
// over again when the root's goes.
func TestARelationPathTriesTheRootFirst(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("notes/x.md", "the folder's", "notes/n.md", "---\namends: [x.md]\n---\nbody\n")
	eq(t, "the folder's only", e.kinds("notes/n.md"), []string{"amends x.md -> notes/x.md"})
	e.put("x.md", "the root's")
	eq(t, "the root's arrived", e.kinds("notes/n.md"), []string{"amends x.md -> x.md"})
	e.remove("x.md")
	eq(t, "the root's gone", e.kinds("notes/n.md"), []string{"amends x.md -> notes/x.md"})
	e.remove("notes/x.md")
	eq(t, "neither", e.kinds("notes/n.md"), []string{"amends x.md -> -"})
	if got := e.unresolved(); !reflect.DeepEqual(got, []string{"notes/n.md x.md missing"}) {
		t.Errorf("unresolved = %q", got)
	}
}

// TestARelationFollowsItsTargetsName: a relation by name re-resolves when the document answering
// to the name moves, as a body wikilink does.
func TestARelationFollowsItsTargetsName(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("t.md", "t", "src.md", "---\nrelated: [golib-t]\n---\nbody\n", "golib-t.md", "first")
	eq(t, "by name", e.kinds("src.md"), []string{"related golib-t -> golib-t.md"})
	e.remove("golib-t.md")
	e.put("sub/golib-t.md", "moved")
	eq(t, "moved", e.kinds("src.md"), []string{"related golib-t -> sub/golib-t.md"})
}

// TestRelationsAreNotInLinks: the in-links boost counts body links; a relation leaves it as it was.
func TestRelationsAreNotInLinks(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("t.md", "target", "a.md", "[[t]]", "b.md", "---\nrelated: [[t]]\n---\nbody\n", "c.md", "---\nsupersedes: [t.md]\n---\nbody\n")
	ctx := context.Background()
	err := searchStore{s: e.store, sem: e.ix.sem}.View(ctx, func(v *View) error {
		id, err := v.s.docID(v.tx, "t.md")
		if err != nil {
			return err
		}
		sig, err := v.Signals(ctx, []int64{id})
		if err != nil {
			return err
		}
		if got := sig[id].InLinks; got != 1 {
			t.Errorf("t.md has %d in-links, want 1: only a.md's body links to it", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTheBodyKindsWalkNoRelation: a walk given the body kinds goes through no relation, so a
// document reached only through one is not in the neighbourhood, and nor is what lies past it.
func TestTheBodyKindsWalkNoRelation(t *testing.T) {
	e := newEnv(t, Options{})
	// a --supersedes--> b --wikilink--> c
	e.put("a.md", "---\nsupersedes: [b.md]\n---\nbody\n", "b.md", "[[c]]", "c.md", "c")
	ctx := context.Background()
	got, err := e.store.Neighborhood(ctx, "a.md", 2, BodyKinds)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Neighborhood{Nodes: []string{"a.md"}}) {
		t.Errorf("body kinds: %+v, want a.md alone", got)
	}
	all, err := e.store.Neighborhood(ctx, "a.md", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Neighborhood{Nodes: []string{"a.md", "b.md", "c.md"},
		Edges: []Edge{{"a.md", "b.md", "supersedes"}, {"b.md", "c.md", LinkWikilink}}}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("every kind: %+v, want %+v", all, want)
	}
	bl, err := e.store.Backlinks(ctx, "b.md", BodyKinds)
	if err != nil || len(bl) != 0 {
		t.Errorf("b.md's body backlinks: %+v, %v; want none", bl, err)
	}
	if us, err := e.store.Unresolved(ctx, BodyKinds); err != nil || len(us) != 0 {
		t.Errorf("unresolved body links: %+v, %v", us, err)
	}
}

// TestASupersessionLoopIsReported: two documents that each say the other replaced it resolve, so
// neither is unresolved, yet a loop has no order: both relations are listed as a cycle, with every
// kind, and not with the body kinds alone.
func TestASupersessionLoopIsReported(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "---\nsupersedes: [b.md]\n---\nA\n", "b.md", "---\nsuperseded_by: [a.md]\nsupersedes: [a.md]\n---\nB\n",
		"c.md", "---\nsupersedes: [d.md]\n---\nC\n", "d.md", "D", "broken.md", "[[nowhere]]",
		// both sides of one relation, which agree: x replaced y; no loop
		"x.md", "---\nsupersedes: [y.md]\n---\nX\n", "y.md", "---\nsuperseded_by: [x.md]\n---\nY\n",
		// a loop of three: p replaced q, q replaced r, r replaced p
		"p.md", "---\nsupersedes: [q.md]\n---\nP\n", "q.md", "---\nsupersedes: [r.md]\n---\nQ\n", "r.md", "---\nsupersedes: [p.md]\n---\nR\n",
		// a document that says it replaces itself is ignored, as the demotion ignores it
		"self.md", "---\nsupersedes: [self.md]\n---\nS\n")
	// a supersedes b; b says a replaced it (the same relation) and that it replaces a: a loop
	got := e.unresolved()
	want := []string{"broken.md [[nowhere]] missing", "a.md b.md cycle", "b.md a.md cycle", "b.md a.md cycle",
		"p.md q.md cycle", "q.md r.md cycle", "r.md p.md cycle"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unresolved:\n got %q\nwant %q", got, want)
	}
	body, err := e.store.Unresolved(context.Background(), BodyKinds)
	if err != nil || len(body) != 1 || body[0].Reason != ReasonMissing {
		t.Errorf("body kinds alone: %+v, %v; want only the missing wikilink", body, err)
	}
}
