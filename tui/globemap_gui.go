//go:build gui

package tui

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"slices"

	"github.com/yongjohnlee80/gio/f32"
	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE GLOBEMAP — Relations › GlobeMap: every document of the workspace in use and the links among
// them, placed by a seeded force layout (golib graph/layout.Force), as Obsidian's graph view. Drag
// pans, the wheel and + - zoom, 0 fits it again, f cycles which links it follows; a press on a
// document closes the panel and opens it. It is read again when it opens, when another workspace
// is entered, and when the filter changes.

// globeLimits bound a build: a knowledge base of twenty thousand documents.
var globeLimits = graphlayout.Limits{MaxNodes: 20000, MaxEdges: 200000, MaxWork: 2_000_000_000}

// globeFilters are the links the GlobeMap follows, f cycling through them.
var globeFilters = []struct {
	name  string
	kinds []any // graph.map's kinds; nil every kind
}{
	{"every link", nil},
	{"body links", []any{"wikilink", "embed", "markdown"}},
	{"relations", []any{"supersedes", "superseded_by", "amends", "related", "sources", "adr", "review", "blocked"}},
}

// Edge classes, each one path and one colour: body links, supersession, every other relation.
const (
	edgeBody = iota
	edgeSupersession
	edgeRelation
	edgeClasses
)

func edgeClass(kind string) int {
	switch kind {
	case "wikilink", "embed", "markdown":
		return edgeBody
	case "supersedes", "superseded_by":
		return edgeSupersession
	}
	return edgeRelation
}

// globe is one GlobeMap build: everything derived from one graph.map answer, made off the loop and
// never changed once installed.
type globe struct {
	ws    string
	m     relMap
	at    []graphlayout.Point // each document's centre, in layout units
	size  graphlayout.Size    // their extent
	r     float32             // a document's dot's radius, in layout units
	edges [edgeClasses]*gui.Path
	dots  []*gui.Path // one per relationTypes, then one for every other type
	rings *gui.Path   // a ring round each superseded document
	grid  hitGrid
}

// buildGlobe lays m out and makes its paths and hit grid.
func buildGlobe(ctx context.Context, ws string, m relMap) (*globe, error) {
	in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1}
	for _, e := range m.Edges {
		in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
	}
	res, err := graphlayout.Force(ctx, in, globeLimits)
	if err != nil {
		return nil, err
	}
	gl := &globe{ws: ws, m: m, at: res.Nodes, size: res.Size, rings: &gui.Path{}}
	if n := len(m.Nodes); n > 0 {
		// a dot about an eighth of the room each document has, so the whole map shows them
		gl.r = max(0.12*float32(math.Sqrt(float64((res.Size.W+30)*(res.Size.H+30))/float64(n))), 3)
	}
	for k := range gl.edges {
		gl.edges[k] = &gui.Path{}
	}
	for _, e := range m.Edges {
		a, b := gl.at[e.Src], gl.at[e.Dst]
		gl.edges[edgeClass(e.Kind)].MoveTo(gui.Pt(a.X, a.Y)).LineTo(gui.Pt(b.X, b.Y))
	}
	gl.dots = make([]*gui.Path, len(relationTypes)+1)
	for k := range gl.dots {
		gl.dots[k] = &gui.Path{}
	}
	for i, n := range m.Nodes {
		k := slices.Index(relationTypes, n.Type)
		if k < 0 {
			k = len(relationTypes)
		}
		circle(gl.dots[k], gl.at[i], gl.r)
		if n.Superseded {
			circle(gl.rings, gl.at[i], gl.r*1.7)
		}
	}
	gl.grid = newHitGrid(gl.at, gl.size, gl.r*4)
	return gl, ctx.Err()
}

// circle adds a circle round c of radius r to p: four cubic arcs.
func circle(p *gui.Path, c graphlayout.Point, r float32) {
	const k = 0.5523 // a quarter circle's cubic control distance, over r
	x, y, d := c.X, c.Y, r*k
	p.MoveTo(gui.Pt(x+r, y)).
		CubeTo(gui.Pt(x+r, y+d), gui.Pt(x+d, y+r), gui.Pt(x, y+r)).
		CubeTo(gui.Pt(x-d, y+r), gui.Pt(x-r, y+d), gui.Pt(x-r, y)).
		CubeTo(gui.Pt(x-r, y-d), gui.Pt(x-d, y-r), gui.Pt(x, y-r)).
		CubeTo(gui.Pt(x+d, y-r), gui.Pt(x+r, y-d), gui.Pt(x+r, y)).
		Close()
}

// hitGrid is the documents by square cells of the layout: where the pointer is, the few near it.
type hitGrid struct {
	cell       float32
	cols, rows int
	cells      [][]int32
}

