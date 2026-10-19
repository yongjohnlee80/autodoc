//go:build gui

package tui

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/yongjohnlee80/gio/f32"
	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE GLOBEMAP — Relations › GlobeMap: every document of the workspace in use and the links among
// them, placed by a seeded force simulation (golib graph/layout.Simulation) that is drawn as it
// settles, as Obsidian's graph view. Drag pans, the wheel and + - zoom, 0 fits it again, f cycles
// which links it follows; a press on a document closes the panel and opens it. Dots keep their size
// on screen, so zooming in spreads a cluster out, and the names show once few enough are in view. It
// is read again when it opens, when another workspace is entered, and when the filter changes. How it
// draws (its theme, colours, names, dots and pace) is its settings' (globesettings_gui.go).

// globeLimits bound a build: a knowledge base of twenty thousand documents.
var globeLimits = graphlayout.Limits{MaxNodes: 20000, MaxEdges: 200000, MaxWork: 2_000_000_000}

const (
	globeDot      = 3.5 // a document's dot's radius on screen, in logical pixels, at medium size, before its links add to it
	globeLabelCap = 300 // the most documents in view whose names are drawn
	globeZoomStep = 1.15
	// A node pushes only within globeReach link lengths, and each link is a spring of
	// globeLinkPull: the unlinked documents settle round the linked ones, not on a far ring, and a
	// dense part spreads out rather than drawing tight (golib graph/layout's Reach and LinkPull).
	globeReach    = 8
	globeLinkPull = 0.5
	globeSteps    = 300 // the simulation's steps to settle, which its progress counts
	globeToast    = "globemap"
)

// globeFilter is a choice of the links the GlobeMap follows.
type globeFilter struct {
	key, name string // the stored name (prefGlobeFilter), and the title's
	kinds     []any  // graph.map's kinds; nil every kind
}

// globeFilters are the links the GlobeMap follows, f cycling through them: the relations first (the
// default), every link one press away.
var globeFilters = []globeFilter{
	{"relations", "relations", []any{"supersedes", "superseded_by", "amends", "related", "sources", "adr", "review", "blocked"}},
	{"every", "every link", nil},
	{"body", "body links", []any{"wikilink", "embed", "markdown"}},
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

// globe is one GlobeMap frame: one graph.map answer placed as the simulation has it at one step,
// made off the loop and never changed once handed over.
type globe struct {
	ws     string
	m      relMap
	deg    []int // each document's links: a hub's dot is a little larger
	at     []graphlayout.Point
	bounds graphlayout.Rect // the documents' extent, about the origin
	edges  [edgeClasses]*gui.Path
	grid   hitGrid
	final  bool // the simulation has settled
}

// globeForceInput is the simulation of m: seeded, so one map settles the same way each time.
func globeForceInput(m relMap) graphlayout.ForceInput {
	in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1, Steps: globeSteps, Reach: globeReach, LinkPull: globeLinkPull}
	for _, e := range m.Edges {
		in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
	}
	return in
}

// degrees counts each document's links.
func degrees(m relMap) []int {
	deg := make([]int, len(m.Nodes))
	for _, e := range m.Edges {
		deg[e.Src]++
		deg[e.Dst]++
	}
	return deg
}

// newGlobe is the frame of m placed at at: its edges' paths, and its hit grid.
func newGlobe(ws string, m relMap, deg []int, at []graphlayout.Point, bounds graphlayout.Rect, final bool) *globe {
	gl := &globe{ws: ws, m: m, deg: deg, at: at, bounds: bounds, final: final}
	for k := range gl.edges {
		gl.edges[k] = &gui.Path{}
	}
	for _, e := range m.Edges {
		a, b := at[e.Src], at[e.Dst]
		gl.edges[edgeClass(e.Kind)].MoveTo(gui.Pt(a.X, a.Y)).LineTo(gui.Pt(b.X, b.Y))
	}
	// a cell about the room each document has: the pointer's document is in its cell or a neighbour
	cell := float32(30)
	if n := len(at); n > 0 {
		cell = max(float32(math.Sqrt(float64((bounds.W+30)*(bounds.H+30))/float64(n))), 1)
	}
	gl.grid = newHitGrid(at, bounds, cell)
	return gl
}

// dotRadius is document i's dot's radius on screen at zoom z, the map fitted at fit, the dots at
// scale: a little larger with its links and as the map is zoomed in, never in proportion to the zoom.
func (gl *globe) dotRadius(i int, z, fit, scale float32) float32 {
	grow := float32(math.Pow(float64(min(max(z/max(fit, 1e-6), 1), 16)), 0.25))
	return globeDot * scale * (1 + 0.15*float32(math.Log2(1+float64(gl.deg[i])))) * grow
}

// circle adds the circle round c of radius r to p (gui.Path.Ellipse).
func circle(p *gui.Path, c graphlayout.Point, r float32) {
	p.Ellipse(gui.Rect{X: c.X - r, Y: c.Y - r, W: 2 * r, H: 2 * r})
}

// hitGrid is the documents by square cells of the layout: where the pointer is, the few near it.
type hitGrid struct {
	origin     graphlayout.Point
	cell       float32
	cols, rows int
	cells      [][]int32
}

