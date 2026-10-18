//go:build !gui

package tui

import (
	"bytes"
	"testing"
)

// A terminal writes nothing into main.qml for the GUI's relation views, and has neither view: its
// document names no GlobeMap or Mindmap type, and Relations › Links is its only item.
func TestATerminalHasNoRelationViews(t *testing.T) {
	for _, gui := range []bool{false, true} { // a build without the GUI has no views, run as one or not
		if got := withRelationViews(layout, gui); !bytes.Equal(got, layout) {
			t.Fatalf("gui %v: a terminal build's main.qml was written to", gui)
		}
	}
	if !bytes.Contains(layout, []byte(relationMenuMarker)) || !bytes.Contains(layout, []byte(relationViewsMarker)) {
		t.Fatal("main.qml lost a relation views' marker")
	}
	var h Host
	if f, ty := h.relationViewFeatures(), h.relationViewTypes(); f != nil || ty != nil {
		t.Fatalf("a terminal's relation views: %v, %v", f, ty)
	}
}
