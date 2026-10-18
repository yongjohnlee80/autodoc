//go:build gui

package tui

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/gio/f32"
	"github.com/yongjohnlee80/golib/gui"
	guimermaid "github.com/yongjohnlee80/golib/gui/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE MINDMAP — Relations › Mindmap: the page's document and the documents within two links of it,
// as a mind map drawn natively (golib gui/mermaid.LayDiagram), each document once, under the one
// that reached it first. A click on a document opens it and keeps the keyboard here; the map is
// rooted again at the page shown, only when one is (Shown). The wheel scrolls a tall map.

// mind is one Mindmap build: everything derived from one graph.map answer, made off the loop and
// never changed once installed.
type mind struct {
	root  fileRef
	m     relMap
	laid  *guimermaid.Laid // laid out from mindmapModel(m, root): its ids "n<i>" index m.Nodes
	width float32          // the width it was laid out for, in logical pixels
}

// mindmap is the Mindmap feature: its builds, fenced (load), and the view's scroll.
type mindmap struct {
	c      Core
	seq    uint64
	open   bool
	page   Page  // the page shown, as Shown last said
	cur    *mind // the build shown; nil until one is installed
	status string
	view   *mindView
	scroll float32 // how far down the map the view is, in logical pixels
	asked  float32 // the width a relayout was last asked for
}

func newMindmap(c Core) *mindmap { return &mindmap{c: c} }

func (*mindmap) Name() string { return "mindmap" }

func (m *mindmap) Panels() []PanelSpec {
	return []PanelSpec{{ID: mindmapPanelID, View: mindmapViewID, Priority: 71,
		Edge: func(prefs) string { return "center" },
		Opened: func(PanelHost) {
			m.open = true
			m.load()
		},
		Closed: func(c PanelHost) {
			m.open = false
			m.seq++ // a build under way is dropped
			c.Keep(c.RestoreKeyboardPane("editor"))
		}}}
}

func (m *mindmap) State() map[string]any { return map[string]any{"App.mindmapTitle": "Mindmap"} }

func (m *mindmap) Commands() map[string]Command {
	return map[string]Command{"App.toggleMindmap": none(m.toggle)}
}

func (m *mindmap) Catalog() []Entry {
	return []Entry{builtin("mindmap.toggle", "the Mindmap", "App.toggleMindmap()", m.toggle)}
}

func (m *mindmap) toggle() { m.c.TogglePanel(mindmapPanelID) }

// Shown roots the map at the page shown, while it is open. A click whose open is cancelled, fails
// or is overtaken shows no page, so it roots nothing.
func (m *mindmap) Shown(p Page) {
	m.page = p
	if m.open {
		m.load()
	}
}

// ThemeChanged lays the map out again in the theme's colours, while it is open: a build keeps the
// colours it was laid out in.
func (m *mindmap) ThemeChanged() {
	if m.open {
		m.load()
	}
}

func (*mindmap) Edited(Page)    {}
func (*mindmap) Moved(int, int) {}
func (*mindmap) Reread(Page)    {}
func (*mindmap) Closed()        {}

// load reads the page's document's neighbourhood and lays it out off the loop; on the loop it
// installs the build whole, or drops it when a newer load, another page or the panel's closing
// came first.
func (m *mindmap) load() {
	ref := m.page.Ref
	tk := m.c.Ticket(&m.seq, TicketFile)
	if ref.path == "" || ref.ws == "" || m.page.Outside {
		m.cur, m.status = nil, "open a workspace's document to see its mind map"
		m.title()
		return
	}
	width, th := m.width(), mindTheme(m.palette())
	m.asked = width
	m.status = "reading " + ref.path + "…"
	m.title()
	m.c.Go(func(ctx context.Context) any {
		res, err := m.c.Call(ctx, "graph.map", ref.ws, map[string]any{"root": ref.path, "depth": int64(2)})
		if err != nil {
			return err
		}
		rm := decodeMap(res)
		model := mindmapModel(rm, ref.path)
		if model == nil {
			return fmt.Errorf("%s is not in its own map", ref.path)
		}
		meas, release := gui.AcquireMeasurer(1)
		defer release()
		laid, err := guimermaid.LayDiagram(ctx, model, width, th, meas)
		if err != nil {
			return err
		}
		return &mind{root: ref, m: rm, laid: laid, width: width}
	}, func(v any) {
		if !tk.Current() || !m.open {
			return
		}
		m.status = ""
		switch v := v.(type) {
		case *mind:
			m.cur, m.scroll = v, 0
		case error:
			if code(v) == rpc.CodeNotFound {
				m.cur, m.status = nil, ref.path+" is not indexed yet"
			} else {
				m.c.Failed("Mindmap", v)
			}
		}
		m.title()
	})
}

// width is the width a build is laid out for: the view's, less its margins; a default before it
// has one.
func (m *mindmap) width() float32 {
	if m.view != nil && m.view.ctx != nil {
		if w := m.view.pixels().W - 2*mindMargin; w > 0 {
			return w
		}
	}
	return 900
}

func (m *mindmap) palette() relPalette {
	if m.view != nil {
		return m.view.pal
	}
	return defaultPalette()
}

