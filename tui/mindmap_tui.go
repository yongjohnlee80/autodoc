//go:build !gui

package tui

import (
	"context"
	"fmt"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
	"github.com/yongjohnlee80/golib/tui/style"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE MINDMAP IN CELLS — a terminal's Relations › Mindmap: the page's document and the documents
// one link from it (d: two links, and back), as the window's mind map (mindmap_gui.go) drawn in
// box-drawing cells (golib tui/mermaid). The keys move a selection from box to box: h j k l and the
// arrows go to the nearest box that way, Enter opens its document, Ctrl+d and Ctrl+u scroll. Every
// document is a rounded box and the root a bang, until a terminal has a graph store for the
// window's groups and their shapes (docs/decisions/relations.md).

// cellMind is one build: everything derived from one graph.map answer, made off the loop and
// never changed once installed.
type cellMind struct {
	root fileRef
	m    relMap
	grid *tuimermaid.Grid // laid out from mindmapModel(m, root, …): Boxes[i].ID names m's documents
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
}

func newCellMindmap(c Core) *cellMindmap {
	m := &cellMindmap{c: c, depth: 1}
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
			m.open, m.depth = true, 1
			m.load()
		},
		Closed: func(c PanelHost) {
			m.open = false
			m.seq++ // a build under way is dropped
			c.Keep(c.RestoreKeyboardHome())
		}}}
}

func (m *cellMindmap) State() map[string]any { return map[string]any{"App.mindmapTitle": "Mindmap"} }

func (m *cellMindmap) Commands() map[string]Command {
	return map[string]Command{"App.toggleMindmap": none(m.toggle)}
}

func (m *cellMindmap) Catalog() []Entry {
	return []Entry{builtin("mindmap.toggle", "the Mindmap", "App.toggleMindmap()", m.toggle)}
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

// LanguageChanged composes the heading again in the UI's language.
func (m *cellMindmap) LanguageChanged() { m.title() }

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
	m.status, m.statusArg = "autodoc.text.mindmap_gui.reading", ref.path
	m.title()
	m.c.Go(func(ctx context.Context) any {
		res, err := m.c.Call(ctx, "graph.map", ref.ws, map[string]any{"root": ref.path, "depth": int64(depth)})
		if err != nil {
			return err
		}
		rm := decodeMap(res)
		model := mindmapModel(rm, ref.path, cellMindShape(ref.path))
		if model == nil {
			return fmt.Errorf("%s is not in its own map", ref.path)
		}
		grid, err := tuimermaid.LayMindmap(ctx, model, cellMeasure{})
		if err != nil {
			return err
		}
		return &cellMind{root: ref, m: rm, grid: grid}
	}, func(v any) {
		if !tk.Current() || !m.open {
			return
		}
		m.status = ""
		switch v := v.(type) {
		case *cellMind:
			m.cur, m.selected, m.x0, m.y0 = v, 0, 0, 0 // Boxes[0] is the root: mindmapModel places it first
			m.reveal()
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

// cellMindTheme is the painter's styles in the theme's tokens: lines and outlines a border's, labels
// the text's, the selected box bold in the accent.
var cellMindTheme = tuimermaid.Theme{
	Line:     style.New().Foreground(style.TokenBorder),
	Box:      style.New().Foreground(style.TokenBorder),
	Label:    style.New().Foreground(style.TokenForeground),
	Selected: style.New().Foreground(style.TokenAccent).Bold(true),
}

func (v *cellMindView) Render(s tuicore.Surface) {
	m := v.m
	if m.cur == nil {
		x, st := 1, style.New().Foreground(style.TokenTextMuted)
		for g := range tuicore.Graphemes(m.statusText()) {
			s.SetCell(x, 0, g, st)
			x += s.StringWidth(g)
		}
		return
	}
	m.cur.grid.Paint(s, v.left()-m.x0, -m.y0, m.selected, cellMindTheme)
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
