//go:build !gui

package tui

import (
	"bytes"
	"testing"
)

// A terminal writes its own relation view into main.qml, run as a window or not: Relations ›
// Mindmap, drawn in cells (CellMindmap), and never the GUI's GlobeMap.
func TestATerminalsRelationViewIsItsCellMindmap(t *testing.T) {
	for _, gui := range []bool{false, true} { // a build without the GUI has its cell views, run as one or not
		got := withRelationViews(layout, gui)
		for _, want := range []string{`App.toggleMindmap()`, `CellMindmap {`, `id: mindmap`} {
			if !bytes.Contains(got, []byte(want)) {
				t.Errorf("gui %v: main.qml lacks %s", gui, want)
			}
		}
		if bytes.Contains(got, []byte("GlobeMap")) {
			t.Errorf("gui %v: a terminal's main.qml names the GlobeMap", gui)
		}
	}
	if !bytes.Contains(layout, []byte(relationMenuMarker)) || !bytes.Contains(layout, []byte(relationViewsMarker)) {
		t.Fatal("main.qml lost a relation views' marker")
	}
	h := newHost(NewSession("unused", nil), Options{})
	fs, ty := h.relationViewFeatures(), h.relationViewTypes()
	if len(fs) != 1 || len(ty) != 1 || ty[0].Name != "CellMindmap" {
		t.Fatalf("a terminal's relation views: %v, %v", fs, ty)
	}
	if _, ok := fs[0].(*cellMindmap); !ok {
		t.Fatalf("a terminal's relation view is %T, want its cell Mindmap", fs[0])
	}
	// Preferences' GlobeMap… names its command here too: it says the settings are a window's
	h.openGlobeSettings() // a host as the program makes one: its notices kept
	if len(h.noticeLog.items) != 1 || h.noticeLog.items[0].text != globeSettingsElsewhere {
		t.Errorf("a terminal's GlobeMap settings said %+v", h.noticeLog.items)
	}
}
