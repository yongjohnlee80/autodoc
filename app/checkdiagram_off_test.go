//go:build !gui

package app

import (
	"strings"
	"testing"
)

// The build without the GUI has no golib/gui to draw with: --check-diagram says it needs the GUI
// build, and exits 3, apart from a diagram that is not drawn.
func TestCheckDiagramNeedsTheGUIBuild(t *testing.T) {
	dir := writeFiles(t, map[string]string{"flow.mmd": "flowchart LR\n  A --> B\n"})
	if code, out := runMain(t, nil, "--check-diagram", dir+"/flow.mmd"); code != diagramsNoGUI || !strings.Contains(out, "needs the GUI build") {
		t.Fatalf("without the GUI: %d %s", code, out)
	}
}
