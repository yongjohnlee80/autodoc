//go:build gui

package tui

import (
	"fmt"

	"github.com/yongjohnlee80/autodoc/tui/relations"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
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
var relationViewsQML = relationDialogsQML + relationPanelQML(relations.GlobeMapPanelID, relations.GlobeMapViewID, "GlobeMap",
	"the GlobeMap (globemap_gui.go): the workspace's documents and their links, laid out by force, in the terminals' theme", "") +
	relationPanelQML(relations.MindmapPanelID, relations.MindmapViewID, "Mindmap",
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

// relationViewFeatures are the GlobeMap and the Mindmap, made once: a window's when the program runs
// as one, else in cells (a GUI build run as a terminal, --ui).
func (h *Host) relationViewFeatures() []feature.Feature {
	if h.relationViews == nil {
		if h.gui {
			h.relationViews = relations.WindowViews(core{h}, core{h}, h.graphUserFile, h.seams.globeMapPage, func() any { return h.backend })
		} else {
			h.relationViews = relations.CellViews(core{h}, core{h}, h.graphUserFile, h.seams.globeMapPage)
		}
	}
	return h.relationViews
}

// relationViewTypes are their views' QML types.
func (h *Host) relationViewTypes() []tuidecl.Type {
	return relations.ViewTypes(h.relationViewFeatures())
}

// openGlobeSettings is App.openGlobeSettings, the host's in every build (Preferences.qml names it):
// the GlobeMap's settings, and a word of why not where there is none.
func (h *Host) openGlobeSettings() {
	if !relations.OpenGlobeSettings(h.relationViewFeatures()) {
		h.notify(h.globeSettingsElsewhereNotice())
	}
}