func newHitGrid(at []graphlayout.Point, bounds graphlayout.Rect, cell float32) hitGrid {
	g := hitGrid{origin: graphlayout.Point{X: bounds.X, Y: bounds.Y}, cell: max(cell, 1)}
	g.cols, g.rows = int(bounds.W/g.cell)+1, int(bounds.H/g.cell)+1
	g.cells = make([][]int32, g.cols*g.rows)
	for i, p := range at {
		c := g.index(p)
		g.cells[c] = append(g.cells[c], int32(i))
	}
	return g
}

func (g hitGrid) index(p graphlayout.Point) int {
	x := min(max(int((p.X-g.origin.X)/g.cell), 0), g.cols-1)
	y := min(max(int((p.Y-g.origin.Y)/g.cell), 0), g.rows-1)
	return y*g.cols + x
}

// nearest is the document nearest p no farther than within, or -1. within is at most a cell: the
// cells round p's are all it looks in.
func (g hitGrid) nearest(at []graphlayout.Point, p graphlayout.Point, within float32) int {
	if len(g.cells) == 0 {
		return -1
	}
	within = min(within, g.cell)
	best, bestD := -1, within*within
	cx := int(math.Floor(float64((p.X - g.origin.X) / g.cell)))
	cy := int(math.Floor(float64((p.Y - g.origin.Y) / g.cell)))
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

// globeDots are the dots and rings of one frame at one zoom bucket in one look and groups, in
// layout units: made again only when one of them changes, so a picture stays one fill per colour
// and one ring stroke.
type globeDots struct {
	of     *globe
	look   *globeLook
	groups *relGroups
	bucket int
	fills  []globeFill // one per colour, in the order the documents first wear it
	rings  *gui.Path   // a ring round each superseded document
}

// globeFill is the dots of one colour.
type globeFill struct {
	c    color.NRGBA
	path *gui.Path
}

// zoomBucket is the zoom's step of globeZoomStep: the dots are made again only when it changes.
func zoomBucket(z float32) int {
	return int(math.Round(math.Log(float64(z)) / math.Log(globeZoomStep)))
}

// dotsAt is gl's dots and rings at zoom z in look l and groups gs, the map fitted at fit: d itself
// when it is already for that frame, step, look and groups.
func dotsAt(d globeDots, gl *globe, l *globeLook, gs *relGroups, z, fit float32) globeDots {
	b := zoomBucket(z)
	if d.of == gl && d.look == l && d.groups == gs && d.bucket == b {
		return d
	}
	bz := float32(math.Pow(globeZoomStep, float64(b))) // the step's zoom: the dots are its size until the next
	d = globeDots{of: gl, look: l, groups: gs, bucket: b, rings: &gui.Path{}}
	fill := map[color.NRGBA]int{}
	for i, n := range gl.m.Nodes {
		c := l.colourOf(n, gs)
		k, ok := fill[c]
		if !ok {
			k, fill[c] = len(d.fills), len(d.fills)
			d.fills = append(d.fills, globeFill{c: c, path: &gui.Path{}})
		}
		r := gl.dotRadius(i, bz, fit, l.dot) / bz // on screen, in layout units
		circle(d.fills[k].path, gl.at[i], r)
		if n.Superseded {
			circle(d.rings, gl.at[i], r*1.7)
		}
	}
	return d
}

// fitView is the zoom that fits bounds in a view of size, and the offset that centres them.
func fitView(b graphlayout.Rect, size gui.Size) (float32, gui.Point) {
	const pad = 32
	z := max(min((size.W-2*pad)/max(b.W, 1), (size.H-2*pad)/max(b.H, 1)), 1e-4)
	return z, gui.Pt((size.W-b.W*z)/2-b.X*z, (size.H-b.H*z)/2-b.Y*z)
}

// globeViewState is the loop's view as a load's pictures take it: its size, its look, its pan and
// zoom once the user moved it (until then each frame is fitted), and the document hovered.
type globeViewState struct {
	size   gui.Size
	look   *globeLook // nil: defaultLook
	groups *relGroups // the workspace's, once read; nil: every document other's colour
	moved  bool
	zoom   float32
	off    gui.Point
	hover  int // the hovered document of the load's map; -1 none
}

func (a globeViewState) same(b globeViewState) bool {
	return a.size == b.size && a.look == b.look && a.groups == b.groups && a.moved == b.moved && a.zoom == b.zoom &&
		a.off == b.off && a.hover == b.hover
}

// lookOf is the view's look.
func (a globeViewState) lookOf() *globeLook { return cmp.Or(a.look, defaultLook) }

// globePicture is one picture pushed: a frame at a view, drawn on the load's goroutine. The names
// and the hovered name are part of it, so they are on screen exactly when it is.
type globePicture struct {
	gl    *globe
	dots  globeDots
	zoom  float32
	off   gui.Point
	look  *globeLook
	hover int
}

// pictureOf is f at view: fitted unless the user moved the view; its dots from d when they serve.
func pictureOf(f *globe, view globeViewState, d globeDots) globePicture {
	fitZ, fitOff := fitView(f.bounds, view.size)
	z, off := fitZ, fitOff
	if view.moved {
		z, off = view.zoom, view.off
	}
	l := view.lookOf()
	return globePicture{gl: f, dots: dotsAt(d, f, l, view.groups, z, fitZ), zoom: z, off: off, look: l, hover: view.hover}
}

// globeNameRow is the line names are set in, in logical pixels: the names of a row are one text
// layout (gui.TextShaper.LayoutRuns), drawn with one DrawText.
const globeNameRow = 16

// nameRows are the names of the documents in a view of size sz, when globeLabelCap or fewer are in
// it: each under its dot, set by rows of globeNameRow, a name that would run into the one before it
// in its row left out. None when more are in view, or when the look names none.
func (p globePicture) nameRows(sz gui.Size) map[int][]gui.TextRun {
	if !p.lookOf().names {
		return nil
	}
	var in []int
	for i, a := range p.gl.at {
		x, y := a.X*p.zoom+p.off.X, a.Y*p.zoom+p.off.Y
		if x >= 0 && y >= 0 && x < sz.W && y < sz.H {
			if in = append(in, i); len(in) > globeLabelCap {
				return nil
			}
		}
	}
	rows := map[int][]gui.TextRun{}
	for _, i := range in {
		a := p.gl.at[i]
		x, y := a.X*p.zoom+p.off.X, a.Y*p.zoom+p.off.Y+globeDot*p.lookOf().dot+2
		text := p.gl.m.label(i)
		w := nameWidth(text)
		row := int(y / globeNameRow)
		rows[row] = append(rows[row], gui.TextRun{Text: text, X: x - w/2})
	}
	for row, runs := range rows {
		slices.SortFunc(runs, func(a, b gui.TextRun) int { return cmp.Compare(a.X, b.X) })
		kept, end := runs[:0], float32(math.Inf(-1))
		for _, r := range runs {
			if r.X >= end { // clear of the name before it
				kept, end = append(kept, r), r.X+nameWidth(r.Text)
			}
		}
		rows[row] = kept
	}
	return rows
}

// nameWidth is about the width of a name in the names' font: a run's spacing, not its drawing.
func nameWidth(s string) float32 { return float32(len([]rune(s)))*6.5 + 8 }

// lookOf is the picture's look.
func (p globePicture) lookOf() *globeLook { return cmp.Or(p.look, defaultLook) }

// paint draws the map in a number of operations bounded by the view and the colours, never by the
// map: the ground, one stroke per edge class, one fill per colour, one stroke of the superseded
// rings, one DrawText per row of names, and the hovered name on its plate.
func (p globePicture) paint(c gui.Canvas) {
	sz := c.Size()
	l := p.lookOf()
	pal := l.pal
	c.FillRect(gui.Rect{W: sz.W, H: sz.H}, gui.Solid(pal.background))
	c.PushTransform(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(p.zoom, p.zoom)).Offset(f32.Pt(p.off.X, p.off.Y)))
	for k, path := range p.gl.edges {
		if path.Segments() > 0 {
			c.StrokePath(path, 1/p.zoom, gui.Solid(l.edges[k]))
		}
	}
	for _, fl := range p.dots.fills {
		if fl.path.Segments() > 0 {
			c.FillPath(fl.path, gui.Solid(fl.c))
		}
	}
	if p.dots.rings.Segments() > 0 {
		c.StrokePath(p.dots.rings, 1.5/p.zoom, gui.Solid(l.ring))
	}
	c.Pop()
	// the names: one DrawText a row, whatever the names in it
	f, muted := gui.Font{Size: 12}, mixNRGBA(pal.background, pal.text, 0.75)
	for row, runs := range p.nameRows(sz) {
		if len(runs) > 0 {
			c.DrawText(c.Text().LayoutRuns(runs, f), gui.Pt(0, float32(row)*globeNameRow), gui.Solid(muted))
		}
	}
	if h := p.hover; h >= 0 && h < len(p.gl.at) {
		a := p.gl.at[h]
		t := c.Text().Layout(p.gl.m.label(h), gui.Font{Size: 13}, 480)
		x, y := a.X*p.zoom+p.off.X+globeDot*l.dot*2+6, a.Y*p.zoom+p.off.Y-t.Height/2
		c.FillRRect(gui.Rect{X: x - 4, Y: y - 2, W: t.Width + 8, H: t.Height + 4}, 4, gui.Solid(mixNRGBA(pal.background, pal.text, 0.12)))
		c.DrawText(t, gui.Pt(x, y), gui.Solid(pal.text))
	}
}

