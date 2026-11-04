//go:build !gui

package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// A terminal's relation views: Relations › Mindmap, drawn in cells (mindmap_tui.go), beside
// Relations › Links (relationmap.go). A terminal draws no GlobeMap.

const mindmapPanelID, mindmapViewID = "mindmap", "mindmapView"

// relationMenuQML is the terminal's Relations menu item for its Mindmap.
const relationMenuQML = `            MenuItem { text: qsTrId("autodoc.gui.relations.mindmap"); onTriggered: App.toggleMindmap() }
`

// relationViewsQML is the Mindmap's panel, a large float in the middle until the user moves or
// sizes it, as the window's is.
const relationViewsQML = `    // the Mindmap in cells (mindmap_tui.go): the page's document and those a link or two from it
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
`

func (h *Host) relationViewFeatures() []Feature {
	if h.relationViews == nil {
		h.relationViews = []Feature{newCellMindmap(core{h})}
	}
	return h.relationViews
}

// relationViewChoices: a terminal has no GlobeMap or Mindmap choosers (language.go's choiceLabels).
func relationViewChoices() map[string][]string { return nil }

// openGlobeSettings is App.openGlobeSettings, which Preferences.qml names in every build.
func (h *Host) openGlobeSettings() { h.notify(h.globeSettingsElsewhereNotice()) }

// relationViewTypes are their views' QML types: the Mindmap's view joins its feature.
func (h *Host) relationViewTypes() []tuidecl.Type {
	var out []tuidecl.Type
	for _, f := range h.relationViewFeatures() {
		if m, ok := f.(*cellMindmap); ok {
			out = append(out, m.viewType())
		}
	}
	return out
}
