package tui

import (
	"context"
	"fmt"
	"image/color"
	"maps"
	"math"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuibraille "github.com/yongjohnlee80/golib/tui/braille"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/style"
)

// THE GLOBEMAP IN CELLS — a terminal's Relations › GlobeMap, in either build (a GUI build run with
// --ui too; relationViewFeatures chooses it at run time, a window keeping its own, globemap_gui.go):
// every document of the workspace in use and the links among them, placed by the window's seeded
// force simulation (globemap.go) and drawn once it settles, in Braille dots (golib tui/braille): a
// document a dot in its group's colour (groups.go, the workspace's graph file), made legible on the
// page (onGround); a link a line of dots, muted, a supersession and another relation each in a
// colour of their own. The keys move a selection from document to document: h j k l and the arrows
// go to the nearest one that way, its name always shown (the others' once few enough are in view);
// Enter opens it. H J K L and Shift with the arrows pan, + and - zoom, 0 fits it again, f cycles the
// links it follows, g shows or hides the legend (the window's l is a direction here); a press on a
// document opens it, the wheel zooms (docs/decisions/editor.md#the-globemap-in-a-terminal).
// Relations › GlobeMap settings… is the window's dialog less what cells can't use: whether it names
// the documents, the links it opens following, and its groups (groupeditor.go).

// cellGlobeNames is the most documents in view whose names are drawn beside their dots, and
// cellGlobeNameWidth the most cells a name takes, its margins included.
const (
	cellGlobeNames     = 24
	cellGlobeNameWidth = 26
)

// labelCluster is one grapheme cluster of a label and the cells it takes.
type labelCluster struct {
	text  string
	width int
}

// cellLabel is text as clusters, measured as s draws them (a wide one two cells, a cluster with
// combining marks one), cut to at most max cells with "…" and a margin; and the cells it takes.
func cellLabel(s tuicore.Surface, text string, limit int) ([]labelCluster, int) {
	var out []labelCluster
	w := 0
	for c := range tuicore.Graphemes(text) {
		out = append(out, labelCluster{c, max(s.StringWidth(c), 1)})
		w += out[len(out)-1].width
	}
	if w <= limit {
		return out, w
	}
	for w > limit-2 && len(out) > 0 { // room for "… "
		w -= out[len(out)-1].width
		out = out[:len(out)-1]
	}
	return append(out, labelCluster{"…", 1}, labelCluster{" ", 1}), w + 2
}

// clipLine is the segment (x0, y0)–(x1, y1) cut to the dot rectangle [0, w) × [0, h)
// (Liang–Barsky), its ends rounded to dots; ok false when none of it is in the rectangle.
func clipLine(x0, y0, x1, y1, w, h int) (int, int, int, int, bool) {
	fx0, fy0 := float64(x0), float64(y0)
	dx, dy := float64(x1-x0), float64(y1-y0)
	t0, t1 := 0.0, 1.0
	for _, c := range [4][2]float64{{-dx, fx0}, {dx, float64(w-1) - fx0}, {-dy, fy0}, {dy, float64(h-1) - fy0}} {
		p, q := c[0], c[1]
		switch {
		case p == 0:
			if q < 0 {
				return 0, 0, 0, 0, false // parallel to this edge, and outside it
			}
		case p < 0:
			if r := q / p; r > t1 {
				return 0, 0, 0, 0, false
			} else if r > t0 {
				t0 = r
			}
		default:
			if r := q / p; r < t0 {
				return 0, 0, 0, 0, false
			} else if r < t1 {
				t1 = r
			}
		}
	}
	round := func(v float64) int { return int(math.Round(v)) }
	return round(fx0 + t0*dx), round(fy0 + t0*dy), round(fx0 + t1*dx), round(fy0 + t1*dy), true
}

// cellGlobe is one build: one graph.map answer placed as the simulation settled it, made off the
// loop and never changed once installed.
type cellGlobe struct {
	ws     string
	m      relMap
	at     []graphlayout.Point
	bounds graphlayout.Rect
}