// globeReceipt is a pushed picture's outcome: a *gui.Receipt, or a test's.
type globeReceipt interface {
	Done() <-chan struct{}
	Outcome() gui.Outcome
}

// globePublisher is where pictures go, and how the view draws them: the window's CanvasStream, or
// (no window: a test's host) the newest picture drawn in the view's own paint.
type globePublisher interface {
	Push(size gui.Size, draw func(gui.Canvas)) globeReceipt
	Draw(c gui.Canvas, dst gui.Rect)
	Close()
}

type streamPublisher struct{ cs *gui.CanvasStream }

func (p streamPublisher) Push(size gui.Size, draw func(gui.Canvas)) globeReceipt {
	return p.cs.Push(size, draw)
}
func (p streamPublisher) Draw(c gui.Canvas, dst gui.Rect) { gui.DrawCanvasStream(c, p.cs, dst) }
func (p streamPublisher) Close()                          { p.cs.Close() }

// globeStream is the GlobeMap's one publisher, fenced by generation: each load publishes under the
// generation it began with, and only while that is the newest. The check and the publication are
// one step under mu, and a newer generation begins without waiting for it, so once a load has
// begun no earlier load's picture is published (pushes also never overlap: a stream refuses a
// second recorder, Busy).
type globeStream struct {
	mu     sync.Mutex
	gen    atomic.Uint64
	pub    globePublisher
	screen atomic.Pointer[globeScreen] // what the window shows, or is about to: read by the pointer, the names
}

