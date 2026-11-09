package tui

import (
	"context"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/htmlview"
)

// diagramHelp is the diagram dialog's help line, as the preview last set it.
func (r *running) diagramHelp() string {
	return onLoop(r, func() string { return r.h.diagramHelpText })
}

// nativeShown reports whether the diagram dialog shows the diagram drawn natively (its help says
// so), no image placed, and its source not on the screen.
func (r *running) nativeShown() bool {
	if len(r.s.Backend.Images()) != 0 || strings.Contains(r.s.String(), "flowchart LR") {
		return false
	}
	return onLoop(r, func() bool { return !r.h.diagramImage && strings.HasPrefix(r.h.diagramHelpText, "Drawn natively") })
}

// closeDiagram closes the diagram dialog with q, and waits for it to close.
func (r *running) closeDiagram(t *testing.T) {
	t.Helper()
	gen := onLoop(r, func() uint64 { return r.h.previewGen })
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the diagram dialog closed", func(string) bool { return onLoop(r, func() uint64 { return r.h.previewGen }) != gen })
}

// TestATerminalWithoutImagesDrawsTheDiagramInCharacters: on a terminal that draws no images, File ›
// Preview Mermaid diagram draws the block in characters, by golib's Mermaid, and says why it is not
// mermaid.js's image; with image previews turned off it shows the block's source, as asked. In
// sepia, then dark, the dialog is logged for review.
func TestATerminalWithoutImagesDrawsTheDiagramInCharacters(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", flowFile)})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriNo }; return true })
	r.h.p.Post(func() { r.h.openPath("f.md") })
	r.waitFile(t, "f.md")
	for _, theme := range []string{"sepia", "dark"} {
		onLoop(r, func() bool { r.h.switchTheme(theme); return true })
		r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		r.h.p.Post(func() { r.h.diagramHelpText = ""; r.h.previewDiagram() }) // no help left from the last: this preview's alone
		r.s.WaitFor(t, theme+": the diagram drawn in characters", func(string) bool { return r.nativeShown() })
		if help := r.diagramHelp(); !strings.Contains(help, "Drawn natively, as mermaid.js is not") || !strings.Contains(help, "does not draw images") {
			t.Fatalf("%s: help %q; want it drawn natively, saying the terminal draws no images", theme, help)
		}
		t.Logf("%s: the dialog:\n%s", theme, r.s)
		r.closeDiagram(t)
	}
	// image previews off: the source, as asked
	r.h.p.Post(func() { r.h.toggleImagePreviews(); r.h.previewDiagram() })
	r.s.WaitForText(t, "flowchart LR")
	if help := r.diagramHelp(); !strings.Contains(help, "image previews are off") || strings.Contains(help, "Drawn natively") {
		t.Fatalf("image previews off: help %q; want the source, saying image previews are off", help)
	}
}

// TestATerminalsPreviewOptionChoosesTheEngine: on a terminal that draws images, File › Preview
// option chooses what draws a diagram, as in a window: mermaid.js's image by default; golib's
// Mermaid in characters with the native engine chosen, and mermaid.js's image again, saying why,
// for a block golib does not draw. The menu is a terminal's too.
func TestATerminalsPreviewOptionChoosesTheEngine(t *testing.T) {
	tuneEnv(t, "XDG_CACHE_HOME="+t.TempDir())
	if strings.Contains(string(layout), "qsTrId(\"autodoc.main.preview_option\")\n                visible: App.gui") {
		t.Fatal("File › Preview option is a window's alone")
	}
	bad := "# Bad\n\n```mermaid\nflowchart LR\n  A[Start --> \n```\n"
	d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", flowFile, "bad.md", bad)})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.rasterizeOverride = func(_ context.Context, _ []byte, p htmlview.Page) ([]byte, error) {
			return tallPage(t, p.Width, int(float64(p.MinHeight)*p.Scale)), nil
		}
		return true
	})
	r.h.p.Post(func() { r.h.openPath("f.md") })
	r.waitFile(t, "f.md")
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitForText(t, "drawn offline by mermaid")
	r.placedImage(t)
	r.closeDiagram(t)
	r.s.WaitFor(t, "the image gone", func(string) bool { return len(r.s.Backend.Images()) == 0 })

	r.h.p.Post(func() { r.h.setMermaidEngine("native"); r.h.diagramHelpText = ""; r.h.previewDiagram() })
	r.s.WaitFor(t, "the native engine's drawing", func(string) bool { return r.nativeShown() })
	if help := r.diagramHelp(); !strings.Contains(help, "Drawn natively (File › Preview option)") {
		t.Fatalf("the native engine: help %q", help)
	}
	r.closeDiagram(t)

	r.h.p.Post(func() { r.h.openPath("bad.md") })
	r.waitFile(t, "bad.md")
	r.h.p.Post(func() { r.h.diagramHelpText = ""; r.h.previewDiagram() })
	r.placedImage(t)
	if help := r.diagramHelp(); !strings.Contains(help, "not drawn natively") || !onLoop(r, func() bool { return r.h.diagramImage }) {
		t.Fatalf("a block golib does not draw, the native engine chosen: help %q; want mermaid.js's image, saying why", help)
	}
}

// TestATerminalsSourceWinsOverTheNativeEngine: with the native engine chosen and image previews off,
// a terminal's preview shows the diagram's source, as image previews off asks; turned off while the
// native drawing is learned, the source too.
func TestATerminalsSourceWinsOverTheNativeEngine(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", flowFile)})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriNo }; return true })
	r.h.p.Post(func() { r.h.openPath("f.md") })
	r.waitFile(t, "f.md")
	r.h.p.Post(func() {
		r.h.setMermaidEngine("native")
		r.h.toggleImagePreviews()
		r.h.diagramHelpText = ""
		r.h.previewDiagram()
	})
	r.s.WaitForText(t, "flowchart LR")
	if help := r.diagramHelp(); !strings.Contains(help, "image previews are off") || strings.HasPrefix(help, "Drawn natively") {
		t.Fatalf("native engine, image previews off: help %q; want the source, saying image previews are off", help)
	}
	r.closeDiagram(t)
	// on again, then off in the same turn as the preview: the native drawing learned off the loop
	// meanwhile gives way to the source
	r.h.p.Post(func() {
		r.h.toggleImagePreviews()
		r.h.diagramHelpText = ""
		r.h.previewDiagram()
		r.h.toggleImagePreviews()
	})
	r.s.WaitForText(t, "flowchart LR")
	if help := r.diagramHelp(); strings.HasPrefix(help, "Drawn natively") {
		t.Fatalf("image previews turned off while the native drawing was learned: help %q; want the source", help)
	}
}