// cellGlobeMap is the GlobeMap feature in cells: its builds, fenced (load), the selection and the
// view's place and zoom over the layout.
type cellGlobeMap struct {
	c         feature.Core
	page      int // documents per page of the map (seams.go: globeMapPage); 0 is the daemon's
	seq       uint64
	open      bool
	filter    int // globeFilters' index
	cur       *cellGlobe
	status    string // what a load says, a catalog id statusArg (and statusN) fill; "" none
	statusArg string
	statusN   int
	store     *graphStore  // the workspaces' graph files: the groups; nil for none
	editor    *groupEditor // the settings' groups
	groups    *relGroups   // groupsWS's groups: each document's colour
	groupsWS  string
	view      *cellGlobeView
	selected  int     // a document of cur, by its index in cur.m.Nodes; -1 none
	zoom      float64 // over the fitted scale: 1 is the whole map in view
	cx, cy    float64 // the layout's point at the view's centre
	noLegend  bool    // g hid the legend
	ground    color.NRGBA
	opens     func(ws, path string) // Enter's and a press's: the panel hidden, the document opened
}

func newCellGlobeMap(c feature.Core, store *graphStore, page int) *cellGlobeMap {
	g := &cellGlobeMap{c: c, page: page, store: store, selected: -1, zoom: 1, ground: themeGround(nil)}
	g.editor = &groupEditor{c: c, store: store, view: viewGlobeMap, name: "globe", changed: g.groupsEdited,
		ground: func() color.NRGBA { return g.ground }}
	g.opens = func(ws, path string) {
		g.c.HidePanel(globeMapPanelID) // hidden, not closed, as the window's: the dock shows it again
		g.c.OpenRef(ws, path)
	}
	return g
}

func (*cellGlobeMap) Name() string { return "globemap" }

func (g *cellGlobeMap) Panels() []feature.PanelSpec {
	return []feature.PanelSpec{{ID: globeMapPanelID, View: globeMapViewID, Priority: 70, Icon: "❂",
		Edge: func(feature.PrefsView) string { return "center" },
		Opened: func(feature.PanelHost) {
			g.open, g.filter = true, globeFilterPref(g.c) // it opens following the links its settings choose
			g.load()
		},
		Closed: func(c feature.PanelHost) {
			g.open = false
			g.seq++ // a build under way is dropped
			c.Keep(c.RestoreKeyboardHome())
		}}}
}

func (g *cellGlobeMap) Prefs() []feature.PrefSpec {
	return []feature.PrefSpec{
		feature.ValuePref(prefGlobeNames, "true", []string{"true", "false"}).Chooser("App.globeNamesIndex", "App.setGlobeNames").
			Then(func(feature.PrefsView) { g.dirty() }),
		feature.ValuePref(prefGlobeFilter, globeFilters[0].key, globeFilterKeys()).Chooser("App.globeFilterIndex", "App.setGlobeFilter").
			Then(func(feature.PrefsView) { g.filterChosen() }),
	}
}

// filterChosen: the map shown follows the links chosen in its settings.
func (g *cellGlobeMap) filterChosen() {
	if f := globeFilterPref(g.c); g.open && f != g.filter {
		g.filter = f
		g.load()
	}
}

// globeWindowSources are the settings dialog's sources and commands for what only a window's
// GlobeMap has (a theme, dots, pace, colours): its rows hide in a terminal, and these stand in.
var globeWindowSources = []string{"App.globeThemeIndex", "App.globeDotsIndex", "App.globeSpeedIndex"}

// State is the panel's heading and the settings' sources.
func (g *cellGlobeMap) State() map[string]any {
	st := g.editor.state()
	maps.Copy(st, map[string]any{
		"App.globeMapTitle": "GlobeMap", "App.globeSettingsError": "",
		"App.globeThemes": feature.Choices(), "App.globeDotSizes": feature.Choices(), "App.globeSpeeds": feature.Choices(),
		"App.globeFilters":     relationChoice(g.c, "App.globeFilters"),
		"App.globeOtherColour": "", "App.globeBodyLinkColour": "", "App.globeSupersessionLinkColour": "",
		"App.globeRelationLinkColour": "", "App.globeRingColour": "",
	})
	for _, src := range globeWindowSources {
		st[src] = 0
	}
	return st
}