// globeShot is one picture as pushed: its frame, the transform it was drawn at, the size, and its
// dots' scale.
type globeShot struct {
	f    *globe
	zoom float32
	off  gui.Point
	size gui.Size
	dot  float32
}

// globeScreen is what input may act on: the picture last presented, and every picture pushed since
// that the window may yet submit (a pending one, or one a hidden view would draw when it shows
// again). Written by the load's goroutine under the stream's lock: a picture is added before it is
// published, and retired only when it can no longer be on screen (a later one Drawn, or itself
// Superseded). Read on the loop without waiting for a Post.
type globeScreen struct {
	shown    *globeShot
	possible []*globeShot
}

// begin is a new generation: every earlier one's pushes are refused (Closed) from now on.
func (s *globeStream) begin() uint64 { return s.gen.Add(1) }

// push publishes shot for gen when it is still the newest; otherwise it publishes nothing, Closed.
// The shot is possible before the publication, so input sees it before the window can submit it.
func (s *globeStream) push(gen uint64, shot *globeShot, draw func(gui.Canvas)) globeReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen.Load() != gen {
		return doneReceipt(gui.Closed)
	}
	next := &globeScreen{possible: []*globeShot{shot}}
	if cur := s.screen.Load(); cur != nil {
		next.shown, next.possible = cur.shown, append(slices.Clone(cur.possible), shot)
	}
	s.screen.Store(next)
	return s.pub.Push(shot.size, draw)
}

// presented records shot as on screen (its receipt Drawn): every picture pushed before it is retired
// with it, as the window draws a stream's newest picture only.
func (s *globeStream) presented(gen uint64, shot *globeShot) {
	s.update(gen, func(sc *globeScreen) {
		if i := slices.Index(sc.possible, shot); i >= 0 {
			sc.possible = slices.Clone(sc.possible[i+1:])
		}
		sc.shown = shot
	})
}

// retire drops shot from the possible pictures: it was Superseded, never drawn.
func (s *globeStream) retire(gen uint64, shot *globeShot) {
	s.update(gen, func(sc *globeScreen) {
		sc.possible = slices.DeleteFunc(slices.Clone(sc.possible), func(x *globeShot) bool { return x == shot })
	})
}

// update replaces the screen by a copy edited by fn, for gen while it is the newest.
func (s *globeStream) update(gen uint64, fn func(*globeScreen)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen.Load() != gen {
		return
	}
	next := &globeScreen{}
	if cur := s.screen.Load(); cur != nil {
		*next = *cur
	}
	fn(next)
	s.screen.Store(next)
}

// paintPresenter keeps the newest picture for the view's paint: with no window there is no
// presentation to wait for, so each push is Drawn at once.
type paintPresenter struct {
	mu   sync.Mutex
	draw func(gui.Canvas)
}

func (p *paintPresenter) Push(_ gui.Size, draw func(gui.Canvas)) globeReceipt {
	p.mu.Lock()
	p.draw = draw
	p.mu.Unlock()
	return doneReceipt(gui.Drawn)
}
func (p *paintPresenter) Draw(c gui.Canvas, _ gui.Rect) {
	p.mu.Lock()
	draw := p.draw
	p.mu.Unlock()
	if draw != nil {
		draw(c)
	}
}
func (*paintPresenter) Close() {}

// doneReceipt is a receipt complete with its outcome.
type doneReceipt gui.Outcome