// mindTheme is the map's theme from the view's colours: nodes tinted by the accent, lines muted.
func mindTheme(p relPalette) guimermaid.Theme {
	return guimermaid.Theme{
		Background:    p.background,
		Text:          p.text,
		Line:          mixNRGBA(p.background, p.text, 0.4),
		NodeFill:      mixNRGBA(p.background, p.accent, 0.12),
		NodeStroke:    mixNRGBA(p.background, p.accent, 0.7),
		ClusterFill:   mixNRGBA(p.background, p.text, 0.04),
		ClusterStroke: mixNRGBA(p.background, p.text, 0.3),
		Font:          gui.Font{Size: 14},
	}
}

func (m *mindmap) title() {
	t := "Mindmap"
	switch {
	case m.status != "":
		t += " · " + m.status
	case m.cur != nil:
		t += fmt.Sprintf(" · %s · %d documents within two links", m.cur.root.path, len(m.cur.m.Nodes))
	}
	m.c.Set("App.mindmapTitle", t)
	m.dirty()
}

func (m *mindmap) dirty() {
	if m.view != nil && m.view.ctx != nil {
		m.view.ctx.MarkDirty()
	}
}

// mindMargin is the space round the map, in logical pixels.
const mindMargin = 16

// origin is where the map's top left is in the view: centred across, scrolled down.
func (m *mindmap) origin(view gui.Size) gui.Point {
	if m.cur == nil {
		return gui.Point{}
	}
	sz := m.cur.laid.Size()
	return gui.Pt(max((view.W-sz.W)/2, mindMargin), mindMargin-m.scroll)
}

// click opens the document under the view point p, the keyboard kept here.
func (m *mindmap) click(p, at gui.Point) {
	cur := m.cur
	if cur == nil {
		return
	}
	id, ok := cur.laid.NodeAt(gui.Pt(p.X-at.X, p.Y-at.Y))
	if !ok {
		return
	}
	if i, ok := cur.m.mindIndex(id); ok {
		m.c.OpenRefFocus(cur.root.ws, cur.m.Nodes[i].Path, mindmapPanelID)
	}
}

// mindView is the Mindmap's native view, in its panel.
type mindView struct {
	m     *mindmap
	pal   relPalette
	ctx   *tuicore.Context
	cells tuicore.Size
}

func (m *mindmap) newView(p relPalette) tuicore.Component {
	v := &mindView{m: m, pal: p}
	m.view = v
	return v
}

func (v *mindView) Init(ctx *tuicore.Context) { v.ctx = ctx }

// Layout takes the panel; a width another than the build's lays the map out again, once asked.
func (v *mindView) Layout(c tuicore.Constraints) tuicore.Size {
	v.cells = c.Constrain(tuicore.Size{W: c.MaxW, H: c.MaxH})
	m := v.m
	if w := m.width(); m.open && m.cur != nil && w != m.cur.width && w != m.asked {
		m.asked = w
		m.c.Post(m.load) // after the frame: a load sets what the frame reads
	}
	return v.cells
}

func (*mindView) Render(tuicore.Surface) {}
func (*mindView) AcceptsFocus() bool     { return true }

func (v *mindView) pixels() gui.Size {
	cp := v.ctx.CellPixels()
	return gui.Size{W: float32(v.cells.W) * cp.W, H: float32(v.cells.H) * cp.H}
}

func (v *mindView) HandleEvent(ev tuicore.Event) bool {
	m := v.m
	e, ok := ev.(tuicore.MouseEvent)
	if !ok {
		return false
	}
	view := v.pixels()
	cp := v.ctx.CellPixels()
	p := gui.Pt((float32(e.X)+e.SubX)*cp.W, (float32(e.Y)+e.SubY)*cp.H)
	switch e.Kind {
	case tuicore.MousePress:
		if e.Button == tuicore.MouseLeft {
			v.ctx.RequestFocus()
			m.click(p, m.origin(view))
		}
	case tuicore.MouseWheel:
		if m.cur == nil {
			return true
		}
		step := float32(48)
		if e.Button == tuicore.WheelUp {
			step = -step
		}
		most := max(m.cur.laid.Size().H+2*mindMargin-view.H, 0)
		m.scroll = min(max(m.scroll+step, 0), most)
		m.dirty()
	}
	return true
}

// NativeView draws the build as it is now, taken on the loop.
func (v *mindView) NativeView() (any, bool) {
	m := v.m
	f := mindFrame{cur: m.cur, at: m.origin(v.pixels()), status: m.status, pal: v.pal}
	return gui.ViewFunc(f.paint), true
}

type mindFrame struct {
	cur    *mind
	at     gui.Point
	status string
	pal    relPalette
}

func (f mindFrame) paint(c gui.Canvas) {
	sz := c.Size()
	c.FillRect(gui.Rect{W: sz.W, H: sz.H}, gui.Solid(f.pal.background))
	if f.cur != nil {
		c.PushTransform(f32.Affine2D{}.Offset(f32.Pt(f.at.X, f.at.Y)))
		f.cur.laid.Paint(c)
		c.Pop()
	}
	if f.status != "" {
		t := c.Text().Layout(f.status, gui.Font{Size: 13}, sz.W-24)
		c.DrawText(t, gui.Pt(12, 10), gui.Solid(f.pal.muted))
	}
}
