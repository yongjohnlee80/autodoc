package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Use the host's actual workspace/file tickets while controlling callback delivery.
type relatedTicketCore struct {
	*scriptedCore
	h *Host
}

func (c relatedTicketCore) Ticket(seq *uint64, scope TicketScope) Ticket {
	return c.h.ticket(seq, scope)
}

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
	if title := c.set["App.relationsTitle"]; title != "links · reading…" {
		t.Fatalf("while a.md's relations are read the drawer says %q", title)
	}
	c.deliver(0)
	if got := r.labels(); !slices.Equal(got, []string{"links (1)", "  b.md", "backlinks (1)", "  c.md"}) {
		t.Fatalf("a.md's rows %q", got)
	}
	if c.set["App.relationsTitle"] != "links (2) · d depth" || r.at != 1 {
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
	if r.rows.Len() != 0 || c.set["App.relationsTitle"] != "links" {
		t.Fatalf("the draft: rows %q, title %q", r.labels(), c.set["App.relationsTitle"])
	}
	r.Shown(Page{Ref: fileRef{"", "/tmp/x.md"}, Outside: true})
	if c.set["App.relationsTitle"] != "links · "+outsideBadge {
		t.Fatalf("a file in no workspace: title %q", c.set["App.relationsTitle"])
	}
}

// d shows the second ring only when the drawer has the keyboard; Enter on a row opens its document.
func TestTheRelationsDepthAndOpen(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
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
	c.views = nil
	r.open(0) // a heading: nothing opens, and the drawer stays
	if len(c.views) != 0 || len(c.files) != 0 || len(c.hidden) != 0 {
		t.Fatalf("a heading: views %v, files %v, hidden %v; want none", c.views, c.files, c.hidden)
	}
	r.open(1) // b.md, the drawer hidden, not closed (ADR 1792397302 r3)
	if !slices.Equal(c.files, []string{"kb:b.md"}) || !slices.Equal(c.hidden, []string{"explorer"}) || len(c.views) != 0 {
		t.Fatalf("opened files %v, hidden %v, views %v; want b.md, the drawer hidden", c.files, c.hidden, c.views)
	}
}

// An answer for a page no longer shown is dropped: the file kept as the draft (its workspace gone)
// does not get the gone file's rows when their read answers late.
func TestTheRelationsDropAStaleAnswer(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	c.deferred = true
	r.Shown(Page{Ref: fileRef{"kb", "a.md"}}) // read 0, held
	r.Shown(Page{})                           // the file kept as the draft
	c.deliver(0)
	if r.rows.Len() != 0 || c.set["App.relationsTitle"] != "links" {
		t.Fatalf("the draft took the gone file's relations: rows %q, title %q", r.labels(), c.set["App.relationsTitle"])
	}
}

// Of two reads for one page (d pressed twice), the later one's rows are the ones shown, whatever
// order they answer in.
func TestTheLaterRelationsReadWins(t *testing.T) {
	c := newScripted("kb")
	scriptedGraph(c)
	r := newRelated(c)
	c.deferred = true
	r.Shown(Page{Ref: fileRef{"kb", "a.md"}})
	c.deliver(0)
	c.focused = true
	r.depth() // depth 2: read 0, held
	r.depth() // depth 1 again: read 1, held
	c.deliver(1, 0)
	if r.deep || slices.ContainsFunc(r.labels(), func(l string) bool { return strings.HasPrefix(l, "    › ") }) {
		t.Fatalf("the first d's late answer replaced the second's: deep %v, rows %q", r.deep, r.labels())
	}
}

// A stale or failed refresh must preserve the currently displayed relations.
// This replaces the former Host.applyRelations test after relations became a Feature.
func TestRelationsCallbackKeepsRowsForStaleOrFailedAnswers(t *testing.T) {
	c := relatedTicketCore{newScripted("kb"), newHost(NewSession("unused", nil), Options{Workspace: "kb"})}
	defer c.h.cancel()
	scriptedGraph(c.scriptedCore)
	r := newRelated(c)
	r.Shown(Page{Ref: fileRef{"kb", "a.md"}})
	c.deferred = true
	for _, scope := range []string{"document", "workspace", "graph.links", "graph.backlinks", "graph.unresolved", "graph.neighborhood"} {
		before, of := slices.Clone(r.rows.All()), r.of
		gen, epoch := c.h.file.gen, c.h.epoch
		scriptedGraph(c.scriptedCore)
		r.deep = scope == "graph.neighborhood"
		if strings.HasPrefix(scope, "graph.") {
			c.answers[scope] = func([]any) (any, error) {
				return nil, errors.New(scope + " refused")
			}
		}
		r.load("kb", "a.md")
		switch scope {
		case "document":
			c.h.file.gen++
		case "workspace":
			c.h.epoch++
		}
		c.deliver(0)
		c.h.file.gen, c.h.epoch = gen, epoch
		if !slices.Equal(before, r.rows.All()) || r.of != of {
			t.Fatalf("%s answer replaced the current relations", scope)
		}
	}
	if !slices.Equal(c.failed, []string{
		"links: graph.links refused", "links: graph.backlinks refused",
		"links: graph.unresolved refused", "links: graph.neighborhood refused",
	}) {
		t.Fatalf("refresh failure was not reported: %v", c.failed)
	}
}

// A todo task's relations (ADR 1792015212 §2.4): its review and blocked relations have their
// sections both ways, and a ref to a file outside the workspace is a row of its section that opens
// the file by its absolute path, never an unresolved link.
func TestATasksRelationsInTheDrawer(t *testing.T) {
	c := newScripted("kb")
	c.answers["graph.links"] = func([]any) (any, error) {
		return []any{
			map[string]any{"path": "reviews/r.md", "raw": "$KB_ROOT/reviews/r.md", "kind": "review", "resolved": true},
			map[string]any{"path": "", "raw": "$PROJECT_ROOT/docs/d.md", "kind": "adr", "resolved": false, "external": "/src/p/docs/d.md"},
			map[string]any{"path": ".todo-list/open/u.md", "raw": "u", "kind": "blocked", "resolved": true},
			map[string]any{"path": "", "raw": "$NOPE/x.md", "kind": "adr", "resolved": false},
		}, nil
	}
	c.answers["graph.backlinks"] = func([]any) (any, error) {
		return []any{map[string]any{"path": ".todo-list/open/w.md", "raw": "t", "kind": "blocked", "resolved": true}}, nil
	}
	c.answers["graph.unresolved"] = func([]any) (any, error) { return []any{}, nil }
	r := newRelated(c)
	r.Shown(Page{Ref: fileRef{"kb", ".todo-list/open/t.md"}})
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
	if !slices.Equal(c.files, []string{":/src/p/docs/d.md", "kb:reviews/r.md"}) {
		t.Fatalf("opened %v", c.files)
	}
}