func (doneReceipt) Done() <-chan struct{}  { return closedChan }
func (r doneReceipt) Outcome() gui.Outcome { return gui.Outcome(r) }

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// globeMap is the GlobeMap feature: its loads, fenced and paced by what the window presents (load),
// and the viewer's pan and zoom.
type globeMap struct {
	c       Core
	present func() globePublisher // the publisher, made when the panel first opens
	pres    *globeStream
	seq     uint64
	open    bool
	filter  int        // globeFilters' index
	cur     *globe     // the frame on screen: its picture was presented (hit testing, the names)
	look    *globeLook // how it draws (globesettings_gui.go); nil until first used
	groups  *relGroups // the workspace's groups (graphstore_gui.go); nil until read
	store   *graphStore
	editor  *groupEditor // the settings' groups
	counts  [2]int       // the load's documents and links, once its map is read
	status  string       // what a load under way says; "" none
	stepped int          // the load's simulation steps run, as last posted
	toast   bool         // the load's progress toast is up
	noKey   bool         // l hid the legend
	view    *globeView

	cancel func()              // ends the load under way
	views  chan globeViewState // the load under way's: the view, newest only
	sent   globeViewState      // the view last sent to it
	resend bool                // the load waits for the view to show again: send it on the next paint

	zoom  float32    // the view asked for (view pixels per layout unit), sent to the load
	off   gui.Point  // where the layout's origin is asked to be in the view
	moved bool       // the user panned or zoomed: no longer fitted
	hover int        // the document under the pointer; -1 none
	drag  *globeDrag // a press held on the view: it holds the pointer until released or lost
}

type globeDrag struct {
	from, off gui.Point
	moved     bool
}

func newGlobeMap(c Core, present func() globePublisher) *globeMap {
	g := &globeMap{c: c, present: present, hover: -1}
	g.editor = &groupEditor{c: c, view: viewGlobeMap, name: "globe", changed: g.groupsEdited,
		ground: func() color.NRGBA { return g.currentLook().pal.background }}
	return g
}

// readGroups takes ws's groups from the store, as its map is read: its graph file's, or its first,
// made of types and the workspace's folders. The frame on screen draws in them at once.
func (g *globeMap) readGroups(ws string, types []string) {
	if g.store == nil {
		return
	}
	g.store.groups(ws, viewGlobeMap, types, g.currentLook().pal.background, func(list []relGroup) {
		if ws == g.c.Workspace() {
			g.editor.listed(ws, types, list)
			g.groupsEdited(list)
		}
	})
}

// groupsEdited draws in list: the dots, and the legend.
func (g *globeMap) groupsEdited(list []relGroup) {
	gs := newRelGroups(list)
	g.groups = &gs
	if g.view != nil && g.view.ctx != nil {
		g.sendView(g.view.pixels(), false)
	}
	g.dirty()
}

func (*globeMap) Name() string { return "globemap" }

func (g *globeMap) Panels() []PanelSpec {
	return []PanelSpec{{ID: globeMapPanelID, View: globeMapViewID, Priority: 70, Icon: "❂",
		Edge: func(prefs) string { return "center" },
		Opened: func(PanelHost) {
			g.open, g.filter = true, g.prefFilter() // it opens following the links its settings choose
			g.load()
		},
		Closed: func(c PanelHost) {
			g.open, g.drag = false, nil // a gesture ends with its panel (its capture is lost as it hides)
			g.stop()                    // the load under way ends, and what it still hands over is dropped
			g.told("GlobeMap closed before it settled")
			c.Keep(c.RestoreKeyboardPane("editor"))
		}}}
}

func (g *globeMap) State() map[string]any {
	st := g.settingsState()
	st["App.globeMapTitle"] = "GlobeMap"
	return st
}

func (g *globeMap) Commands() map[string]Command {
	m := g.settingsCommands()
	m["App.toggleGlobeMap"] = none(g.toggle)
	return m
}

func (g *globeMap) Catalog() []Entry {
	return []Entry{builtin("globemap.toggle", "the GlobeMap", "App.toggleGlobeMap()", g.toggle),
		builtin("globemap.settings", "the GlobeMap's settings", "App.openGlobeSettings()", g.openSettings)}
}

func (g *globeMap) toggle() { g.c.TogglePanel(globeMapPanelID) }

// Entered: another workspace's map, when it shows.
func (g *globeMap) Entered(string) {
	if g.open {
		g.load()
	}
}

// Stopped closes the window's stream.
func (g *globeMap) Stopped() {
	g.stop()
	g.toast = false // the window is going: no toast to end
	if g.pres != nil {
		g.pres.pub.Close()
	}
}

// stop ends the load under way: a newer ticket drops what it still posts, a newer generation what it
// still pushes, and its goroutine returns at its next wait (every wait and step is under its
// context).
func (g *globeMap) stop() {
	g.seq++
	if g.pres != nil {
		g.pres.begin()
	}
	if g.cancel != nil {
		g.cancel()
		g.cancel, g.views = nil, nil
	}
}

// viewState is the view as the loop has it now, at size.
func (g *globeMap) viewState(size gui.Size) globeViewState {
	st := globeViewState{size: size, look: g.currentLook(), groups: g.groups, moved: g.moved, hover: g.hover}
	if g.moved {
		st.zoom, st.off = g.zoom, g.off
	}
	return st
}

// sendView gives the load under way the view at size when it changed, or when force: newest only,
// a view it has not taken yet replaced.
func (g *globeMap) sendView(size gui.Size, force bool) {
	st := g.viewState(size)
	if g.views == nil || !force && st.same(g.sent) {
		return
	}
	g.sent = st
	select {
	case <-g.views:
	default:
	}
	g.views <- st
}

