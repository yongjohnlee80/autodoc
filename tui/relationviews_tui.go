//go:build !gui

package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// A terminal build's relation views: Relations › Mindmap, drawn in cells (mindmap_cells.go,
// relationviews_cells.go), beside Relations › Links (relationmap.go). A terminal draws no GlobeMap.

const mindmapPanelID, mindmapViewID = "mindmap", "mindmapView"

// relationMenuQML and relationViewsQML are the window's: a build without the GUI has none, and
// writes the terminal's (relationviews_cells.go).
const relationMenuQML, relationViewsQML = "", ""

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
