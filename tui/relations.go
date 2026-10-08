package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE RELATIONS DRAWER — SPC l (ADR 1791430651 §4.4): what the open document is connected to,
// grouped by the edge's kind and direction, from its frontmatter's relations and its body's links
// both ways. Enter opens a row in the editor and the drawer stays; d, in the drawer, shows the
// second ring (graph.neighborhood at depth 2) under each first-ring document; the unresolved
// relations and supersession cycles close the list. SPC j is a card of the first nine neighbours,
// a digit each; SPC b goes back to the document opened before. The editor's right-click menu has
// both ("Go to related…", "Back").

// relSection is one heading of the drawer: links out of the document of the kinds out, and links
// into it of the kinds in.
type relSection struct {
	label   string
	out, in []string
}

// body are the kinds of link a document's body makes (core/index.BodyKinds).
var body = []string{"wikilink", "embed", "markdown"}

// relSections are the drawer's headings, in its order. A relation in is the other side's
// relation out: one that names this document superseded_by makes this its successor.
var relSections = []relSection{
	{label: "superseded by", out: []string{"superseded_by"}, in: []string{"supersedes"}},
	{label: "supersedes", out: []string{"supersedes"}, in: []string{"superseded_by"}},
	{label: "sources", out: []string{"sources"}},
	{label: "cited by", in: []string{"sources"}},
	{label: "amends", out: []string{"amends"}},
	{label: "amended by", in: []string{"amends"}},
	{label: "related", out: []string{"related"}, in: []string{"related"}},
	{label: "adr", out: []string{"adr"}},
	{label: "links", out: body},
	{label: "backlinks", in: append(slices.Clone(body), "adr")},
}

// relRow is one row of the drawer: a heading or an unresolved link (path ""), or a document to
// open, of workspace ws.
type relRow struct{ label, ws, path, section string }

// relEdge is one link of graph.links or graph.backlinks: the other end, as written, and its kind.
type relEdge struct {
	path, raw, kind string
	resolved        bool
}

// relations is what loadRelations gathered for one document.
type relations struct {
	out, in []relEdge
	cycles  []string    // the raw values of its supersession relations that go round a loop
	edges   [][3]string // at depth 2, graph.neighborhood's edges: src, dst, kind
	err     error
}

// loadRelations reads the relations of p in workspace ws and shows them in the drawer.
func (h *Host) loadRelations(ws, p string) {
	gen, ep, deep := h.file.gen, h.epoch, h.relDeep
	do(h, func(ctx context.Context) relations {
		var r relations
		edges := func(verb string) ([]relEdge, error) {
			res, err := h.call(ctx, verb, ws, p)
			if code(err) == rpc.CodeNotFound {
				return nil, nil // not indexed yet (just created or written): no link is known
			}
			var out []relEdge
			for _, l := range asList(res) {
				m := asMap(l)
				resolved, _ := m["resolved"].(bool)
				out = append(out, relEdge{path: str(m, "path"), raw: str(m, "raw"), kind: str(m, "kind"), resolved: resolved})
			}
			return out, err
		}
		if r.out, r.err = edges("graph.links"); r.err != nil {
			return r
		}
		if r.in, r.err = edges("graph.backlinks"); r.err != nil {
			return r
		}
		// the supersession relations of the workspace that resolve or not, and loop: this one's
		res, err := h.call(ctx, "graph.unresolved", ws, map[string]any{"kinds": []any{"supersedes", "superseded_by"}})
		if err != nil {
			r.err = err
			return r
		}
		for _, u := range asList(res) {
			if m := asMap(u); str(m, "src") == p && str(m, "reason") == "cycle" {
				r.cycles = append(r.cycles, str(m, "raw"))
			}
		}
		if deep {
			res, err := h.call(ctx, "graph.neighborhood", ws, p, 2)
			if code(err) != rpc.CodeNotFound && err != nil {
				r.err = err
				return r
			}
			for _, e := range asList(asMap(res)["edges"]) {
				m := asMap(e)
				r.edges = append(r.edges, [3]string{str(m, "src"), str(m, "dst"), str(m, "kind")})
			}
		}
		return r
	}, func(r relations) {
		if gen != h.file.gen || ep != h.epoch {
			return
		}
		if r.err != nil {
			h.failed("relations", r.err)
			return
		}
		h.showRelations(ws, p, r)
	})
}

