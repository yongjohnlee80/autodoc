//go:build gui

package tui

import (
	"embed"
	"fmt"
	"image/color"
	"strings"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	guimermaid "github.com/yongjohnlee80/golib/gui/mermaid"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/style"
)

// THE RELATION VIEWS — Relations › GlobeMap (globemap_gui.go) and Relations › Mindmap
// (mindmap_gui.go): two panels of the GUI's own, written into main.qml after their markers
// (relationmap.go), each a native view drawn over its cells. Their colours are the theme's, given
// to the view as properties, so a theme switch (the document built again) recolours them.

const (
	globeMapPanelID, globeMapViewID = "globeMap", "globeMapView"
	mindmapPanelID, mindmapViewID   = "mindmap", "mindmapView"
)

// relationMenuQML is the views' Relations menu items.
const relationMenuQML = `            MenuItem { text: "&GlobeMap"; onTriggered: App.toggleGlobeMap() }
            MenuItem { text: "GlobeMap &settings…"; onTriggered: App.openGlobeSettings() }
            MenuItem { text: "&Mindmap"; onTriggered: App.toggleMindmap() }
            MenuItem { text: "Mi&ndmap settings…"; onTriggered: App.openMindmapSettings() }
`

// relationColours are the theme's colours a view is drawn in, as its properties bind them.
const relationColours = `background: Theme.document.base; text: Theme.document.text; muted: Theme.document.lineNumber
            accent: Theme.document.highlight; superseded: Theme.syntax.alert; relation: Theme.syntax.attribute
            adr: Theme.syntax.keyword; review: Theme.syntax.string; convention: Theme.syntax.dataType
            synthesis: Theme.syntax.decVal; pr: Theme.syntax.specialChar`

// relationViewsQML is the views' panels, each a large float in the middle of the window until the
// user moves or sizes it, and their dialogs.
// The GlobeMap takes no colours here: it is drawn in the terminals' theme (globemap_gui.go). The
// Mindmap is drawn in the page's.
var relationViewsQML = relationDialogsQML + relationPanelQML(globeMapPanelID, globeMapViewID, "GlobeMap",
	"the GlobeMap (globemap_gui.go): the workspace's documents and their links, laid out by force, in the terminals' theme", "") +
	relationPanelQML(mindmapPanelID, mindmapViewID, "Mindmap",
		"the Mindmap (mindmap_gui.go): the page's document and those two links from it, as a mind map", relationColours)

// relationDialogs are the views' dialogs, the GUI's alone: not autodoc.dialogs components, which
// every build's document may use and so may name only what every build's App has. Each is written
// into main.qml whole, under its id (relationDialogsQML).
//
//go:embed qmlgui/*.qml
var relationDialogs embed.FS

// relationDialogsQML is the views' dialogs as main.qml holds them: their settings
// (globesettings_gui.go, mindsettings_gui.go).
var relationDialogsQML = inlineDialog("GlobeMapSettings.qml", "globeMapSettings") +
	inlineDialog("MindmapSettings.qml", "mindmapSettings")

// inlineDialog is the dialog of qmlgui/file, its root given the id: the file's text from its root
// Dialog on, indented as main.qml's children are.
func inlineDialog(file, id string) string {
	b, err := relationDialogs.ReadFile("qmlgui/" + file)
	if err != nil {
		panic("tui: embedded " + file + ": " + err.Error())
	}
	src := string(b)
	at := strings.Index(src, "Dialog {")
	if at < 0 {
		panic("tui: " + file + " has no root Dialog")
	}
	src = "Dialog {\n    id: " + id + src[at+len("Dialog {"):]
	var out strings.Builder
	for line := range strings.Lines(src) {
		if strings.TrimSpace(line) != "" {
			out.WriteString("    ")
		}
		out.WriteString(line)
	}
	return out.String()
}

func relationPanelQML(id, view, typ, what, colours string) string {
	return fmt.Sprintf(`    // %[4]s
    Panel {
        id: %[1]s
        heading: App.%[1]sTitle
        closeOnQ: true
        edge: Tui.Center
        size: 85
        length: 90
        floating: App.%[1]sFloating
        floatX: App.%[1]sX; floatY: App.%[1]sY; floatWidth: App.%[1]sW; floatHeight: App.%[1]sH
        onPlaced: App.panelPlaced(%[1]q, x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened(%[1]q)
        onClosed: App.panelClosed(%[1]q)
        %[3]s {
            id: %[2]s
            %[5]s
        }
    }
`, id, view, typ, what, colours)
}

