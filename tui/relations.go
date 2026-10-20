package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE RELATIONS DRAWER — SPC l (ADR 1791430651 §4.4): what the open document is connected to,
// grouped by the edge's kind and direction, from its frontmatter's relations and its body's links
// both ways. Enter opens a row in the editor and closes the drawer; d, in the drawer, shows the
// second ring (graph.neighborhood at depth 2) under each first-ring document; the unresolved
// relations and supersession cycles close the list. SPC j goes to the link under the cursor and
// SPC b back to the document opened before (golink.go, navigation.go); the editor's right-click
// menu has both, "Go to link", on a link, and "Back".
//
// The drawer is a feature of its own, a DocWatcher: a page shown loads its relations. Opening a
// document, following a link, the history it leaves and back stay the host's (files.go, below).

// relSection is one heading of the drawer: links out of the document of the kinds out, and links
// into it of the kinds in.
type relSection struct {
	label   string
	heading string // its heading's catalog id: %1 is how many rows it heads
	out, in []string
}

// body are the kinds of link a document's body makes (core/index.BodyKinds).
var body = []string{"wikilink", "embed", "markdown"}

// relSections are the drawer's headings, in its order. A relation in is the other side's
// relation out: one that names this document superseded_by makes this its successor.
var relSections = []relSection{
	{label: "superseded by", heading: "autodoc.text.relations.superseded_by", out: []string{"superseded_by"}, in: []string{"supersedes"}},
	{label: "supersedes", heading: "autodoc.text.relations.supersedes", out: []string{"supersedes"}, in: []string{"superseded_by"}},
	{label: "sources", heading: "autodoc.text.relations.sources", out: []string{"sources"}},
	{label: "cited by", heading: "autodoc.text.relations.cited_by", in: []string{"sources"}},
	{label: "amends", heading: "autodoc.text.relations.amends", out: []string{"amends"}},
	{label: "amended by", heading: "autodoc.text.relations.amended_by", in: []string{"amends"}},
	{label: "related", heading: "autodoc.text.relations.related", out: []string{"related"}, in: []string{"related"}},
	{label: "adr", heading: "autodoc.text.relations.adr", out: []string{"adr"}},
	{label: "review", heading: "autodoc.text.relations.review", out: []string{"review"}},
	{label: "reviews", heading: "autodoc.text.relations.reviews", in: []string{"review"}},
	{label: "blocked by", heading: "autodoc.text.relations.blocked_by", out: []string{"blocked"}},
	{label: "blocks", heading: "autodoc.text.relations.blocks", in: []string{"blocked"}},
	{label: "links", heading: "autodoc.text.relations.links", out: body},
	{label: "backlinks", heading: "autodoc.text.relations.backlinks", in: append(slices.Clone(body), "adr")},
}

// relRow is one row of the drawer: a heading or an unresolved link (path ""), a document to open,
// of workspace ws, or a file outside the workspace to open by its absolute path (external).
type relRow struct{ label, ws, path, section, external string }

// relEdge is one link of graph.links or graph.backlinks: the other end, as written, and its kind;
// external is a todo task's file outside the workspace, absolute (ADR 1792015212 §2.4).
type relEdge struct {
	path, raw, kind, external string
	resolved                  bool
}

// relations is what load gathered for one document.
type relations struct {
	out, in []relEdge
	cycles  []string    // the raw values of its supersession relations that go round a loop
	edges   [][3]string // at depth 2, graph.neighborhood's edges: src, dst, kind
	err     error
}

