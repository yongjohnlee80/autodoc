//go:build !gui

package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// A terminal draws no GlobeMap and no Mindmap: Relations › Links is its view of a document's
// relations (relationmap.go).

const relationMenuQML, relationViewsQML = "", ""

// relationViewFeatures are a terminal build's own features: a note's Rendered view, its page in
// cells (notepage_tui.go). A terminal draws no GlobeMap and no Mindmap of the window's.
func (h *Host) relationViewFeatures() []Feature {
	if h.relationViews == nil {
		h.relationViews = []Feature{newCellNotePage(h)}
	}
	return h.relationViews
}

// relationViewChoices: a terminal has no GlobeMap or Mindmap choosers (language.go's choiceLabels).
func relationViewChoices() map[string][]string { return nil }

// openGlobeSettings is App.openGlobeSettings, which Preferences.qml names in every build.
func (h *Host) openGlobeSettings()                { h.notify(h.globeSettingsElsewhereNotice()) }
func (h *Host) relationViewTypes() []tuidecl.Type { return nil }