// load reads the workspace's map and settles it by simulation on its own goroutine, which also
// draws and pushes each picture. It steps the simulation only once the window has presented the
// last picture (its receipt Drawn, or Superseded by a newer one): the settling shows step by step,
// at the display's pace. A hidden view (NotShown) holds it until the view is drawn again; a closed
// stream ends it. A view change (size, pan, zoom, colours) pushes the same step again. The loop
// takes each frame (hit testing, the names) only while the load is current and the panel open.
func (g *globeMap) load() {
	g.stop()
	ws, filter := g.c.Workspace(), g.filter
	tk := g.c.Ticket(&g.seq, TicketWorkspace)
	if ws == "" {
		g.cur, g.status = nil, "no workspace: enter one to see its map"
		g.title()
		return
	}
	if g.pres == nil {
		g.pres = &globeStream{pub: g.present()}
	}
	pres, gen := g.pres, g.pres.begin()
	g.counts = [2]int{}
	lctx, cancel := context.WithCancel(context.Background())
	views := make(chan globeViewState, 1)
	g.cancel, g.views, g.sent = cancel, views, globeViewState{}
	if g.view != nil && g.view.ctx != nil {
		g.sendView(g.view.pixels(), true)
	}
	g.status, g.stepped = "reading "+ws+"…", 0
	g.tell("GlobeMap: reading " + ws + "'s map…")
	g.title()
	g.c.Go(func(ctx context.Context) any {
		ctx, done := context.WithCancel(ctx)
		defer done()
		defer context.AfterFunc(lctx, done)() // stop() ends a step under way too
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
			if tk.Current() && g.open && g.views == views {
				g.counts, g.status = [2]int{len(m.Nodes), len(m.Edges)}, ""
				g.progress()
				g.readGroups(ws, m.Types)
			}
		})
		sim, err := graphlayout.NewSimulation(globeForceInput(m), globeLimits)
		if err != nil {
			return err
		}
		push := func(shot *globeShot, draw func(gui.Canvas)) globeReceipt { return pres.push(gen, shot, draw) }
		last := -1
		stepped := func(done int) { // a twentieth at a time: the toast and the title, not every picture
			if b := done * 20 / globeSteps; b != last {
				last = b
				g.c.Post(func() {
					if tk.Current() && g.open && g.views == views {
						g.stepped = done
						g.progress()
					}
				})
			}
		}
		return settle(ctx, sim, ws, m, push, views, stepped, func(shot *globeShot) {
			pres.presented(gen, shot) // at once, for input: not through the loop
			g.c.Post(func() {
				if tk.Current() && g.open && g.views == views {
					g.install(shot.f, shot.zoom, shot.off)
				}
			})
		}, func(shot *globeShot) { pres.retire(gen, shot) }, func() {
			g.c.Post(func() { // hidden: when the view is drawn again, it is sent again
				if tk.Current() && g.open && g.views == views {
					g.resend = true
					g.dirty()
				}
			})
		})
	}, func(v any) {
		if err, ok := v.(error); ok && tk.Current() && g.open {
			g.status = ""
			g.told("GlobeMap: " + ws + "'s map could not be laid out")
			g.c.Failed("GlobeMap", err)
			g.title()
		}
	})
}

// settle runs one load's simulation to its end, pacing each step by the presentation of the picture
// before it, and then keeps its last frame drawn as the view changes, until ctx ends: every wait and
// step is under ctx, so the load's cancel ends it wherever it waits. views is the load's own. One
// picture is in flight at a time: a view change waits for the receipt in hand, and the newest goes
// into the next push. shown is a picture Drawn, retired one Superseded, hidden a view not drawn.
func settle(ctx context.Context, sim *graphlayout.Simulation, ws string, m relMap,
	push func(*globeShot, func(gui.Canvas)) globeReceipt, views <-chan globeViewState,
	stepped func(done int), shown, retired func(*globeShot), hidden func()) error {
	done := 0
	var view globeViewState
	select {
	case view = <-views: // the first view: its size and colours
	case <-ctx.Done():
		return nil
	}
	deg := degrees(m)
	var dots globeDots
	frame := func() (*globe, error) {
		at, bounds, err := sim.Frame(nil)
		if err != nil {
			return nil, err
		}
		return newGlobe(ws, m, deg, at, bounds, sim.Done()), nil
	}
	f, err := frame()
	if err != nil {
		return err
	}
	for {
		// push frame f at the view: one picture in flight at a time, so none that the window may
		// have submitted is ever forgotten before its receipt is handled
		if view.size.W > 0 && view.size.H > 0 {
			p := pictureOf(f, view, dots)
			dots = p.dots
			shot := &globeShot{f: f, zoom: p.zoom, off: p.off, size: view.size, dot: p.lookOf().dot}
			r := push(shot, p.paint)
			changed := false
		receipt:
			for {
				select {
				case <-r.Done():
					break receipt
				case view = <-views: // kept for the next push: none before this one is handled
					changed = true
				case <-ctx.Done():
					return nil
				}
			}
			switch r.Outcome() {
			case gui.Closed, gui.Busy:
				return nil // the stream is gone, a newer load holds it, or another push does
			case gui.Drawn:
				shown(shot) // on screen now: every picture before it retired
			case gui.Superseded:
				retired(shot) // never drawn
			case gui.NotShown:
				hidden() // still possible: drawn if the view shows again
				if !changed {
					goto hold
				}
				continue
			}
			if !f.final {
				n := view.lookOf().steps // the settings' pace
				if _, err := sim.Step(ctx, n); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				done += n
				stepped(done)
				if f, err = frame(); err != nil {
					return err
				}
				continue
			}
			if changed {
				continue // settled: the newest view, pushed again
			}
		}
	hold: // settled, hidden, or no size yet: the next push waits for a view change
		select {
		case view = <-views:
		case <-ctx.Done():
			return nil
		}
	}
}

