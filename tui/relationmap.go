package tui

import (
	"bytes"
	"slices"
)

// THE RELATION VIEWS' GLUE — the panels the GUI writes into main.qml for the GlobeMap and the
// Mindmap, and the notice a terminal gives for the GlobeMap's settings. graph.map's answer as the
// views read it is the relations feature's (graphmap.go).

// globeSettingsElsewhereNotice is App.openGlobeSettings where there is no GlobeMap (a terminal):
// the notice that the GlobeMap and its settings are the window's, in the UI's language.
func (h *Host) globeSettingsElsewhereNotice() string {
	return h.tr("autodoc.notice.relationmap.the_globemap_and_its_settings_are_the_window_s_r")
}

// The lines of main.qml the GUI's relation views are written after: their Relations menu items,
// and their panels.
const (
	relationMenuMarker  = "            // ---- the relation views' menu items (the GUI's) ----\n"
	relationViewsMarker = "    // ---- the relation views' panels (the GUI's) ----\n"
)

// withRelationViews is src with the relation views written in after their markers: the window's
// when gui (the program runs as a window, not only built able to), the terminal's (its Mindmap in
// cells, relationviews_cells.go) otherwise, in either build; nothing where a marker is missing (a
// test's own layout).
func withRelationViews(src []byte, gui bool) []byte {
	if !gui {
		return insertAfter(insertAfter(src, relationMenuMarker, cellRelationMenuQML), relationViewsMarker, cellRelationViewsQML)
	}
	return insertAfter(insertAfter(src, relationMenuMarker, relationMenuQML), relationViewsMarker, relationViewsQML)
}

// insertAfter is src with text written after the line marker, or src as it is when it has no
// such line or text is empty.
func insertAfter(src []byte, marker, text string) []byte {
	i := bytes.Index(src, []byte(marker))
	if i < 0 || text == "" {
		return src
	}
	at := i + len(marker)
	out := append(slices.Clip(src[:at]), text...)
	return append(out, src[at:]...)
}