func newHitGrid(at []graphlayout.Point, size graphlayout.Size, cell float32) hitGrid {
	g := hitGrid{cell: max(cell, 1)}
	g.cols, g.rows = int(size.W/g.cell)+1, int(size.H/g.cell)+1
	g.cells = make([][]int32, g.cols*g.rows)
	for i, p := range at {
		c := g.index(p)
		g.cells[c] = append(g.cells[c], int32(i))
	}
	return g
}

func (g hitGrid) index(p graphlayout.Point) int {
	x := min(max(int(p.X/g.cell), 0), g.cols-1)
	y := min(max(int(p.Y/g.cell), 0), g.rows-1)
	return y*g.cols + x
}

// nearest is the document nearest p no farther than within, or -1. within is at most a cell: the
// cells round p's are all it looks in.
func (g hitGrid) nearest(at []graphlayout.Point, p graphlayout.Point, within float32) int {
	if len(g.cells) == 0 {
		return -1
	}
	best, bestD := -1, within*within
	cx, cy := int(math.Floor(float64(p.X/g.cell))), int(math.Floor(float64(p.Y/g.cell)))
	for y := cy - 1; y <= cy+1; y++ {
		for x := cx - 1; x <= cx+1; x++ {
			if x < 0 || y < 0 || x >= g.cols || y >= g.rows {
				continue
			}
			for _, i := range g.cells[y*g.cols+x] {
				dx, dy := at[i].X-p.X, at[i].Y-p.Y
				if d := dx*dx + dy*dy; d <= bestD {
					best, bestD = int(i), d
				}
			}
		}
	}
	return best
}

// globeMap is the GlobeMap feature: its builds, fenced (load), and the viewer's pan and zoom.
type globeMap struct {
	c      Core
	seq    uint64
	open   bool
	filter int    // globeFilters' index
	cur    *globe // the build shown; nil until one is installed
	status string // what a build under way says; "" none
	view   *globeView

	zoom  float32    // view pixels per layout unit
	off   gui.Point  // where the layout's origin is in the view
	moved bool       // the user panned or zoomed: no longer fitted
	hover int        // the document under the pointer; -1 none
	drag  *globeDrag // a press held on the view: it holds the pointer until released or lost
}

type globeDrag struct {
	from, off gui.Point
	moved     bool
}

func newGlobeMap(c Core) *globeMap { return &globeMap{c: c, hover: -1} }

func (*globeMap) Name() string { return "globemap" }

func (g *globeMap) Panels() []PanelSpec {
	return []PanelSpec{{ID: globeMapPanelID, View: globeMapViewID, Priority: 70,
		Edge: func(prefs) string { return "center" },
		Opened: func(PanelHost) {
			g.open = true
			g.load()
		},
		Closed: func(c PanelHost) {
			g.open, g.drag = false, nil // a gesture ends with its panel (its capture is lost as it hides)
			g.seq++                     // a build under way is dropped
			c.Keep(c.RestoreKeyboardPane("editor"))
		}}}
}

func (g *globeMap) State() map[string]any { return map[string]any{"App.globeMapTitle": "GlobeMap"} }

func (g *globeMap) Commands() map[string]Command {
	return map[string]Command{"App.toggleGlobeMap": none(g.toggle)}
}

func (g *globeMap) Catalog() []Entry {
	return []Entry{builtin("globemap.toggle", "the GlobeMap", "App.toggleGlobeMap()", g.toggle)}
}

func (g *globeMap) toggle() { g.c.TogglePanel(globeMapPanelID) }

// Entered: another workspace's map, when it shows.
func (g *globeMap) Entered(string) {
	if g.open {
		g.load()
	}
}

// load reads the workspace's map and builds it off the loop; on the loop it installs the build
// whole, or drops it when a newer load, another workspace or the panel's closing came first.
func (g *globeMap) load() {
	ws, filter := g.c.Workspace(), g.filter
	tk := g.c.Ticket(&g.seq, TicketWorkspace)
	if ws == "" {
		g.cur, g.status = nil, "no workspace: enter one to see its map"
		g.title()
		return
	}
	g.status = "reading " + ws + "…"
	g.title()
	g.c.Go(func(ctx context.Context) any {
		opts := map[string]any{}
		if k := globeFilters[filter].kinds; k != nil {
			opts["kinds"] = k
		}
		res, err := g.c.Call(ctx, "graph.map", ws, opts)
		if err != nil {
			return err
		}
		m := decodeMap(res)
		g.c.Post(func() {
			if tk.Current() && g.open {
				g.status = fmt.Sprintf("laying out %d documents…", len(m.Nodes))
				g.title()
			}
		})
		gl, err := buildGlobe(ctx, ws, m)
		if err != nil {
			return err
		}
		return gl
	}, func(v any) {
		if !tk.Current() || !g.open {
			return
		}
		g.status = ""
		switch v := v.(type) {
		case *globe:
			g.cur, g.moved, g.hover = v, false, -1
		case error:
			g.c.Failed("GlobeMap", v)
		}
		g.title()
	})
}