// showRelations lays out the drawer's rows: each section with what it holds, its count in its
// heading; under each document, at depth 2, the ones it links with that are not a neighbour; then
// the unresolved relations and cycles.
func (h *Host) showRelations(ws, p string, r relations) {
	ring := map[string]bool{p: true}
	for _, e := range append(slices.Clone(r.out), r.in...) {
		if e.resolved {
			ring[e.path] = true
		}
	}
	// second[n] are the documents beyond n, each with how it is linked to n
	second := map[string][]string{}
	for _, e := range r.edges {
		src, dst, kind := e[0], e[1], e[2]
		switch {
		case ring[src] && src != p && !ring[dst]:
			second[src] = append(second[src], dst+"  ("+kind+" →)")
		case ring[dst] && dst != p && !ring[src]:
			second[dst] = append(second[dst], src+"  (← "+kind+")")
		}
	}
	var rows []relRow
	under := map[string]bool{} // a document's second ring is listed once, under its first row
	neighbours := 0
	for _, s := range relSections {
		var paths []string
		for _, e := range r.out {
			if e.resolved && slices.Contains(s.out, e.kind) && !slices.Contains(paths, e.path) {
				paths = append(paths, e.path)
			}
		}
		for _, e := range r.in {
			if slices.Contains(s.in, e.kind) && !slices.Contains(paths, e.path) {
				paths = append(paths, e.path)
			}
		}
		if len(paths) == 0 {
			continue
		}
		rows = append(rows, relRow{label: fmt.Sprintf("%s (%d)", s.label, len(paths))})
		for _, q := range paths {
			rows = append(rows, relRow{label: "  " + q, ws: ws, path: q, section: s.label})
			if under[q] {
				continue
			}
			under[q] = true
			neighbours++
			beyond := second[q]
			slices.Sort(beyond)
			for _, b := range slices.Compact(beyond) {
				rows = append(rows, relRow{label: "    › " + b, ws: ws, path: b[:strings.Index(b, "  (")], section: s.label})
			}
		}
	}
	var unresolved []string
	for _, e := range r.out {
		if !e.resolved {
			unresolved = append(unresolved, e.raw+"  ("+e.kind+", names no document)")
		}
	}
	for _, c := range r.cycles {
		unresolved = append(unresolved, c+"  (supersession goes round a loop)")
	}
	if len(unresolved) > 0 {
		rows = append(rows, relRow{label: fmt.Sprintf("unresolved (%d)", len(unresolved))})
		for _, u := range unresolved {
			rows = append(rows, relRow{label: "  " + u})
		}
	}
	h.relRows = rows
	list := make([]rowOf, len(rows))
	for i, row := range rows {
		list[i] = rowOf{"key": fmt.Sprint(i), "label": row.label}
	}
	h.relationsModel.Reset(list)
	// the cursor on the first document, not a heading: Enter opens it
	if first := slices.IndexFunc(rows, func(r relRow) bool { return r.path != "" }); first >= 0 {
		h.set("App.linksIndex", -1) // moved away first: the same row twice still reaches the view
		h.set("App.linksIndex", first)
	}
	title := fmt.Sprintf("relations (%d)", neighbours)
	if h.relDeep {
		title += " · depth 2"
	}
	h.set("App.relationsTitle", title+" · d depth")
}

// clearRelations empties the drawer, saying why when a file is open.
func (h *Host) clearRelations(why string) {
	h.relRows = nil
	h.relationsModel.Reset(nil)
	h.set("App.relationsTitle", why)
}

