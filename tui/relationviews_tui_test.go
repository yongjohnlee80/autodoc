//go:build !gui

package tui

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/relations"
)

// A terminal writes its own relation views into main.qml, run as a window or not: Relations ›
// GlobeMap and Relations › Mindmap, drawn in cells (CellGlobeMap, CellMindmap), and never the GUI's
// views. Preferences' GlobeMap… opens the terminal's settings.
func TestATerminalsRelationViewsAreItsCellViews(t *testing.T) {
	windowView := regexp.MustCompile(`(^|[^A-Za-z])(GlobeMap|Mindmap) \{`) // the window's, not the cells'
	// a terminal: the build without the GUI never runs as a window
	for _, gui := range []bool{false} {
		got := withRelationViews(layout, gui)
		for _, want := range []string{`App.toggleGlobeMap()`, `CellGlobeMap {`, `id: globeMap`, `App.toggleMindmap()`,
			`CellMindmap {`, `id: mindmap`, `onTriggered: App.openGlobeSettings()`} {
			if !bytes.Contains(got, []byte(want)) {
				t.Errorf("gui %v: main.qml lacks %s", gui, want)
			}
		}
		if windowView.Match(got) {
			t.Errorf("gui %v: a terminal's main.qml names a window's view: %s", gui, windowView.Find(got))
		}
	}
	if !bytes.Contains(layout, []byte(relationMenuMarker)) || !bytes.Contains(layout, []byte(relationViewsMarker)) {
		t.Fatal("main.qml lost a relation views' marker")
	}
	h := newHost(NewSession("unused", nil), Options{})
	fs, ty := h.relationViewFeatures(), h.relationViewTypes()
	if len(fs) != 2 || len(ty) != 2 || ty[0].Name != "CellGlobeMap" || ty[1].Name != "CellMindmap" {
		t.Fatalf("a terminal's relation views: %v, %v", fs, ty)
	}
	if _, ok := fs[0].(*relations.CellGlobeMap); !ok {
		t.Fatalf("a terminal's first relation view is %T, want its cell GlobeMap", fs[0])
	}
	if _, ok := fs[1].(*relations.CellMindmap); !ok {
		t.Fatalf("a terminal's second relation view is %T, want its cell Mindmap", fs[1])
	}
}