// paletteOf is a view's colours from a theme's values (document.*, syntax.*), each one the theme
// lacks or gives in a form other than #rrggbb taking def's.
func paletteOf(v map[string]string, def relPalette) relPalette {
	c := func(key string, fallback color.NRGBA) color.NRGBA {
		if c, ok := parseHex(v[key]); ok {
			return c
		}
		return fallback
	}
	p := relPalette{
		background: c("document.base", def.background), text: c("document.text", def.text),
		muted: c("document.lineNumber", def.muted), accent: c("document.highlight", def.accent),
		superseded: c("syntax.alert", def.superseded), relation: c("syntax.attribute", def.relation),
		types: map[string]color.NRGBA{},
	}
	for t, key := range map[string]string{"adr": "syntax.keyword", "review": "syntax.string",
		"convention": "syntax.dataType", "synthesis": "syntax.decVal", "pr": "syntax.specialChar"} {
		p.types[t] = c(key, def.types[t])
	}
	return p
}

// relPalette is a view's colours.
type relPalette struct {
	background, text, muted, accent, superseded, relation color.NRGBA
	types                                                 map[string]color.NRGBA // a document's colour by its frontmatter type
}

// relationTypes are the frontmatter types drawn in colours of their own; any other is text's.
var relationTypes = []string{"adr", "review", "convention", "synthesis", "pr"}

// paletteFields reads each colour property into its place.
func paletteFields(cs map[string]*style.Color) map[string]tuidecl.Field {
	out := map[string]tuidecl.Field{}
	for k, c := range cs {
		out[k] = tuidecl.ColorField(c)
	}
	return out
}

// paletteProps are a view's colour properties.
var paletteProps = append([]string{"background", "text", "muted", "accent", "superseded", "relation"}, relationTypes...)

// relationViewType is the QML type of a view: name, built by build from its palette, and dropped
// by gone. A view takes no children. Its colours are constructor properties: a theme switch
// changes them, and the document builds the view again (gone runs for the old one). A colour the
// theme gives without RGB values (a terminal's palette index) keeps defaultPalette's.
func relationViewType(name string, build func(relPalette) tuicore.Component, gone func(tuicore.Component)) tuidecl.Type {
	return tuidecl.Type{Name: name, Ctor: paletteProps, Destroyed: gone, Build: func(b tuidecl.Build) (tuicore.Component, []string, error) {
		if len(b.Children) != 0 {
			return nil, nil, fmt.Errorf("a %s takes no children (at %s)", name, b.Pos)
		}
		names := paletteProps
		cs := map[string]*style.Color{}
		for _, n := range names {
			cs[n] = new(style.Color)
		}
		consumed, err := tuidecl.ReadProps(b.Props, paletteFields(cs))
		if err != nil {
			return nil, nil, err
		}
		def := defaultPalette()
		rgb := func(c *style.Color, fallback color.NRGBA) color.NRGBA {
			if r, g, bl, ok := c.RGBValues(); ok {
				return color.NRGBA{R: r, G: g, B: bl, A: 0xff}
			}
			return fallback
		}
		p := relPalette{
			background: rgb(cs["background"], def.background), text: rgb(cs["text"], def.text),
			muted: rgb(cs["muted"], def.muted), accent: rgb(cs["accent"], def.accent),
			superseded: rgb(cs["superseded"], def.superseded), relation: rgb(cs["relation"], def.relation),
			types: map[string]color.NRGBA{},
		}
		for _, t := range relationTypes {
			p.types[t] = rgb(cs[t], def.types[t])
		}
		return build(p), consumed, nil
	}}
}

// defaultPalette is dark.qml's colours: what a view wears before a theme gives it others.
func defaultPalette() relPalette {
	hex := func(v uint32) color.NRGBA {
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
	}
	return relPalette{
		background: hex(0x1c1c1c), text: hex(0xd0d0d0), muted: hex(0x5f5f5f), accent: hex(0x87afd7),
		superseded: hex(0xff5f5f), relation: hex(0xafafd7),
		types: map[string]color.NRGBA{"adr": hex(0x87afd7), "review": hex(0xd7af87), "convention": hex(0x87d7af),
			"synthesis": hex(0xafd7ff), "pr": hex(0xff8787)},
	}
}

// relationViewFeatures are the GlobeMap and the Mindmap, made once: only when the program runs as a
// window. A build with the GUI running in a terminal (no --gui) has Links alone, as any terminal.
func (h *Host) relationViewFeatures() []Feature {
	if !h.gui {
		return nil
	}
	if h.relationViews == nil {
		store := newGraphStore(core{h}, core{h}, h.graphUserFile) // the views' groups, one store for both
		g, m := newGlobeMap(core{h}, h.globePresenter), newMindmap(core{h})
		g.store, g.editor.store, m.store = store, store, store
		h.relationViews = []Feature{g, m}
	}
	return h.relationViews
}

// globePresenter is where the GlobeMap's pictures go: a stream of the window it runs in, or, with
// no window (a test's host on a terminal backend), its view's own paint.
func (h *Host) globePresenter() globePublisher {
	if b, ok := h.backend.(*gui.Backend); ok {
		return streamPublisher{cs: b.NewCanvasStream()}
	}
	return &paintPresenter{}
}