// Commands are the map's and its settings' (App.openGlobeSettings is the host's in every build,
// which opens these: relationviews_tui.go, globesettings_gui.go).
func (g *cellGlobeMap) Commands() map[string]feature.Command {
	c := g.editor.commands()
	c["App.toggleGlobeMap"] = feature.None(g.toggle)
	for _, name := range []string{"App.setGlobeTheme", "App.setGlobeDots", "App.setGlobeSpeed"} {
		c[name] = feature.OneNumber("a row", func(int) {}) // a window's alone: its row hides here
	}
	c["App.setGlobeColour"] = feature.TwoStrings("a colour's key and #rrggbb", func(string, string) {})
	return c
}

func (g *cellGlobeMap) Catalog() []feature.Entry {
	return []feature.Entry{feature.Builtin("globemap.toggle", "the GlobeMap", "App.toggleGlobeMap()", g.toggle),
		feature.Builtin("globemap.settings", "the GlobeMap's settings", "App.openGlobeSettings()", g.openSettings)}
}

// openSettings is Relations › GlobeMap settings… and Preferences › GlobeMap…: the workspace's
// groups, read first when this workspace's are not yet.
func (g *cellGlobeMap) openSettings() {
	ws := g.c.Workspace()
	g.c.Open("globeMapSettings")
	if ws == "" || (g.groupsWS == ws && g.editor.ws == ws && !g.editor.loading) {
		g.editor.show("") // its groups listed already: as they are
		return
	}
	var types []string
	if g.cur != nil && g.cur.ws == ws {
		types = g.cur.m.Types
	}
	g.editor.loadingFor(ws) // not the last workspace's groups while ws's are read
	g.readGroups(ws, types)
}

// groupsEdited draws in list: the settings' edit.
func (g *cellGlobeMap) groupsEdited(list []Group) {
	gs := newRelGroups(list)
	g.groups = &gs
	g.dirty()
}

func (g *cellGlobeMap) toggle() { g.c.TogglePanel(globeMapPanelID) }

// Entered: another workspace's map, when it shows; its settings' groups are that workspace's, read
// with its map or as the settings open, and nothing is edited meanwhile.
func (g *cellGlobeMap) Entered(ws string) {
	if ws != g.editor.ws {
		g.editor.loadingFor(ws)
	}
	if g.open {
		g.load()
	}
}

// LanguageChanged composes again, in the UI's language, the heading and the settings' filter
// chooser's rows.
func (g *cellGlobeMap) LanguageChanged() {
	g.c.Set("App.globeFilters", relationChoice(g.c, "App.globeFilters"))
	g.title()
}

// load reads the workspace's map and settles it off the loop; on the loop it installs the build
// whole, fitted, its most linked document selected, or drops it when a newer load or the panel's
// closing came first.
func (g *cellGlobeMap) load() {
	ws, filter := g.c.Workspace(), g.filter
	tk := g.c.Ticket(&g.seq, feature.TicketWorkspace)
	if ws == "" {
		g.cur, g.status = nil, "autodoc.text.globemap_gui.no_workspace_enter_one_to_see_its_map"
		g.title()
		return
	}
	g.status, g.statusArg = "autodoc.text.globemap_gui.reading", ws
	g.title()
	g.c.Go(func(ctx context.Context) any {
		opts := map[string]any{}
		if k := globeFilters[filter].kinds; k != nil {
			opts["kinds"] = k
		}
		if g.page > 0 {
			opts["limit"] = int64(g.page)
		}
		m, err := readMap(func(o map[string]any) (any, error) { return g.c.Call(ctx, "graph.map", ws, o) }, opts,
			func(docs int) {
				g.c.Post(func() {
					if tk.Current() && g.open {
						g.status, g.statusArg, g.statusN = "autodoc.text.globemap_gui.reading_documents", ws, docs
						g.title()
					}
				})
			})
		if err != nil {
			return err
		}
		sim, err := graphlayout.NewSimulation(globeForceInput(m), globeLimits)
		if err != nil {
			return err
		}
		if _, err := sim.Step(ctx, globeSteps); err != nil {
			return err
		}
		at, bounds, err := sim.Frame(nil)
		if err != nil {
			return err
		}
		return &cellGlobe{ws: ws, m: m, at: at, bounds: bounds}
	}, func(v any) {
		if !tk.Current() || !g.open {
			return
		}
		g.status = ""
		switch v := v.(type) {
		case *cellGlobe:
			g.cur, g.zoom, g.selected = v, 1, hub(v.m)
			g.fit()
			if g.groupsWS != v.ws {
				g.readGroups(v.ws, v.m.Types)
			}
		case error:
			g.c.Failed("GlobeMap", v)
		}
		g.title()
	})
}

