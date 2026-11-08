package tui

import (
	"context"
	"fmt"
	"image/color"
	"maps"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
	"github.com/yongjohnlee80/golib/tui/style"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE MINDMAP IN CELLS — a terminal's Relations › Mindmap, in either build (a GUI build run with
// --ui too; relationViewFeatures chooses it at run time, a window keeping its own, mindmap_gui.go): the page's document and the documents
// one link from it (d: two links, and back), as the window's mind map (mindmap_gui.go) drawn in
// box-drawing cells (golib tui/mermaid). The keys move a selection from box to box: h j k l and the
// arrows go to the nearest box that way, Enter opens its document, Ctrl+d and Ctrl+u scroll. A
// document wears its group's outline and colour, the window's groups (groups.go), kept in the
// workspace's graph file (graphstore.go); a document in no group is a rounded box, the root a bang.
// Each colour is made legible on the page it is painted on (onGround), whatever the theme.
// Relations › Mindmap settings… is the window's dialog (qmlrelations/MindmapSettings.qml) less its
// theme: the depth the map opens at (tui.mindmap.depth) and the groups (groupeditor.go).

// The Mindmap's settings, in either build: the window's choose its theme too (mindsettings_gui.go).
const (
	prefMindTheme = "tui.mindmap.theme"
	prefMindDepth = "tui.mindmap.depth"
)

// mindChoices are the Mindmap's settings' fixed choosers, by their App.* source (language.go's
// choiceLabels): each row's key its English, its label in the UI's language.
func mindChoices() map[string][]string {
	shapes := make([]string, len(mindShapes))
	for i, s := range mindShapes {
		shapes[i] = s.name
	}
	return map[string][]string{"App.mindDepths": {"one link out", "two links out"}, "App.mindShapeChoices": shapes}
}

// mindDepth is the depth the Mindmap's settings open it at: 1, or 2.
func mindDepth(c Core) int {
	if c.Prefs().Value(prefMindDepth) == "2" {
		return 2
	}
	return 1
}

// cellMind is one build: everything derived from one graph.map answer, made off the loop and
// never changed once installed.
type cellMind struct {
	root fileRef
	m    relMap
	grid *tuimermaid.Grid // laid out from mindmapModel(m, root, …): Boxes[i].ID names m's documents
	// tints are the boxes' groups' colours, by Box.ID, as the graph file has them: a box in no group
	// has none
	tints map[string]color.NRGBA
}

// cellMindmap is the Mindmap feature in cells: its builds, fenced (load), the selection and the
// view's scroll.
type cellMindmap struct {
	c         Core
	seq       uint64
	open      bool
	page      Page      // the page shown, as Shown last said
	cur       *cellMind // the build shown; nil until one is installed
	status    string    // what a load says, a catalog id statusArg fills; "" none
	statusArg string
	view      *cellMindView
	depth     int                   // the links out from the root it shows: 1, or 2 (d toggles)
	selected  int                   // a Boxes index: the root once a build is installed
	x0, y0    int                   // the scroll: the grid's cell at the view's top left
	opens     func(ws, path string) // Enter's: the panel hidden, the document opened
	store     *graphStore           // the workspaces' graph files: the groups; nil for none
	editor    *groupEditor          // the settings' groups
	groups    *relGroups            // groupsWS's groups: each document's outline and colour
	groupsWS  string
	ground    color.NRGBA // the page's background, as last painted: what new groups are legible on
}

func newCellMindmap(c Core, store *graphStore) *cellMindmap {
	m := &cellMindmap{c: c, depth: 1, store: store, ground: themeGround(nil)}
	m.editor = &groupEditor{c: c, store: store, view: viewMindmap, name: "mind", shapes: true,
		changed: m.groupsEdited, ground: func() color.NRGBA { return m.ground }}
	m.opens = func(ws, path string) {
		m.c.HidePanel(mindmapPanelID) // hidden, not closed, as the window's: the dock shows it again
		m.c.OpenRef(ws, path)
	}
	return m
}

func (*cellMindmap) Name() string { return "mindmap" }

func (m *cellMindmap) Panels() []PanelSpec {
	return []PanelSpec{{ID: mindmapPanelID, View: mindmapViewID, Priority: 71, Icon: "⎇",
		Edge: func(prefs) string { return "center" },
		Opened: func(PanelHost) {
			m.open, m.depth = true, mindDepth(m.c) // it opens at the depth its settings choose
			m.load()
		},
		Closed: func(c PanelHost) {
			m.open = false
			m.seq++ // a build under way is dropped
			c.Keep(c.RestoreKeyboardHome())
		}}}
}

func (*cellMindmap) Prefs() []PrefSpec {
	return []PrefSpec{ValuePref(prefMindDepth, "1", []string{"1", "2"}).chooser("App.mindDepthIndex", "App.setMindDepth")}
}

// State is the panel's heading and the settings' sources: the theme chooser, which a terminal
// hides, has no rows.
func (m *cellMindmap) State() map[string]any {
	st := m.editor.state()
	maps.Copy(st, map[string]any{
		"App.mindmapTitle": "Mindmap", "App.mindThemes": choices(), "App.mindThemeIndex": 0,
		"App.mindDepths": choiceModel(m.c, "App.mindDepths"), "App.mindShapeChoices": choiceModel(m.c, "App.mindShapeChoices"),
	})
	return st
}

func (m *cellMindmap) Commands() map[string]Command {
	c := m.editor.commands()
	c["App.toggleMindmap"] = none(m.toggle)
	c["App.openMindmapSettings"] = none(m.openSettings)
	c["App.setMindTheme"] = oneNumber("a row", func(int) {}) // a terminal's map is in the page's theme
	return c
}

func (m *cellMindmap) Catalog() []Entry {
	return []Entry{builtin("mindmap.toggle", "the Mindmap", "App.toggleMindmap()", m.toggle),
		builtin("mindmap.settings", "the Mindmap's settings", "App.openMindmapSettings()", m.openSettings)}
}

// openSettings is Relations › Mindmap settings…: the workspace's groups, read first when this
// workspace's are not yet (the page's neighbourhood names the schema's types).
func (m *cellMindmap) openSettings() {
	ws := m.c.Workspace()
	m.c.Open("mindmapSettings")
	if ws == "" || (m.groupsWS == ws && m.editor.ws == ws && !m.editor.loading) {
		m.editor.show("") // its groups listed already: as they are
		return
	}
	var types []string
	if m.cur != nil && m.cur.root.ws == ws {
		types = m.cur.m.Types
	}
	m.editor.loadingFor(ws) // not the last workspace's groups while ws's are read
	m.readGroups(ws, types)
}

// groupsEdited lays the map out again in list: the settings' edit.
func (m *cellMindmap) groupsEdited(list []relGroup) {
	gs := newRelGroups(list)
	m.groups = &gs
	if m.open {
		m.load()
	}
}

func (m *cellMindmap) toggle() { m.c.TogglePanel(mindmapPanelID) }

// Shown roots the map at the page shown, while it is open.
func (m *cellMindmap) Shown(p Page) {
	m.page = p
	if m.open {
		m.load()
	}
}

func (*cellMindmap) Edited(Page)    {}
func (*cellMindmap) Moved(int, int) {}
func (*cellMindmap) Reread(Page)    {}
func (*cellMindmap) Closed()        {}

// LanguageChanged composes again, in the UI's language, the heading and the settings' choosers'
// rows.
func (m *cellMindmap) LanguageChanged() {
	for _, src := range []string{"App.mindDepths", "App.mindShapeChoices"} {
		m.c.Set(src, choiceModel(m.c, src))
	}
	m.title()
}

// toggleDepth is d: two links out, or one again, read afresh at the page shown.
func (m *cellMindmap) toggleDepth() {
	m.depth = 3 - m.depth
	m.load()
}

// cellMeasure measures in the cells a terminal draws text in: a layout off the loop has no
// surface to ask.
type cellMeasure struct{}

func (cellMeasure) StringWidth(s string) int { return tuicore.StringWidth(s) }

// cellMindShape is a document's outline in cells: the root a bang, every other a rounded box.
func cellMindShape(root string) func(mapNode) pm.MindShape {
	return func(n mapNode) pm.MindShape {
		if n.Path == root {
			return pm.MindBang
		}
		return pm.MindRounded
	}
}

// cellMindShapeIn is a document's outline in gs: its group's, where its group names one; else
// cellMindShape's.
func cellMindShapeIn(root string, gs *relGroups) func(mapNode) pm.MindShape {
	plain := cellMindShape(root)
	return func(n mapNode) pm.MindShape {
		if i := gs.ofOr(n); i >= 0 && gs.list[i].Shape != pm.MindDefault {
			return gs.list[i].Shape
		}
		return plain(n)
	}
}

// cellMindTints are grid's boxes' colours in gs, by Box.ID: each its document's group's.
func cellMindTints(rm relMap, grid *tuimermaid.Grid, gs *relGroups) map[string]color.NRGBA {
	out := map[string]color.NRGBA{}
	for _, b := range grid.Boxes {
		if i, ok := rm.mindIndex(b.ID); ok {
			if k := gs.ofOr(rm.Nodes[i]); k >= 0 {
				out[b.ID] = gs.list[k].Colour
			}
		}
	}
	return out
}

// readGroups takes ws's groups from the store: its graph file's, or its first, made of the
// schema's types and the workspace's folders, legible on the page last painted. The map is laid
// out again in them.
func (m *cellMindmap) readGroups(ws string, types []string) {
	if m.store == nil {
		return
	}
	m.store.groups(ws, viewMindmap, types, m.ground, func(list []relGroup) {
		if ws != m.c.Workspace() {
			return
		}
		m.editor.listed(ws, types, list)
		m.groupsWS = ws
		m.groupsEdited(list)
	})
}

// load reads the page's document's neighbourhood and lays it out off the loop; on the loop it
// installs the build whole, the root selected and the scroll at the top, or drops it when a newer
// load, another page or the panel's closing came first.
func (m *cellMindmap) load() {
	ref := m.page.Ref
	tk := m.c.Ticket(&m.seq, TicketFile)
	if ref.path == "" || ref.ws == "" || m.page.Outside {
		m.cur, m.status = nil, "autodoc.text.mindmap_gui.open_a_workspace_s_document_to_see_its_mind_map"
		m.title()
		return
	}
	depth := m.depth
	var gs *relGroups
	if m.groupsWS == ref.ws {
		gs = m.groups
	}
	m.status, m.statusArg = "autodoc.text.mindmap_gui.reading", ref.path
	m.title()
	m.c.Go(func(ctx context.Context) any {
		res, err := m.c.Call(ctx, "graph.map", ref.ws, map[string]any{"root": ref.path, "depth": int64(depth)})
		if err != nil {
			return err
		}
		rm := decodeMap(res)
		model := mindmapModel(rm, ref.path, cellMindShapeIn(ref.path, gs))
		if model == nil {
			return fmt.Errorf("%s is not in its own map", ref.path)
		}
		grid, err := tuimermaid.LayMindmap(ctx, model, cellMeasure{})
		if err != nil {
			return err
		}
		return &cellMind{root: ref, m: rm, grid: grid, tints: cellMindTints(rm, grid, gs)}
	}, func(v any) {
		if !tk.Current() || !m.open {
			return
		}
		m.status = ""
		switch v := v.(type) {
		case *cellMind:
			m.cur, m.selected, m.x0, m.y0 = v, 0, 0, 0 // Boxes[0] is the root: mindmapModel places it first
			m.reveal()
			if m.groupsWS != v.root.ws {
				m.readGroups(v.root.ws, v.m.Types) // laid out again in them once read
			}
		case error:
			if code(v) == rpc.CodeNotFound {
				m.cur, m.status, m.statusArg = nil, "autodoc.text.mindmap_gui.is_not_indexed_yet", ref.path
			} else {
				m.c.Failed("Mindmap", v)
			}
		}
		m.title()
	})
}

// title is the panel's heading: the build's root, its documents and depth and the keys, or what a
// load says.
func (m *cellMindmap) title() {
	t := "Mindmap"
	switch {
	case m.status != "":
		t += " · " + m.c.Trf(m.status, m.statusArg)
	case m.cur != nil:
		t = m.c.Trf("autodoc.text.mindmap_tui.mindmap_documents_depth_keys", m.cur.root.path, len(m.cur.m.Nodes), m.depth)
	}
	m.c.Set("App.mindmapTitle", t)
	m.dirty()
}

func (m *cellMindmap) dirty() {
	if m.view != nil && m.view.ctx != nil {
		m.view.ctx.MarkDirty()
	}
}

// step selects the nearest box from the selected one in direction (dx, dy), by box centres: of
// those whose centre lies that way, the one nearest along it, a cell aside counting twice. None that
// way keeps the selection.
func (m *cellMindmap) step(dx, dy int) {
	if m.cur == nil || len(m.cur.grid.Boxes) == 0 {
		return
	}
	bs := m.cur.grid.Boxes
	cx, cy := bs[m.selected].Centre()
	best, bestScore := -1, 0
	for i, b := range bs {
		if i == m.selected {
			continue
		}
		x, y := b.Centre()
		along, aside := (x-cx)*dx+(y-cy)*dy, (x-cx)*dy+(y-cy)*dx
		if along <= 0 {
			continue
		}
		if aside < 0 {
			aside = -aside
		}
		if score := along + 2*aside; best < 0 || score < bestScore {
			best, bestScore = i, score
		}
	}
	if best >= 0 {
		m.selected = best
		m.reveal()
		m.dirty()
	}
}

// reveal scrolls the selected box into the view.
func (m *cellMindmap) reveal() {
	if m.cur == nil || m.view == nil || m.selected >= len(m.cur.grid.Boxes) {
		return
	}
	w, h := m.view.cells.W, m.view.cells.H
	r := m.cur.grid.Boxes[m.selected].Rect
	at := m.view.left()
	switch {
	case r.X+at < m.x0:
		m.x0 = r.X + at
	case r.X+at+r.W > m.x0+w:
		m.x0 = r.X + at + r.W - w
	}
	switch {
	case r.Y < m.y0:
		m.y0 = r.Y
	case r.Y+r.H > m.y0+h:
		m.y0 = r.Y + r.H - h
	}
	m.x0, m.y0 = max(m.x0, 0), max(m.y0, 0)
}

// scroll moves the view by dy rows, within the map.
func (m *cellMindmap) scroll(dy int) {
	if m.cur == nil || m.view == nil {
		return
	}
	m.y0 = min(max(m.y0+dy, 0), max(m.cur.grid.Size.H-m.view.cells.H, 0))
	m.dirty()
}

// openSelected opens the selected box's document, the panel hidden, as a click in the window does:
// the document mindIndex names by the box's ID.
func (m *cellMindmap) openSelected() {
	cur := m.cur
	if cur == nil || m.selected >= len(cur.grid.Boxes) {
		return
	}
	if i, ok := cur.m.mindIndex(cur.grid.Boxes[m.selected].ID); ok {
		m.opens(cur.root.ws, cur.m.Nodes[i].Path)
	}
}

// cellMindView is the Mindmap's view in its panel: the build painted at the scroll.
type cellMindView struct {
	m     *cellMindmap
	ctx   *tuicore.Context
	cells tuicore.Size
	// tinted is the build whose tints were made legible on ground, as tints: made again when the
	// build or the page's background changes
	tinted *cellMind
	ground color.NRGBA
	tints  map[string]style.ColorSpec
}

func (m *cellMindmap) newView() tuicore.Component {
	v := &cellMindView{m: m}
	m.view = v
	return v
}

// cellMindmapType is the view's QML type, CellMindmap: built once per document, it joins its
// feature, and leaves it when the document drops it.
func (m *cellMindmap) viewType() tuidecl.Type {
	return tuidecl.Type{Name: "CellMindmap", Destroyed: func(c tuicore.Component) {
		if m.view == c {
			m.view = nil
		}
	}, Build: func(b tuidecl.Build) (tuicore.Component, []string, error) {
		if len(b.Children) != 0 {
			return nil, nil, fmt.Errorf("a CellMindmap takes no children (at %s)", b.Pos)
		}
		return m.newView(), nil, nil
	}}
}

func (v *cellMindView) Init(ctx *tuicore.Context) { v.ctx = ctx }

func (v *cellMindView) Layout(c tuicore.Constraints) tuicore.Size {
	v.cells = c.Constrain(tuicore.Size{W: c.MaxW, H: c.MaxH})
	return v.cells
}

func (*cellMindView) AcceptsFocus() bool { return true }

// left is the grid's column in the view: centred across when it is narrower.
func (v *cellMindView) left() int {
	if v.m.cur == nil {
		return 0
	}
	return max((v.cells.W-v.m.cur.grid.Size.W)/2, 0)
}

// cellMindTheme is golib's own diagram theme (mermaid.TokenTheme): the page's tokens on the
// page's own background, so the pane's boxes follow the theme by construction, never the
// terminal's own background (which a foreground-only style punches through).
var cellMindTheme = tuimermaid.TokenTheme()

func (v *cellMindView) Render(s tuicore.Surface) {
	m := v.m
	if m.cur == nil {
		x, st := 1, style.PageText().Foreground(style.TokenTextMuted)
		for g := range tuicore.Graphemes(m.statusText()) {
			s.SetCell(x, 0, g, st)
			x += s.StringWidth(g)
		}
		return
	}
	m.ground = themeGround(s.Theme())
	th := cellMindTheme
	if len(m.cur.tints) > 0 {
		tints := v.legible(m.cur, m.ground)
		th.Tint = func(id string) (style.ColorSpec, bool) { c, ok := tints[id]; return c, ok }
	}
	m.cur.grid.Paint(s, v.left()-m.x0, -m.y0, m.selected, th)
}

// legible is cur's tints made legible on ground (onGround), kept until either changes.
func (v *cellMindView) legible(cur *cellMind, ground color.NRGBA) map[string]style.ColorSpec {
	if v.tinted != cur || v.ground != ground || v.tints == nil {
		v.tints = make(map[string]style.ColorSpec, len(cur.tints))
		for id, c := range cur.tints {
			c = onGround(c, ground)
			v.tints[id] = style.RGB(c.R, c.G, c.B)
		}
		v.tinted, v.ground = cur, ground
	}
	return v.tints
}

// themeGround is th's page background as a colour: black on a dark theme whose background is the
// terminal's own, white on any other; the default theme's for none.
func themeGround(th *style.Theme) color.NRGBA {
	if th == nil {
		d := style.DefaultTheme()
		th = &d
	}
	if r, g, b, ok := th.Color(style.TokenBackground).RGBValues(); ok {
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}
	}
	if dark, _ := th.Dark(); dark {
		return color.NRGBA{A: 0xff}
	}
	return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
}

// statusText is what a load says, in the UI's language; "" none.
func (m *cellMindmap) statusText() string {
	if m.status == "" {
		return ""
	}
	return m.c.Trf(m.status, m.statusArg)
}

func (v *cellMindView) HandleEvent(ev tuicore.Event) bool {
	m := v.m
	k, ok := ev.(tuicore.KeyEvent)
	if !ok || k.Kind != tuicore.KeyPress {
		return false
	}
	half, ctrl := max(v.cells.H/2, 1), k.Mods&tuicore.ModCtrl != 0
	switch {
	case ctrl && k.Code == 'd':
		m.scroll(half)
	case ctrl && k.Code == 'u':
		m.scroll(-half)
	case ctrl:
		return false
	case k.Code == tuicore.KeyLeft || k.Text == "h":
		m.step(-1, 0)
	case k.Code == tuicore.KeyDown || k.Text == "j":
		m.step(0, 1)
	case k.Code == tuicore.KeyUp || k.Text == "k":
		m.step(0, -1)
	case k.Code == tuicore.KeyRight || k.Text == "l":
		m.step(1, 0)
	case k.Code == tuicore.KeyEnter:
		m.openSelected()
	case k.Text == "d":
		m.toggleDepth()
	default:
		return false
	}
	return true
}