// title says what the panel shows, and redraws it.
func (g *globeMap) title() {
	t := "GlobeMap"
	switch {
	case g.status != "":
		t += " · " + g.status
	case g.cur != nil:
		t += fmt.Sprintf(" · %s · %d documents, %d links · %s · f filter, + - zoom, 0 fit",
			g.cur.ws, len(g.cur.m.Nodes), len(g.cur.m.Edges), globeFilters[g.filter].name)
	}
	g.c.Set("App.globeMapTitle", t)
	g.dirty()
}

func (g *globeMap) dirty() {
	if g.view != nil && g.view.ctx != nil {
		g.view.ctx.MarkDirty()
	}
}

// fit zooms and centres the build in the view, until the user pans or zooms.
func (g *globeMap) fit(view gui.Size) {
	gl := g.cur
	if gl == nil || g.moved || view.W <= 0 || view.H <= 0 {
		return
	}
	const pad = 24
	w, h := max(gl.size.W, 1)+2*gl.r, max(gl.size.H, 1)+2*gl.r
	g.zoom = max(min((view.W-2*pad)/w, (view.H-2*pad)/h), 1e-4)
	g.off = gui.Pt((view.W-gl.size.W*g.zoom)/2, (view.H-gl.size.H*g.zoom)/2)
}

// toLayout is a view point in layout units.
func (g *globeMap) toLayout(p gui.Point) graphlayout.Point {
	return graphlayout.Point{X: (p.X - g.off.X) / g.zoom, Y: (p.Y - g.off.Y) / g.zoom}
}

// hit is the document under the view point p, or -1: within its dot, or a few pixels of it.
func (g *globeMap) hit(p gui.Point) int {
	gl := g.cur
	if gl == nil || g.zoom <= 0 {
		return -1
	}
	return gl.grid.nearest(gl.at, g.toLayout(p), min(max(gl.r, 6/g.zoom), gl.grid.cell))
}

// zoomBy zooms by f about the view point p.
func (g *globeMap) zoomBy(f float32, p gui.Point) {
	z := min(max(g.zoom*f, 1e-4), 1e3)
	f = z / g.zoom
	g.zoom, g.moved = z, true
	g.off = gui.Pt(p.X-(p.X-g.off.X)*f, p.Y-(p.Y-g.off.Y)*f)
	g.dirty()
}

// press opens document i, the panel closed first.
func (g *globeMap) press(i int) {
	ws, p := g.cur.ws, g.cur.m.Nodes[i].Path
	if err := g.c.View(globeMapPanelID, "close"); err != nil {
		g.c.Failed("GlobeMap: open", err)
	}
	g.c.OpenRef(ws, p)
}

// globeView is the GlobeMap's native view, in its panel.
type globeView struct {
	g     *globeMap
	pal   relPalette
	ctx   *tuicore.Context
	cells tuicore.Size
}

func (g *globeMap) newView(p relPalette) tuicore.Component {
	v := &globeView{g: g, pal: p}
	g.view = v
	return v
}

func (v *globeView) Init(ctx *tuicore.Context) { v.ctx = ctx }
func (v *globeView) Layout(c tuicore.Constraints) tuicore.Size {
	v.cells = c.Constrain(tuicore.Size{W: c.MaxW, H: c.MaxH})
	return v.cells
}
func (*globeView) Render(tuicore.Surface) {}
func (*globeView) AcceptsFocus() bool     { return true }

// pixels is the view's size, and p's place in it, in logical pixels.
func (v *globeView) pixels() gui.Size {
	cp := v.ctx.CellPixels()
	return gui.Size{W: float32(v.cells.W) * cp.W, H: float32(v.cells.H) * cp.H}
}

func (v *globeView) point(e tuicore.MouseEvent) gui.Point {
	cp := v.ctx.CellPixels()
	return gui.Pt((float32(e.X)+e.SubX)*cp.W, (float32(e.Y)+e.SubY)*cp.H)
}