// hub is m's most linked document, the first of equals; -1 for none.
func hub(m relMap) int {
	best, most := -1, -1
	for i, d := range degrees(m) {
		if d > most {
			best, most = i, d
		}
	}
	return best
}

// readGroups takes ws's groups from the store: its graph file's, or its first, made of the
// schema's types and the workspace's folders, legible on the page last painted.
func (g *cellGlobeMap) readGroups(ws string, types []string) {
	if g.store == nil {
		return
	}
	g.store.groups(ws, viewGlobeMap, types, g.ground, func(list []Group) {
		if ws != g.c.Workspace() {
			return
		}
		g.editor.listed(ws, types, list)
		g.groupsWS = ws
		g.groupsEdited(list)
	})
}

// title is the panel's heading: the workspace, its documents and links, the links followed and the
// keys, or what a load says.
func (g *cellGlobeMap) title() {
	t := "GlobeMap"
	switch {
	case g.status == "autodoc.text.globemap_gui.reading_documents":
		t += " · " + g.c.Trf(g.status, g.statusArg, g.statusN)
	case g.status != "":
		t += " · " + g.c.Trf(g.status, g.statusArg)
	case g.cur != nil:
		t = g.c.Trf("autodoc.text.globemap_gui.globemap_documents_links", g.cur.ws, len(g.cur.m.Nodes), len(g.cur.m.Edges),
			g.c.Tr(globeFilters[g.filter].nameMsg), g.c.Tr("autodoc.text.globemap_tui.keys"))
	}
	g.c.Set("App.globeMapTitle", t)
	g.dirty()
}

func (g *cellGlobeMap) dirty() {
	if g.view != nil && g.view.ctx != nil {
		g.view.ctx.MarkDirty()
	}
}

// scale is dots per layout unit: the fitted scale, with a margin of a cell, times the zoom.
func (g *cellGlobeMap) scale() float64 {
	if g.cur == nil || g.view == nil {
		return 1
	}
	dw, dh := float64(2*g.view.cells.W-4), float64(4*g.view.cells.H-8)
	b := g.cur.bounds
	fit := math.Min(dw/math.Max(float64(b.W), 1), dh/math.Max(float64(b.H), 1))
	return math.Max(fit, 1e-6) * g.zoom
}

// fit puts the whole map in view, about its centre.
func (g *cellGlobeMap) fit() {
	if g.cur == nil {
		return
	}
	b := g.cur.bounds
	g.zoom, g.cx, g.cy = 1, float64(b.X+b.W/2), float64(b.Y+b.H/2)
	g.dirty()
}

// dot is document i's place in the view's dot space (2 a cell across, 4 down).
func (g *cellGlobeMap) dot(i int) (int, int) {
	s, p := g.scale(), g.cur.at[i]
	w, h := 2*g.view.cells.W, 4*g.view.cells.H
	return int(math.Round(float64(w)/2 + (float64(p.X)-g.cx)*s)), int(math.Round(float64(h)/2 + (float64(p.Y)-g.cy)*s))
}