// install takes the frame on screen and the transform its picture was drawn at. Until the user
// pans or zooms, the view asked for follows it (each picture is fitted); a load's first frame fits
// again, and drops the hover.
func (g *globeMap) install(f *globe, zoom float32, off gui.Point) {
	if g.cur == nil || g.cur.ws != f.ws || len(g.cur.m.Nodes) != len(f.m.Nodes) {
		g.moved, g.hover = false, -1
	}
	g.cur, g.status = f, ""
	if !g.moved {
		g.zoom, g.off = zoom, off
	}
	if f.final {
		g.told(fmt.Sprintf("GlobeMap: %s settled · %d documents, %d links", f.ws, len(f.m.Nodes), len(f.m.Edges)))
	}
	g.title()
}

// globeNotifier is the host's ongoing toasts (core's).
type globeNotifier interface {
	NotifyOngoing(id, msg string)
	NotifyDone(id, msg string)
}

// tell puts msg in the load's progress toast, updated in place until told ends it.
func (g *globeMap) tell(msg string) {
	if n, ok := g.c.(globeNotifier); ok {
		n.NotifyOngoing(globeToast, msg)
		g.toast = true
	}
}

// told ends the load's progress toast with msg, when one is up.
func (g *globeMap) told(msg string) {
	if n, ok := g.c.(globeNotifier); ok && g.toast {
		g.toast = false
		n.NotifyDone(globeToast, msg)
	}
}

// progress shows how far the load has settled: in its toast, as a bar, and in the title.
func (g *globeMap) progress() {
	if g.cur == nil || !g.cur.final {
		g.tell(fmt.Sprintf("GlobeMap: laying out %s · %d documents, %d links %s",
			g.c.Workspace(), g.counts[0], g.counts[1], bar(int64(min(g.stepped, globeSteps)), globeSteps)))
	}
	g.title()
}

// title says what the panel shows, and redraws it.
func (g *globeMap) title() {
	t := "GlobeMap"
	switch {
	case g.status != "":
		t += " · " + g.status
	case g.counts != [2]int{} || g.cur != nil:
		state := "f filter, + - zoom, 0 fit, l legend"
		if g.cur == nil || !g.cur.final {
			state = fmt.Sprintf("settling %d%%", min(g.stepped, globeSteps)*100/globeSteps)
		}
		t += fmt.Sprintf(" · %s · %d documents, %d links · %s · %s",
			g.c.Workspace(), g.counts[0], g.counts[1], globeFilters[g.filter].name, state)
	}
	g.c.Set("App.globeMapTitle", t)
	g.dirty()
}

func (g *globeMap) dirty() {
	if g.view != nil && g.view.ctx != nil {
		g.view.ctx.MarkDirty()
	}
}

// at is the document of shot under the view point p, or -1: within its dot, or a few pixels of it.
func (shot *globeShot) at(p gui.Point) int {
	if shot == nil || shot.zoom <= 0 {
		return -1
	}
	q := graphlayout.Point{X: (p.X - shot.off.X) / shot.zoom, Y: (p.Y - shot.off.Y) / shot.zoom}
	return shot.f.grid.nearest(shot.f.at, q, max(2*globeDot*cmp.Or(shot.dot, 1), 8)/shot.zoom)
}

// target is the document under the view point p, in the picture it was found in, and whether the
// screen agrees on it. Every picture the window may be showing (the one presented, and any pushed
// since that is not retired) must find the same document under the pointer, at the view's size (a
// resized slot shows its picture scaled); else the press is refused rather than open what may not
// be under the pointer. One read of the screen: what is pressed is what was found.
func (g *globeMap) target(p gui.Point, size gui.Size) (*globeShot, int, bool) {
	if g.pres == nil {
		return nil, -1, false
	}
	sc := g.pres.screen.Load()
	if sc == nil || sc.shown == nil || sc.shown.size != size {
		return nil, -1, false
	}
	i := sc.shown.at(p)
	for _, x := range sc.possible {
		if x.size != size || x.at(p) != i {
			return nil, -1, false
		}
	}
	return sc.shown, i, true
}

// zoomBy zooms by f about the view point p.
func (g *globeMap) zoomBy(f float32, p gui.Point) {
	z := min(max(g.zoom*f, 1e-4), 1e3)
	f = z / g.zoom
	g.zoom, g.moved = z, true
	g.off = gui.Pt(p.X-(p.X-g.off.X)*f, p.Y-(p.Y-g.off.Y)*f)
	g.dirty()
}

