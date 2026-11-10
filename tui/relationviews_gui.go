//go:build gui

package tui

import (
	"fmt"
	"image/color"

	"github.com/yongjohnlee80/golib/gui"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/style"
)

// THE RELATION VIEWS — Relations › GlobeMap (globemap_gui.go) and Relations › Mindmap
// (mindmap_gui.go): two panels of the GUI's own, written into main.qml after their markers
// (relationmap.go), each a native view drawn over its cells. Their colours are the theme's, given
// to the view as properties, so a theme switch (the document built again) recolours them.

// relationMenuQML is the views' Relations menu items.
const relationMenuQML = `            MenuItem { text: qsTrId("autodoc.gui.relations.globemap"); onTriggered: App.toggleGlobeMap() }
            MenuItem { text: qsTrId("autodoc.gui.relations.globemap_settings"); onTriggered: App.openGlobeSettings() }
            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap"); onTriggered: App.toggleMindmap() }
            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap_settings"); onTriggered: App.openMindmapSettings() }
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

// relationDialogsQML is the views' dialogs as main.qml holds them: their settings
// (globesettings_gui.go, mindsettings_gui.go; relationviews_cells.go's inlineDialog).
var relationDialogsQML = inlineDialog("GlobeMapSettings.qml", "globeMapSettings") +
	inlineDialog("MindmapSettings.qml", "mindmapSettings")

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
		consumed, err := b.ReadProps(paletteFields(cs))
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

// relationViewFeatures are the GlobeMap and the Mindmap, made once: only when the program runs as a
// window. A build with the GUI running in a terminal (no --gui) has Links alone, as any terminal.
func (h *Host) relationViewFeatures() []feature.Feature {
	if !h.gui { // run as a terminal (--ui): the terminal's Mindmap, in cells (mindmap_cells.go)
		if h.relationViews == nil {
			h.relationViews = cellRelationViews(h)
		}
		return h.relationViews
	}
	if h.relationViews == nil {
		store := newGraphStore(core{h}, core{h}, h.graphUserFile) // the views' groups, one store for both
		g, m := newGlobeMap(core{h}, h.globePresenter, h.seams.globeMapPage), newMindmap(core{h})
		g.store, g.editor.store, m.store = store, store, store
		h.relationViews = []feature.Feature{g, m}
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
		case *cellGlobeMap:
			out = append(out, f.viewType())
		case *cellMindmap:
			out = append(out, f.viewType())
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

// openGlobeSettings is App.openGlobeSettings, the host's in every build (Preferences.qml names it):
// the GlobeMap's settings in a window, and a word of why not elsewhere.
func (h *Host) openGlobeSettings() {
	for _, f := range h.relationViewFeatures() {
		switch g := f.(type) {
		case *globeMap:
			g.openSettings()
			return
		case *cellGlobeMap: // a GUI build run as a terminal (--ui)
			g.openSettings()
			return
		}
	}
	h.notify(h.globeSettingsElsewhereNotice())
}