// relationRows are the drawer's rows for p's relations rl, and how many neighbours they list.
func relationRows(t wording, ws, p string, rl relations) ([]relRow, int) {
	ring := map[string]bool{p: true}
	for _, e := range append(slices.Clone(rl.out), rl.in...) {
		if e.resolved {
			ring[e.path] = true
		}
	}
	// second[n] are the documents beyond n, each with how it is linked to n
	second := map[string][]string{}
	for _, e := range rl.edges {
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
		for _, e := range rl.out {
			if e.resolved && slices.Contains(s.out, e.kind) && !slices.Contains(paths, e.path) {
				paths = append(paths, e.path)
			}
		}
		for _, e := range rl.in {
			if slices.Contains(s.in, e.kind) && !slices.Contains(paths, e.path) {
				paths = append(paths, e.path)
			}
		}
		var external []string
		for _, e := range rl.out {
			if e.external != "" && slices.Contains(s.out, e.kind) && !slices.Contains(external, e.external) {
				external = append(external, e.external)
			}
		}
		if len(paths)+len(external) == 0 {
			continue
		}
		rows = append(rows, relRow{label: t.Trf(s.heading, len(paths)+len(external))})
		for _, x := range external {
			rows = append(rows, relRow{label: "  " + t.Trf("autodoc.text.relations.outside_the_workspace", x), external: x, section: s.label})
		}
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
	for _, e := range rl.out {
		if !e.resolved && e.external == "" {
			unresolved = append(unresolved, t.Trf("autodoc.text.relations.names_no_document", e.raw, e.kind))
		}
	}
	for _, c := range rl.cycles {
		unresolved = append(unresolved, t.Trf("autodoc.text.relations.supersession_goes_round_a_loop", c))
	}
	if len(unresolved) > 0 {
		rows = append(rows, relRow{label: t.Trf("autodoc.text.relations.unresolved", len(unresolved))})
		for _, u := range unresolved {
			rows = append(rows, relRow{label: "  " + u})
		}
	}
	return rows, neighbours
}

// relationRowOf is a row of the drawer: a document, a heading, or what could not be resolved.
func relationRowOf(i int, r relRow) rowOf { return rowOf{"key": fmt.Sprint(i), "label": r.label} }

// related is the Links drawer (Relations › Links): the shown page's relations, as rows.
type related struct {
	c    Core
	rows *rows[relRow]
	page Page    // the page shown
	of   fileRef // the document the rows are of; the zero fileRef when they are of none
	deep bool    // the drawer shows the second ring (d)
	at   int     // the drawer's row under its cursor (FindPane reads it)
	seq  uint64  // numbers the reads: only the latest is shown, and a clear drops any in flight
}

func newRelated(c Core) *related {
	return &related{c: c, rows: newRows(tuidecl.NewListModel("key", "label"), relationRowOf)}
}

func (*related) Name() string { return "relations" }

func (r *related) Panels() []PanelSpec {
	s := relationsPanelSpec()
	s.Open = func(c PanelHost) {
		r.ofPage()
		c.OpenDrawer(relationsPanelSpec().ID)
	}
	return []PanelSpec{s}
}

func (r *related) State() map[string]any {
	return map[string]any{
		"App.relations":      r.rows.Model(),
		"App.relationsTitle": r.c.Tr("autodoc.text.relations.title_links"),
		"App.relationsIndex": 0,
	}
}

func (r *related) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleRelations": none(r.toggle),
		"App.relationsMoved":  oneNumber("a row", r.moved),
		"App.openRelation":    oneNumber("a row", r.open),
		"App.relationsDepth":  none(r.depth),
	}
}

// Catalog is the drawer's user command (catalog.go).
func (r *related) Catalog() []Entry {
	return []Entry{
		builtin("links.toggle", "the links", "App.toggleRelations()", r.toggle),
	}
}

// Shown reads the relations of the page shown. The rest of the page's lifecycle changes none:
// a page's links are the ones indexed, read when it is shown or the drawer opens.
func (r *related) Shown(p Page) {
	r.page = p
	r.ofPage()
}
func (*related) Edited(Page)    {}
func (*related) Moved(int, int) {}
func (*related) Reread(Page)    {}
func (*related) Closed()        {}

// toggle opens the drawer, or closes it; opening reads the relations again (panels.go).
func (r *related) toggle() { r.c.TogglePanel(relationsPanelSpec().ID) }

// moved is the drawer's cursor moving.
func (r *related) moved(i int) { r.at = i }

// ofPage reads the page's relations, or says why it has none.
func (r *related) ofPage() {
	switch ref := r.page.Ref; {
	case ref.path == "":
		r.clear(r.c.Tr("autodoc.text.relations.title_links"))
	case r.page.Outside:
		r.clear(r.c.Trf("autodoc.text.relations.title_links_outside", outsideBadge)) // it is in no workspace's graph
	default:
		// another document's rows go at once: until this one's are read, the drawer has none
		// rather than the last document's
		if r.of != ref {
			r.clear(r.c.Tr("autodoc.text.relations.title_links_reading"))
		}
		r.load(ref.ws, ref.path)
	}
}

// clear empties the drawer, saying why; a read still in flight is of rows it no longer shows.
func (r *related) clear(why string) {
	r.seq++
	r.of = fileRef{}
	r.rows.Reset(nil)
	r.c.Set("App.relationsTitle", why)
}

