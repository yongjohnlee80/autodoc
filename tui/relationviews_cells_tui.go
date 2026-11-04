//go:build !gui

package tui

// cellRelationViews: a terminal build writes its relation views (relationviews_tui.go) into
// main.qml whether or not the program runs as a window, which it never does (withRelationViews).
const cellRelationViews = true
