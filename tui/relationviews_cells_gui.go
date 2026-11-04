//go:build gui

package tui

// cellRelationViews: the GUI build writes its relation views (relationviews_gui.go) into main.qml
// only when the program runs as a window (withRelationViews).
const cellRelationViews = false