// relationViewTypes are their views' QML types: each built view joins its feature.
func (h *Host) relationViewTypes() []tuidecl.Type {
	var out []tuidecl.Type
	for _, f := range h.relationViewFeatures() {
		switch f := f.(type) {
		case *globeMap:
			out = append(out, relationViewType("GlobeMap", f.newView, func(c tuicore.Component) {
				if f.view == c {
					f.view, f.drag = nil, nil // its gesture goes with it
				}
			}))
		case *mindmap:
			out = append(out, relationViewType("Mindmap", f.newView, func(c tuicore.Component) {
				if f.view == c {
					f.view = nil
				}
			}))
		}
	}
	return out
}

// mixNRGBA is a blended with b by t: 0 is a, 1 is b.
func mixNRGBA(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x)*(1-t) + float32(y)*t + 0.5) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// A VIEW'S LEGEND — what its marks stand for, on a plate in its bottom right corner (l hides it, and
// shows it again). Each row is a mark, drawn in a box of legendMarkW by legendMarkH, and its words.

const (
	legendRowH  = 18
	legendMarkW = 24
	legendMarkH = 13
	legendPad   = 8
)

// legendRow is one row of a legend.
type legendRow struct {
	mark func(c gui.Canvas, box gui.Rect)
	text string
}

// paintLegend draws rows on a plate in the bottom right of a view of size sz, in pal's colours:
// nothing when they do not fit.
func paintLegend(c gui.Canvas, sz gui.Size, pal relPalette, rows []legendRow) {
	if len(rows) == 0 {
		return
	}
	f := gui.Font{Size: 12}
	texts := make([]*gui.TextLayout, len(rows))
	w := float32(0)
	for i, r := range rows {
		texts[i] = c.Text().Layout(r.text, f, 240)
		w = max(w, texts[i].Width)
	}
	pw, ph := 2*legendPad+legendMarkW+8+w, 2*legendPad+float32(len(rows))*legendRowH
	if pw+24 > sz.W || ph+24 > sz.H {
		return
	}
	plate := gui.Rect{X: sz.W - pw - 12, Y: sz.H - ph - 12, W: pw, H: ph}
	c.FillRRect(plate, 6, gui.Solid(mixNRGBA(pal.background, pal.text, 0.07)))
	c.StrokeRRect(plate, 6, 1, gui.Solid(mixNRGBA(pal.background, pal.text, 0.25)))
	for i, r := range rows {
		y := plate.Y + legendPad + float32(i)*legendRowH
		r.mark(c, gui.Rect{X: plate.X + legendPad, Y: y + (legendRowH-legendMarkH)/2, W: legendMarkW, H: legendMarkH})
		c.DrawText(texts[i], gui.Pt(plate.X+legendPad+legendMarkW+8, y+(legendRowH-texts[i].Height)/2), gui.Solid(pal.text))
	}
}

// legendDot is a dot of colour col, r across, centred in its box.
func legendDot(col color.NRGBA, r float32) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		c.FillEllipse(gui.Rect{X: b.X + b.W/2 - r/2, Y: b.Y + b.H/2 - r/2, W: r, H: r}, gui.Solid(col))
	}
}

// legendRing is a ring of colour col round a dot of colour dot.
func legendRing(dot, col color.NRGBA) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		legendDot(dot, 7)(c, b)
		p := &gui.Path{}
		circle(p, graphlayout.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}, 6)
		c.StrokePath(p, 1.5, gui.Solid(col))
	}
}

// legendLine is a link of colour col across its box.
func legendLine(col color.NRGBA) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		c.StrokePath((&gui.Path{}).MoveTo(gui.Pt(b.X, b.Y+b.H/2)).LineTo(gui.Pt(b.X+b.W, b.Y+b.H/2)), 2, gui.Solid(col))
	}
}

// legendForm is a Mindmap node's outline by its shape, in th's node colours: the map's own
// (gui/mermaid.PaintMindShape).
func legendForm(s pm.MindShape, th guimermaid.Theme) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		guimermaid.PaintMindShape(c, s, b, th.NodeFill, th.NodeStroke, 1, max(th.Font.Size, 8)*0.6)
	}
}

// groupLegend are a legend's rows for the groups of gs that documents of m wear, in the groups'
// order, each its mark; at most groupLegendRows, then a row saying how many more. None at all says
// how to have some.
func groupLegend(gs *relGroups, m relMap, mark func(relGroup) func(gui.Canvas, gui.Rect)) []legendRow {
	none := func(gui.Canvas, gui.Rect) {}
	if gs == nil || len(gs.list) == 0 {
		return []legendRow{{none, "no groups: no schema types or folders (settings add them)"}}
	}
	worn := make([]bool, len(gs.list))
	for _, n := range m.Nodes {
		if i := gs.of(n); i >= 0 {
			worn[i] = true
		}
	}
	var rows []legendRow
	more := 0
	for i, g := range gs.list {
		switch {
		case !worn[i]:
		case len(rows) < groupLegendRows:
			rows = append(rows, legendRow{mark(g), groupLabel(g.Query)})
		default:
			more++
		}
	}
	if more > 0 {
		rows = append(rows, legendRow{none, fmt.Sprintf("… %d more groups", more)})
	}
	return rows
}