// press opens document i of the picture it was found in, the panel hidden first, not closed: the
// dock shows it again as it was (ADR 1792397302 r3).
func (g *globeMap) press(shot *globeShot, i int) {
	ws, p := shot.f.ws, shot.f.m.Nodes[i].Path
	g.c.HidePanel(globeMapPanelID)
	g.c.OpenRef(ws, p)
}

// globeView is the GlobeMap's native view, in its panel. Its colours are its look's, not the
// document's: the panel takes no colour properties.
type globeView struct {
	g     *globeMap
	ctx   *tuicore.Context
	cells tuicore.Size
}

func (g *globeMap) newView(relPalette) tuicore.Component {
	v := &globeView{g: g}
	g.view = v
	return v
}

func (v *globeView) Init(ctx *tuicore.Context) { v.ctx = ctx }
func (v *globeView) Layout(c tuicore.Constraints) tuicore.Size {
	v.cells = c.Constrain(tuicore.Size{W: c.MaxW, H: c.MaxH})
	return v.cells
}
func (*globeView) AcceptsFocus() bool { return true }

func (*globeView) Render(tuicore.Surface) {}

// pixels is the view's size, in logical pixels.
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
			} else if _, i, _ := g.target(p, v.pixels()); i != g.hover {
				g.hover = i // none while the screen disagrees; the picture draws it
				g.sendView(v.pixels(), false)
			}
		case tuicore.MouseRelease:
			if d := g.drag; d != nil {
				g.drag = nil
				v.ctx.ReleasePointer()
				if shot, i, ok := g.target(p, v.pixels()); !d.moved && ok && i >= 0 {
					g.press(shot, i) // refused while the screen is changing under the pointer
				}
			}
		case tuicore.MouseWheel:
			switch e.Button {
			case tuicore.WheelUp:
				g.zoomBy(globeZoomStep, p)
			case tuicore.WheelDown:
				g.zoomBy(1/globeZoomStep, p)
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
		case "l":
			g.noKey = !g.noKey
			g.dirty()
		default:
			return false
		}
		return true
	}
	return false
}

// NativeView records the view's paint: the load's pictures by the stream's slot, and a status line
// while a load reads. The map, its names and the hovered name are all in the pictures, so they are
// on screen exactly when their picture is. The view as it is now goes to the load when it changed.
func (v *globeView) NativeView() (any, bool) {
	g := v.g
	view := v.pixels()
	g.sendView(view, g.resend)
	g.resend = false
	l := g.currentLook()
	var key []legendRow
	if !g.noKey {
		key = g.legend(l)
	}
	return gui.ViewFunc(globeOverlay{pres: g.pres, status: g.status, pal: l.pal, legend: key}.paint), true
}

// legend is what the GlobeMap's marks stand for in look l: the colour of each group the map shows
// (groupLegend), any other document's, the superseded ring, the links the filter follows, and the
// dots' size.
func (g *globeMap) legend(l *globeLook) []legendRow {
	var m relMap
	if g.cur != nil {
		m = g.cur.m
	}
	rows := groupLegend(g.groups, m, func(gr relGroup) func(gui.Canvas, gui.Rect) { return legendDot(gr.Colour, 8) })
	rows = append(rows, legendRow{legendDot(l.other, 8), "any other document"})
	rows = append(rows, legendRow{legendRing(l.other, l.ring), "superseded"})
	kinds := globeFilters[g.filter].kinds
	shows := func(cls int) bool {
		return kinds == nil || slices.ContainsFunc(kinds, func(k any) bool { s, _ := k.(string); return edgeClass(s) == cls })
	}
	for _, e := range []struct {
		cls  int
		text string
	}{{edgeBody, "body link"}, {edgeSupersession, "supersedes"}, {edgeRelation, "other relation"}} {
		if shows(e.cls) {
			rows = append(rows, legendRow{legendLine(l.edges[e.cls]), e.text})
		}
	}
	return append(rows, legendRow{func(c gui.Canvas, b gui.Rect) {
		legendDot(l.other, 5)(c, gui.Rect{X: b.X, Y: b.Y, W: b.W / 2, H: b.H})
		legendDot(l.other, 10)(c, gui.Rect{X: b.X + b.W/2, Y: b.Y, W: b.W / 2, H: b.H})
	}, "larger: more links"})
}

// globeOverlay is the view's own paint over the load's pictures.
type globeOverlay struct {
	pres   *globeStream
	status string
	pal    relPalette
	legend []legendRow // nil: hidden
}

// paint draws the stream's slot (or the ground before there is a stream), and the status line.
func (o globeOverlay) paint(c gui.Canvas) {
	sz := c.Size()
	if o.pres != nil {
		o.pres.pub.Draw(c, gui.Rect{W: sz.W, H: sz.H})
	} else {
		c.FillRect(gui.Rect{W: sz.W, H: sz.H}, gui.Solid(o.pal.background))
	}
	if o.status != "" {
		t := c.Text().Layout(o.status, gui.Font{Size: 13}, sz.W-24)
		c.DrawText(t, gui.Pt(12, 10), gui.Solid(o.pal.muted))
	}
	paintLegend(c, sz, o.pal, o.legend)
}
