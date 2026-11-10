//go:build !gui

package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/relations"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// A terminal build's relation views: Relations › GlobeMap and Relations › Mindmap, drawn in cells
// (globemap_cells.go, mindmap_cells.go, relationviews_cells.go), beside Relations › Links
// (relationmap.go).

// relationMenuQML and relationViewsQML are the window's: a build without the GUI has none, and
// writes the terminal's (relationviews_cells.go).
const relationMenuQML, relationViewsQML = "", ""

// relationViewFeatures are the terminal's GlobeMap and Mindmap, in cells, made once.
func (h *Host) relationViewFeatures() []feature.Feature {
	if h.relationViews == nil {
		h.relationViews = relations.CellViews(core{h}, core{h}, h.graphUserFile, h.seams.globeMapPage)
	}
	return h.relationViews
}

// openGlobeSettings is App.openGlobeSettings, which Preferences.qml names in every build: the
// GlobeMap's settings, the terminal's.
func (h *Host) openGlobeSettings() {
	if !relations.OpenGlobeSettings(h.relationViewFeatures()) {
		h.notify(h.globeSettingsElsewhereNotice())
	}
}

// relationViewTypes are their views' QML types: the Mindmap's view joins its feature.
func (h *Host) relationViewTypes() []tuidecl.Type {
	return relations.ViewTypes(h.relationViewFeatures())
}
