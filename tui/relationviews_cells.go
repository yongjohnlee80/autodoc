package tui

import (
	"embed"
	"strings"
)

// A TERMINAL'S RELATION VIEWS, in either build: Relations › GlobeMap and Relations › Mindmap drawn
// in cells (globemap_cells.go, mindmap_cells.go) and the Mindmap's settings, beside Relations ›
// Links (relationmap.go). withRelationViews writes them into main.qml on a terminal; a window
// writes the window's (relationviews_gui.go).

// cellRelationViews are a terminal's relation views, one graph store for both: the GlobeMap and
// the Mindmap, in cells.
func cellRelationViews(h *Host) []Feature {
	store := newGraphStore(core{h}, core{h}, h.graphUserFile)
	return []Feature{newCellGlobeMap(core{h}, store), newCellMindmap(core{h}, store)}
}

// cellRelationMenuQML is the terminal's Relations menu items for its GlobeMap and Mindmap.
const cellRelationMenuQML = `            MenuItem { text: qsTrId("autodoc.gui.relations.globemap"); onTriggered: App.toggleGlobeMap() }
            MenuItem { text: qsTrId("autodoc.gui.relations.globemap_settings"); onTriggered: App.openGlobeSettings() }
            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap"); onTriggered: App.toggleMindmap() }
            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap_settings"); onTriggered: App.openMindmapSettings() }
`

// relationDialogs are the relation views' dialogs: not autodoc.dialogs components, which every
// document may use and so may name only what every App has. Each is written into main.qml whole,
// under its id: a window's both (relationviews_gui.go), a terminal's the Mindmap's.
//
//go:embed qmlrelations/*.qml
var relationDialogs embed.FS

// inlineDialog is the dialog of qmlrelations/file, its root given the id: the file's text from its
// root Dialog on, indented as main.qml's children are.
func inlineDialog(file, id string) string {
	b, err := relationDialogs.ReadFile("qmlrelations/" + file)
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

// cellRelationViewsQML is the terminal GlobeMap's and Mindmap's panels, each a large float in the
// middle until the user moves or sizes it, as the window's are, and their settings, the window's
// dialogs.
var cellRelationViewsQML = `    // the GlobeMap in cells (globemap_cells.go): the workspace's documents and their links, in Braille
    Panel {
        id: globeMap
        heading: App.globeMapTitle
        closeOnQ: true
        edge: Tui.Center
        size: 85
        length: 90
        floating: App.globeMapFloating
        floatX: App.globeMapX; floatY: App.globeMapY; floatWidth: App.globeMapW; floatHeight: App.globeMapH
        onPlaced: App.panelPlaced("globeMap", x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened("globeMap")
        onClosed: App.panelClosed("globeMap")
        CellGlobeMap {
            id: globeMapView
        }
    }
    // the Mindmap in cells (mindmap_cells.go): the page's document and those a link or two from it
    Panel {
        id: mindmap
        heading: App.mindmapTitle
        closeOnQ: true
        edge: Tui.Center
        size: 85
        length: 90
        floating: App.mindmapFloating
        floatX: App.mindmapX; floatY: App.mindmapY; floatWidth: App.mindmapW; floatHeight: App.mindmapH
        onPlaced: App.panelPlaced("mindmap", x, y, width, height)
        minimumSize: 20
        onOpened: App.panelOpened("mindmap")
        onClosed: App.panelClosed("mindmap")
        CellMindmap {
            id: mindmapView
        }
    }
` + inlineDialog("GlobeMapSettings.qml", "globeMapSettings") + inlineDialog("MindmapSettings.qml", "mindmapSettings")