// cellOf is document i's cell in the view.
func (g *cellGlobeMap) cellOf(i int) (int, int) {
	x, y := g.dot(i)
	return floorDiv(x, 2), floorDiv(y, 4)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// inView reports whether document i's cell is in the view.
func (g *cellGlobeMap) inView(i int) bool {
	x, y := g.cellOf(i)
	return x >= 0 && y >= 0 && x < g.view.cells.W && y < g.view.cells.H
}

// step selects the nearest document from the selected one in direction (dx, dy), by their places
// in the layout: of those that lie that way, the one nearest along it, aside counting twice. None
// that way keeps the selection. The view follows a selection that leaves it.
func (g *cellGlobeMap) step(dx, dy int) {
	if g.cur == nil || g.selected < 0 {
		return
	}
	from := g.cur.at[g.selected]
	best, bestScore := -1, 0.0
	for i, p := range g.cur.at {
		if i == g.selected {
			continue
		}
		x, y := float64(p.X-from.X), float64(p.Y-from.Y)
		along, aside := x*float64(dx)+y*float64(dy), math.Abs(x*float64(dy)+y*float64(dx))
		if along <= 0 {
			continue
		}
		if score := along + 2*aside; best < 0 || score < bestScore {
			best, bestScore = i, score
		}
	}
	if best >= 0 {
		g.selected = best
		g.reveal()
		g.dirty()
	}
}

// reveal centres the view on the selected document when it is out of view.
func (g *cellGlobeMap) reveal() {
	if g.cur == nil || g.view == nil || g.selected < 0 || g.inView(g.selected) {
		return
	}
	p := g.cur.at[g.selected]
	g.cx, g.cy = float64(p.X), float64(p.Y)
}

// pan moves the view a quarter of itself by (dx, dy).
func (g *cellGlobeMap) pan(dx, dy int) {
	if g.cur == nil || g.view == nil {
		return
	}
	s := g.scale()
	g.cx += float64(dx) * float64(2*g.view.cells.W) / 4 / s
	g.cy += float64(dy) * float64(4*g.view.cells.H) / 4 / s
	g.dirty()
}

// zoomBy zooms by f about the view's centre, within a sixteenth and sixty-four times the fit.
func (g *cellGlobeMap) zoomBy(f float64) {
	g.zoom = math.Min(math.Max(g.zoom*f, 1.0/16), 64)
	g.dirty()
}

// openSelected opens the selected document, the panel hidden.
func (g *cellGlobeMap) openSelected() {
	if cur := g.cur; cur != nil && g.selected >= 0 {
		g.opens(cur.ws, cur.m.Nodes[g.selected].Path)
	}
}

// at is the document whose dot is in cell (x, y) or a cell beside it, the nearest; -1 none.
func (g *cellGlobeMap) at(x, y int) int {
	if g.cur == nil || g.view == nil {
		return -1
	}
	best, bestD := -1, 3
	for i := range g.cur.at {
		cx, cy := g.cellOf(i)
		if d := max(absInt(cx-x), absInt(cy-y)); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// cellGlobeView is the GlobeMap's view in its panel.
type cellGlobeView struct {
	g     *cellGlobeMap
	ctx   *tuicore.Context
	cells tuicore.Size
}

func (g *cellGlobeMap) newView() tuicore.Component {
	v := &cellGlobeView{g: g}
	g.view = v
	return v
}

// viewType is the view's QML type, CellGlobeMap: built once per document, it joins its feature,
// and leaves it when the document drops it.
func (g *cellGlobeMap) viewType() tuidecl.Type {
	return tuidecl.Type{Name: "CellGlobeMap", Destroyed: func(c tuicore.Component) {
		if g.view == c {
			g.view = nil
		}
	}, Build: func(b tuidecl.Build) (tuicore.Component, []string, error) {
		if len(b.Children) != 0 {
			return nil, nil, fmt.Errorf("a CellGlobeMap takes no children (at %s)", b.Pos)
		}
		return g.newView(), nil, nil
	}}
}

func (v *cellGlobeView) Init(ctx *tuicore.Context) { v.ctx = ctx }

func (v *cellGlobeView) Layout(c tuicore.Constraints) tuicore.Size {
	v.cells = c.Constrain(tuicore.Size{W: c.MaxW, H: c.MaxH})
	return v.cells
}

func (*cellGlobeView) AcceptsFocus() bool { return true }

// The series a cell's dots are drawn in: a link's class (edgeBody…), else a document's, by its
// index past edgeClasses.
func docSeries(i int) int { return edgeClasses + i }

// edgeStyles are the links' styles by class, on the page: body links muted, supersession the
// warning's colour, any other relation the accent.
var edgeStyles = [edgeClasses]style.Style{
	edgeBody:         style.PageText().Foreground(style.TokenTextMuted),
	edgeSupersession: style.PageText().Foreground(style.TokenWarning),
	edgeRelation:     style.PageText().Foreground(style.TokenAccent),
}

// docStyle is document i's dot's style: its group's colour made legible on ground, a superseded
// one's the error's, any other the document's accent.
func (g *cellGlobeMap) docStyle(i int, ground color.NRGBA) style.Style {
	n := g.cur.m.Nodes[i]
	if k := g.groups.ofOr(n); k >= 0 {
		c := onGround(g.groups.list[k].Colour, ground)
		return style.PageText().Foreground(style.RGB(c.R, c.G, c.B))
	}
	if n.Superseded {
		return style.PageText().Foreground(style.TokenError)
	}
	return style.PageText().Foreground(style.TokenAccentDocument)
}

func (v *cellGlobeView) Render(s tuicore.Surface) {
	g := v.g
	page := style.PageText()
	s.Fill(tuicore.Rect{W: v.cells.W, H: v.cells.H}, " ", page)
	if g.cur == nil {
		x, st := 1, page.Foreground(style.TokenTextMuted)
		for c := range tuicore.Graphemes(g.statusText()) {
			s.SetCell(x, 0, c, st)
			x += s.StringWidth(c)
		}
		return
	}
	g.ground = themeGround(s.Theme())
	cv := tuibraille.New(v.cells.W, v.cells.H)
	dw, dh := 2*v.cells.W, 4*v.cells.H
	for _, e := range g.cur.m.Edges {
		x0, y0 := g.dot(e.Src)
		x1, y1 := g.dot(e.Dst)
		// clipped to the view's dots first: zoomed in, most links run far off the view, and a
		// line walks every dot of its length (golib's Line clipping itself: a follow-up)
		if cx0, cy0, cx1, cy1, ok := clipLine(x0, y0, x1, y1, dw, dh); ok {
			cv.Line(cx0, cy0, cx1, cy1, edgeClass(e.Kind))
		}
	}
	inView := 0
	for i := range g.cur.at { // a dot of two by two over the lines: a document owns its cell
		x, y := g.dot(i)
		for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			cv.Set(x+d[0], y+d[1], docSeries(i))
		}
		if g.inView(i) {
			inView++
		}
	}
	for y := range v.cells.H {
		for x := range v.cells.W {
			r, sr := cv.At(x, y)
			if r == ' ' || sr < 0 {
				continue
			}
			st := page
			if sr < edgeClasses {
				st = edgeStyles[sr]
			} else {
				st = g.docStyle(sr-edgeClasses, g.ground)
			}
			s.SetCell(x, y, string(r), st)
		}
	}
	taken := map[[2]int]bool{} // the cells a name holds: no two names overlap
	name := func(i int, st style.Style) {
		x, y := g.cellOf(i)
		label, w := cellLabel(s, " "+g.cur.m.label(i)+" ", cellGlobeNameWidth)
		at := x + 1
		if at+w > v.cells.W {
			at = x - w
		}
		for k := range w {
			if taken[[2]int{at + k, y}] {
				return
			}
		}
		for k := range w {
			taken[[2]int{at + k, y}] = true
		}
		cx := at
		for _, c := range label { // each cluster at its own width: a wide one takes two cells
			s.SetCell(cx, y, c.text, st)
			cx += c.width
		}
	}
	if g.selected >= 0 && g.inView(g.selected) {
		x, y := g.cellOf(g.selected)
		r, _ := cv.At(x, y)
		s.SetCell(x, y, string(r), g.docStyle(g.selected, g.ground).Reverse(true))
		taken[[2]int{x, y}] = true
		name(g.selected, page.Reverse(true))
	}
	if inView <= cellGlobeNames && g.c.Prefs().Value(prefGlobeNames) != "false" {
		for i := range g.cur.at {
			if i != g.selected && g.inView(i) {
				name(i, page.Foreground(style.TokenTextMuted))
			}
		}
	}
	if !g.noLegend {
		v.legend(s)
	}
}

// legend draws the groups, each a dot in its colour and its words, in the view's top right corner,
// at most groupLegendRows of them and a row saying how many more.
func (v *cellGlobeView) legend(s tuicore.Surface) {
	g := v.g
	if g.groups == nil || len(g.groups.list) == 0 {
		return
	}
	type row struct {
		text string
		st   style.Style
	}
	var rows []row
	for k, gr := range g.groups.list {
		if k == groupLegendRows {
			rows = append(rows, row{g.c.Trf("autodoc.text.globemap_tui.more_groups", len(g.groups.list)-k), style.Style{}})
			break
		}
		c := onGround(gr.Colour, g.ground)
		rows = append(rows, row{groupLabel(gr.Query), style.PageText().Foreground(style.RGB(c.R, c.G, c.B))})
	}
	w := 0
	for _, r := range rows {
		w = max(w, tuicore.StringWidth(r.text)+4)
	}
	x0 := v.cells.W - w
	if x0 < 0 {
		return
	}
	page := style.PageText()
	for y, r := range rows {
		if y >= v.cells.H {
			return
		}
		s.Fill(tuicore.Rect{X: x0, Y: y, W: w, H: 1}, " ", page)
		if r.st != (style.Style{}) {
			s.SetCell(x0+1, y, "●", r.st)
		}
		tuicore.DrawText(s, x0+3, y, r.text, page)
	}
}

// statusText is what a load says, in the UI's language; "" none.
func (g *cellGlobeMap) statusText() string {
	switch g.status {
	case "":
		return ""
	case "autodoc.text.globemap_gui.reading_documents":
		return g.c.Trf(g.status, g.statusArg, g.statusN)
	}
	return g.c.Trf(g.status, g.statusArg)
}

func (v *cellGlobeView) HandleEvent(ev tuicore.Event) bool {
	g := v.g
	switch e := ev.(type) {
	case tuicore.MouseEvent:
		switch {
		case e.Kind == tuicore.MousePress && e.Button == tuicore.MouseLeft:
			if i := g.at(e.X, e.Y); i >= 0 {
				g.selected = i
				g.openSelected()
			}
		case e.Kind == tuicore.MouseWheel && e.Button == tuicore.WheelUp:
			g.zoomBy(1.25)
		case e.Kind == tuicore.MouseWheel && e.Button == tuicore.WheelDown:
			g.zoomBy(1 / 1.25)
		default:
			return false
		}
		return true
	case tuicore.KeyEvent:
		if e.Kind != tuicore.KeyPress || e.Mods&(tuicore.ModCtrl|tuicore.ModAlt) != 0 {
			return false
		}
		shift := e.Mods&tuicore.ModShift != 0
		switch {
		case e.Code == tuicore.KeyLeft && shift || e.Text == "H":
			g.pan(-1, 0)
		case e.Code == tuicore.KeyRight && shift || e.Text == "L":
			g.pan(1, 0)
		case e.Code == tuicore.KeyUp && shift || e.Text == "K":
			g.pan(0, -1)
		case e.Code == tuicore.KeyDown && shift || e.Text == "J":
			g.pan(0, 1)
		case e.Code == tuicore.KeyLeft || e.Text == "h":
			g.step(-1, 0)
		case e.Code == tuicore.KeyRight || e.Text == "l":
			g.step(1, 0)
		case e.Code == tuicore.KeyUp || e.Text == "k":
			g.step(0, -1)
		case e.Code == tuicore.KeyDown || e.Text == "j":
			g.step(0, 1)
		case e.Code == tuicore.KeyEnter:
			g.openSelected()
		case e.Text == "+" || e.Text == "=":
			g.zoomBy(1.25)
		case e.Text == "-":
			g.zoomBy(1 / 1.25)
		case e.Text == "0":
			g.fit()
		case e.Text == "f":
			g.filter = (g.filter + 1) % len(globeFilters)
			g.load()
		case e.Text == "g":
			g.noLegend = !g.noLegend
			g.dirty()
		default:
			return false
		}
		return true
	}
	return false
}

// absInt is n's distance from 0.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