func (v *globeView) HandleEvent(ev tuicore.Event) bool {
	g := v.g
	switch e := ev.(type) {
	case tuicore.MouseEvent:
		p := v.point(e)
		g.fit(v.pixels())
		switch e.Kind {
		case tuicore.MousePress:
			// a drag holds the pointer, so its release ends it wherever it happens
			if e.Button == tuicore.MouseLeft && v.ctx.CapturePointer() {
				g.drag = &globeDrag{from: p, off: g.off}
				v.ctx.RequestFocus()
			}
		case tuicore.MouseMotion:
			if d := g.drag; d != nil {
				dx, dy := p.X-d.from.X, p.Y-d.from.Y
				if d.moved || dx*dx+dy*dy > 9 {
					d.moved, g.moved = true, true
					g.off = gui.Pt(d.off.X+dx, d.off.Y+dy)
					g.dirty()
				}
			} else if i := g.hit(p); i != g.hover {
				g.hover = i
				g.dirty()
			}
		case tuicore.MouseRelease:
			if d := g.drag; d != nil {
				g.drag = nil
				v.ctx.ReleasePointer()
				if i := g.hit(p); !d.moved && i >= 0 {
					g.press(i)
				}
			}
		case tuicore.MouseWheel:
			switch e.Button {
			case tuicore.WheelUp:
				g.zoomBy(1.15, p)
			case tuicore.WheelDown:
				g.zoomBy(1/1.15, p)
			}
		}
		return true
	case tuicore.PointerCaptureLostEvent:
		g.drag = nil // hidden, unmounted, the window's focus lost: the gesture is over
		return true
	case tuicore.KeyEvent:
		if e.Kind != tuicore.KeyPress {
			return false
		}
		view := v.pixels()
		g.fit(view)
		centre := gui.Pt(view.W/2, view.H/2)
		switch e.Text {
		case "+", "=":
			g.zoomBy(1.25, centre)
		case "-":
			g.zoomBy(1/1.25, centre)
		case "0":
			g.moved = false
			g.dirty()
		case "f":
			g.filter = (g.filter + 1) % len(globeFilters)
			g.load()
		default:
			return false
		}
		return true
	}
	return false
}

// NativeView draws the build as it is now: what it reads is taken on the loop, here, so the paint
// shares nothing that changes.
func (v *globeView) NativeView() (any, bool) {
	g := v.g
	g.fit(v.pixels())
	f := globeFrame{gl: g.cur, zoom: g.zoom, off: g.off, hover: g.hover, status: g.status, pal: v.pal}
	return gui.ViewFunc(f.paint), true
}

// globeFrame is one frame of the GlobeMap.
type globeFrame struct {
	gl     *globe
	zoom   float32
	off    gui.Point
	hover  int
	status string
	pal    relPalette
}

// paint draws the map in a constant number of operations, whatever its size: one stroke per edge
// class, one fill per document type, one stroke of the superseded rings, and the hovered
// document's name.
func (f globeFrame) paint(c gui.Canvas) {
	sz := c.Size()
	c.FillRect(gui.Rect{W: sz.W, H: sz.H}, gui.Solid(f.pal.background))
	gl := f.gl
	if gl == nil || f.zoom <= 0 {
		f.say(c, f.status)
		return
	}
	edgeColour := [edgeClasses]color.NRGBA{
		mixNRGBA(f.pal.background, f.pal.muted, 0.8),
		mixNRGBA(f.pal.background, f.pal.superseded, 0.7),
		mixNRGBA(f.pal.background, f.pal.relation, 0.7),
	}
	c.PushTransform(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(f.zoom, f.zoom)).Offset(f32.Pt(f.off.X, f.off.Y)))
	for k, p := range gl.edges {
		if p.Segments() > 0 {
			c.StrokePath(p, 1/f.zoom, gui.Solid(edgeColour[k]))
		}
	}
	for k, p := range gl.dots {
		col := f.pal.text
		if k < len(relationTypes) {
			col = f.pal.types[relationTypes[k]]
		}
		if p.Segments() > 0 {
			c.FillPath(p, gui.Solid(col))
		}
	}
	if gl.rings.Segments() > 0 {
		c.StrokePath(gl.rings, 1.5/f.zoom, gui.Solid(f.pal.superseded))
	}
	c.Pop()
	if f.hover >= 0 && f.hover < len(gl.at) {
		at := gl.at[f.hover]
		t := c.Text().Layout(gl.m.label(f.hover), gui.Font{Size: 13}, 480)
		x, y := at.X*f.zoom+f.off.X+gl.r*f.zoom+6, at.Y*f.zoom+f.off.Y-t.Height/2
		c.FillRRect(gui.Rect{X: x - 4, Y: y - 2, W: t.Width + 8, H: t.Height + 4}, 4, gui.Solid(mixNRGBA(f.pal.background, f.pal.text, 0.12)))
		c.DrawText(t, gui.Pt(x, y), gui.Solid(f.pal.text))
	}
	f.say(c, f.status)
}

// say writes a status line at the view's top left, when there is one.
func (f globeFrame) say(c gui.Canvas, s string) {
	if s == "" {
		return
	}
	t := c.Text().Layout(s, gui.Font{Size: 13}, c.Size().W-24)
	c.DrawText(t, gui.Pt(12, 10), gui.Solid(f.pal.muted))
}