// load reads the relations of p in workspace ws and shows them in the drawer.
func (r *related) load(ws, p string) {
	tk, deep := r.c.Ticket(&r.seq, TicketFile), r.deep
	r.c.Go(func(ctx context.Context) any {
		var rl relations
		edges := func(verb string) ([]relEdge, error) {
			res, err := r.c.Call(ctx, verb, ws, p)
			if code(err) == rpc.CodeNotFound {
				return nil, nil // not indexed yet (just created or written): no link is known
			}
			var out []relEdge
			for _, l := range asList(res) {
				m := asMap(l)
				resolved, _ := m["resolved"].(bool)
				out = append(out, relEdge{path: str(m, "path"), raw: str(m, "raw"), kind: str(m, "kind"), resolved: resolved,
					external: str(m, "external")})
			}
			return out, err
		}
		if rl.out, rl.err = edges("graph.links"); rl.err != nil {
			return rl
		}
		if rl.in, rl.err = edges("graph.backlinks"); rl.err != nil {
			return rl
		}
		// the supersession relations of the workspace that resolve or not, and loop: this one's
		res, err := r.c.Call(ctx, "graph.unresolved", ws, map[string]any{"kinds": []any{"supersedes", "superseded_by"}})
		if err != nil {
			rl.err = err
			return rl
		}
		for _, u := range asList(res) {
			if m := asMap(u); str(m, "src") == p && str(m, "reason") == "cycle" {
				rl.cycles = append(rl.cycles, str(m, "raw"))
			}
		}
		if deep {
			res, err := r.c.Call(ctx, "graph.neighborhood", ws, p, 2)
			if code(err) != rpc.CodeNotFound && err != nil {
				rl.err = err
				return rl
			}
			for _, e := range asList(asMap(res)["edges"]) {
				m := asMap(e)
				rl.edges = append(rl.edges, [3]string{str(m, "src"), str(m, "dst"), str(m, "kind")})
			}
		}
		return rl
	}, func(v any) {
		rl := v.(relations)
		if !tk.Current() {
			return
		}
		if rl.err != nil {
			r.c.Failed(r.c.Tr("autodoc.notice.relations.links"), rl.err)
			return
		}
		r.show(ws, p, rl)
	})
}

// show lays out the drawer's rows: each section with what it holds, its count in its heading;
// under each document, at depth 2, the ones it links with that are not a neighbour; then the
// unresolved relations and cycles.
func (r *related) show(ws, p string, rl relations) {
	rows, neighbours := relationRows(r.c, ws, p, rl)
	r.of = fileRef{ws, p}
	r.rows.Reset(rows)
	// the cursor on the first document, not a heading: Enter opens it
	if first := slices.IndexFunc(rows, func(r relRow) bool { return r.path != "" }); first >= 0 {
		r.moveTo(first)
	}
	title := r.c.Trf("autodoc.text.relations.title_links_count", neighbours)
	if r.deep {
		title = r.c.Trf("autodoc.text.relations.title_links_depth_2", neighbours)
	}
	r.c.Set("App.relationsTitle", r.c.Trf("autodoc.text.relations.d_depth", title))
}

// LanguageChanged composes the drawer again in the UI's language: its title and its rows, read
// again for the page shown.
func (r *related) LanguageChanged() { r.ofPage() }

// moveTo puts the drawer's cursor on row i.
func (r *related) moveTo(i int) {
	r.c.Set("App.relationsIndex", -1) // moved away first: the same row twice still reaches the view
	r.c.Set("App.relationsIndex", i)
	r.at = i
}

// FindPane is the drawer's rows for find, as it shows them.
func (r *related) FindPane() FindPane {
	return FindPane{Name: "links", View: relationsPanelSpec().View, Scope: relationsPanelSpec().ID, Rows: func() ([]string, int, func(int, int) error) {
		return r.labels(), r.at, func(row, _ int) error { r.moveTo(row); return nil }
	}}
}

// labels are the drawer's rows as it shows them.
func (r *related) labels() []string {
	var out []string
	for _, row := range r.rows.All() {
		out = append(out, row.label)
	}
	return out
}

// open opens the drawer's row i in the editor, the drawer hidden first, not closed, as the GlobeMap
// is: the dock shows it again (ADR 1792397302 r3).
func (r *related) open(i int) {
	row, ok := r.rows.At(i)
	if !ok || row.external == "" && row.path == "" {
		return // a heading, or a relation that names nothing: the drawer stays
	}
	r.c.HidePanel(relationsPanelSpec().ID)
	switch {
	case row.external != "":
		r.c.OpenAbsolute(row.external) // its workspace's file when one indexes it, else outside them all
	case row.path != "":
		r.c.OpenRef(row.ws, row.path)
	}
}

// depth is d: the drawer shows the second ring, or no longer does. Only the drawer answers it.
func (r *related) depth() {
	if !r.c.HasFocus(relationsPanelSpec().View) {
		return
	}
	r.deep = !r.deep
	r.ofPage()
}