// relationsOfOpenFile reads the open file's relations, or says why it has none.
func (h *Host) relationsOfOpenFile() {
	switch {
	case !h.file.open:
		h.clearRelations("relations")
	case h.file.outside():
		h.clearRelations("relations · " + outsideBadge) // it is in no workspace's graph
	default:
		h.loadRelations(h.file.ws, h.file.path)
	}
}

// openRelation opens the drawer's row i in the editor; the drawer stays open.
func (h *Host) openRelation(i int) {
	if i < 0 || i >= len(h.relRows) || h.relRows[i].path == "" {
		return
	}
	row := h.relRows[i]
	h.openIn(row.ws, row.path)
}

// relationsDepth is d: the drawer shows the second ring, or no longer does. Only the drawer
// answers it.
func (h *Host) relationsDepth() {
	if comp, ok := h.p.Find(panels["links"]); !ok || !h.p.App().FocusWithin(comp) {
		return
	}
	h.relDeep = !h.relDeep
	h.relationsOfOpenFile()
}

// jumpRows are the jump card's documents: the drawer's first nine, once each.
func (h *Host) jumpRows() []relRow {
	var out []relRow
	for _, r := range h.relRows {
		if r.path == "" || strings.HasPrefix(r.label, "    ") || slices.ContainsFunc(out, func(o relRow) bool { return o.path == r.path }) {
			continue
		}
		if out = append(out, r); len(out) == 9 {
			break
		}
	}
	return out
}

// openJumpCard is SPC j: the open document's neighbours, a digit each.
func (h *Host) openJumpCard() {
	rows := h.jumpRows()
	if len(rows) == 0 {
		h.notify("no related documents: SPC l shows what " + h.file.name() + " links with")
		return
	}
	var b strings.Builder
	for i, r := range rows {
		fmt.Fprintf(&b, "%d  %-14s %s\n", i+1, r.section, r.path)
	}
	h.set("App.jumpText", strings.TrimRight(b.String(), "\n"))
	h.open("jumpCard")
}

// jumpTo opens the jump card's document n (1 to 9).
func (h *Host) jumpTo(n int) {
	h.closeDialog("jumpCard")
	if rows := h.jumpRows(); n >= 1 && n <= len(rows) {
		h.openIn(rows[n-1].ws, rows[n-1].path)
	}
}

// fileRef is a document opened: its workspace ("" outside every one) and its path.
type fileRef struct{ ws, path string }

// maxHistory is how many documents back goes.
const maxHistory = 32

// rememberLeft puts the document left behind on the history, once at its top.
func (h *Host) rememberLeft(prev fileRef) {
	if n := len(h.history); n > 0 && h.history[n-1] == prev {
		return
	}
	h.history = append(h.history, prev)
	if len(h.history) > maxHistory {
		h.history = h.history[len(h.history)-maxHistory:]
	}
}

// goBack is SPC b: the document opened before this one, which is not remembered again.
func (h *Host) goBack() {
	cur := fileRef{h.file.ws, h.file.path}
	for len(h.history) > 0 {
		prev := h.history[len(h.history)-1]
		h.history = h.history[:len(h.history)-1]
		if h.file.open && prev == cur {
			continue
		}
		h.openRef(prev, false)
		return
	}
	h.notify("nothing to go back to: SPC b returns to the documents opened before this one")
}

// editorMenu is the editor's right-click menu: the stock rows, then the related documents and back.
func (h *Host) editorMenu(e *widget.Editor) []widget.MenuItemModel {
	items := widget.EditorContextItems(e)
	related := widget.NewCommand("autodoc.related", "Go to related…", widget.EditorMenuAction{ID: "autodoc.related",
		Run: func(*widget.Editor) { h.openJumpCard() }})
	related.Enabled = len(h.jumpRows()) > 0
	back := widget.NewCommand("autodoc.back", "Back", widget.EditorMenuAction{ID: "autodoc.back",
		Run: func(*widget.Editor) { h.goBack() }})
	back.Enabled = len(h.history) > 0
	return append(items, widget.NewSeparator("autodoc.sep.relations"), related, back)
}
