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
// both ways. Enter opens a row in the editor and the drawer stays; d, in the drawer, shows the
// second ring (graph.neighborhood at depth 2) under each first-ring document; the unresolved
// relations and supersession cycles close the list. SPC j is a card of the first nine neighbours,
// a digit each; SPC b goes back to the document opened before. The editor's right-click menu has
// "Go to link", on a link, and "Back".
//
// The drawer and the jump card are a feature of their own, a DocWatcher: a page shown loads its
// relations. Opening a document, the history it leaves and back stay the host's (files.go, below).

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

// relations is what load gathered for one document.
type relations struct {
	out, in []relEdge
	cycles  []string    // the raw values of its supersession relations that go round a loop
	edges   [][3]string // at depth 2, graph.neighborhood's edges: src, dst, kind
	err     error
}

// relationRows are the drawer's rows for p's relations rl, and how many neighbours they list.
func relationRows(ws, p string, rl relations) ([]relRow, int) {
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
	for _, e := range rl.out {
		if !e.resolved {
			unresolved = append(unresolved, e.raw+"  ("+e.kind+", names no document)")
		}
	}
	for _, c := range rl.cycles {
		unresolved = append(unresolved, c+"  (supersession goes round a loop)")
	}
	if len(unresolved) > 0 {
		rows = append(rows, relRow{label: fmt.Sprintf("unresolved (%d)", len(unresolved))})
		for _, u := range unresolved {
			rows = append(rows, relRow{label: "  " + u})
		}
	}
	return rows, neighbours
}

// relationRowOf is a row of the drawer: a document, a heading, or what could not be resolved.
func relationRowOf(i int, r relRow) rowOf { return rowOf{"key": fmt.Sprint(i), "label": r.label} }

// related is the Relations drawer and the jump card: the shown page's relations, as rows.
type related struct {
	c    Core
	rows *rows[relRow]
	page Page    // the page shown
	of   fileRef // the document the rows are of; the zero fileRef when they are of none
	deep bool    // the drawer shows the second ring (d)
	at   int     // the drawer's row under its cursor (find.go reads it)
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
		"App.relationsTitle": "relations",
		"App.jumpText":       "",
		"App.relationsIndex": 0,
	}
}

func (r *related) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleRelations": none(r.toggle),
		"App.relationsMoved":  oneNumber("a row", r.moved),
		"App.openRelation":    oneNumber("a row", r.open),
		"App.relationsDepth":  none(r.depth),
		"App.openJumpCard":    none(r.openJumpCard),
		"App.jumpTo":          oneNumber("1 to 9", r.jumpTo),
	}
}

// Catalog is the drawer's and the jump card's user commands (catalog.go).
func (r *related) Catalog() []Entry {
	return []Entry{
		builtin("file.related", "related files, by digit", "App.openJumpCard()", r.openJumpCard),
		builtin("links.toggle", "the relations", "App.toggleRelations()", r.toggle),
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
		r.clear("relations")
	case r.page.Outside:
		r.clear("relations · " + outsideBadge) // it is in no workspace's graph
	default:
		// another document's rows go at once: until this one's are read, the drawer, the card and
		// the right-click menu have none rather than the last document's
		if r.of != ref {
			r.clear("relations · reading…")
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
				out = append(out, relEdge{path: str(m, "path"), raw: str(m, "raw"), kind: str(m, "kind"), resolved: resolved})
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
			r.c.Failed("relations", rl.err)
			return
		}
		r.show(ws, p, rl)
	})
}

// show lays out the drawer's rows: each section with what it holds, its count in its heading;
// under each document, at depth 2, the ones it links with that are not a neighbour; then the
// unresolved relations and cycles.
func (r *related) show(ws, p string, rl relations) {
	rows, neighbours := relationRows(ws, p, rl)
	r.of = fileRef{ws, p}
	r.rows.Reset(rows)
	// the cursor on the first document, not a heading: Enter opens it
	if first := slices.IndexFunc(rows, func(r relRow) bool { return r.path != "" }); first >= 0 {
		r.moveTo(first)
	}
	title := fmt.Sprintf("relations (%d)", neighbours)
	if r.deep {
		title += " · depth 2"
	}
	r.c.Set("App.relationsTitle", title+" · d depth")
}

// moveTo puts the drawer's cursor on row i.
func (r *related) moveTo(i int) {
	r.c.Set("App.relationsIndex", -1) // moved away first: the same row twice still reaches the view
	r.c.Set("App.relationsIndex", i)
	r.at = i
}

// labels are the drawer's rows as it shows them (find.go finds in them).
func (r *related) labels() []string {
	var out []string
	for _, row := range r.rows.All() {
		out = append(out, row.label)
	}
	return out
}

// open opens the drawer's row i in the editor; the drawer stays open.
func (r *related) open(i int) {
	row, ok := r.rows.At(i)
	if !ok || row.path == "" {
		return
	}
	r.c.OpenRef(row.ws, row.path)
}

// depth is d: the drawer shows the second ring, or no longer does. Only the drawer answers it.
func (r *related) depth() {
	if !r.c.HasFocus(relationsPanelSpec().View) {
		return
	}
	r.deep = !r.deep
	r.ofPage()
}

// jumpRows are the jump card's documents: the drawer's first nine, once each.
func (r *related) jumpRows() []relRow {
	var out []relRow
	for _, row := range r.rows.All() {
		if row.path == "" || strings.HasPrefix(row.label, "    ") || slices.ContainsFunc(out, func(o relRow) bool { return o.path == row.path }) {
			continue
		}
		if out = append(out, row); len(out) == 9 {
			break
		}
	}
	return out
}

// openJumpCard is SPC j: the page's neighbours, a digit each.
func (r *related) openJumpCard() {
	rows := r.jumpRows()
	if len(rows) == 0 {
		r.c.Notify("no related documents: SPC l shows what " + pageName(r.page) + " links with")
		return
	}
	var b strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&b, "%d  %-14s %s\n", i+1, row.section, row.path)
	}
	r.c.Set("App.jumpText", strings.TrimRight(b.String(), "\n"))
	r.c.Open("jumpCard")
}

// jumpTo opens the jump card's document n (1 to 9).
func (r *related) jumpTo(n int) {
	r.c.Close("jumpCard")
	if rows := r.jumpRows(); n >= 1 && n <= len(rows) {
		r.c.OpenRef(rows[n-1].ws, rows[n-1].path)
	}
}
