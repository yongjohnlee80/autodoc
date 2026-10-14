//go:build gui

package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// runGUINote is the TUI in the GUI's style, standing in for a window, with a.md, a note of one
// mermaid fence, open; setup runs on the loop first (a browser, or none).
func runGUINote(t *testing.T, note string, setup func(h *Host)) *running {
	t.Helper()
	kb := fileDir(t, "a.md", "# A\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true,
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "· kb")
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.nativeViews = func() bool { return true }; setup(r.h); return true })
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	return r
}

const fencedNote = "# A\n\n```mermaid\ngraph TD\n  A[one] --> B[two]\n  B --> C[(three)]\n```\n"

// paneOps are the drawing operations of the pane's page painted, by kind.
func paneOps(r *running) []string {
	return onLoop(r, func() []string {
		v, _ := r.h.htmlView()
		rc := gui.NewRecordingCanvas(gui.Size{W: 900, H: 700}, gui.Size{W: 8, H: 16})
		v.BoundLayout().(gui.View).Paint(rc)
		return rc.Ops()
	})
}

// The GUI's HTML pane draws a note's mermaid fence by mermaid.js, as View diagram and the terminal's
// previews do: the fence's diagram page is drawn by the browser and placed in the page as its
// picture, not its code.
func TestTheHTMLPaneDrawsDiagramsByMermaidJS(t *testing.T) {
	var mu sync.Mutex
	var pages []string
	r := runGUINote(t, fencedNote, func(h *Host) {
		h.browserOverride = func() bool { return true }
		h.rasterizeOverride = func(_ context.Context, page []byte, p widget.Page) ([]byte, error) {
			mu.Lock()
			pages = append(pages, string(page))
			mu.Unlock()
			return tallPage(t, 300, 200), nil
		}
	})
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitFor(t, "the pane's page", func(string) bool { return r.paneSource() != "" })
	r.s.WaitFor(t, "the diagram's picture in the pane", func(string) bool { return slices.Contains(paneOps(r), "DrawImage") })
	mu.Lock()
	defer mu.Unlock()
	if len(pages) == 0 || !strings.Contains(pages[0], `<pre class="mermaid">graph TD`) || !strings.Contains(pages[0], "mermaid") {
		t.Fatalf("the browser was given %d pages, want the fence's diagram page", len(pages))
	}
	if slices.Contains(paneOps(r), "StrokePath") {
		t.Error("the diagram was drawn natively beside mermaid.js's picture")
	}
}

// Without a browser the GUI's HTML pane draws a note's mermaid fence natively, by golib's Mermaid:
// the page it renders keeps the fence's source bytes for following the cursor, and the diagram is
// still a diagram, not its code. Painted as the window paints the pane, its shapes and edges are drawn.
func TestTheHTMLPaneDrawsAFencedDiagram(t *testing.T) {
	r := runGUINote(t, fencedNote, func(h *Host) { h.browserOverride = func() bool { return false } })
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitFor(t, "the pane's page", func(string) bool { return r.paneSource() != "" })
	// the diagram is laid out off the loop: paint until it lands
	r.s.WaitFor(t, "the diagram drawn in the pane", func(string) bool {
		return onLoop(r, func() bool {
			v, _ := r.h.htmlView()
			rc := gui.NewRecordingCanvas(gui.Size{W: 900, H: 700}, gui.Size{W: 8, H: 16})
			v.BoundLayout().(gui.View).Paint(rc)
			return slices.Contains(rc.Ops(), "StrokePath") && slices.Contains(rc.Ops(), "FillEllipse") // edges; the cylinder
		})
	})
}

// In a GUI window View diagram draws the diagram by mermaid.js, as an image, as a terminal does:
// Zoom in and out are there.
func TestViewDiagramInTheGUIIsMermaidJSsImage(t *testing.T) {
	calls := make(chan struct{}, 8)
	r := runGUINote(t, fencedNote, func(h *Host) {
		h.browserOverride = func() bool { return true }
		h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		h.rasterizeOverride = func(_ context.Context, _ []byte, p widget.Page) ([]byte, error) {
			calls <- struct{}{}
			return tallPage(t, p.Width, 300), nil
		}
	})
	r.h.p.Post(func() { r.h.previewDiagram() })
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("mermaid.js was not asked to draw the diagram")
	}
	r.s.WaitFor(t, "the image shown", func(string) bool {
		return onLoop(r, func() bool {
			img, _ := r.h.p.Tree().Source("App.diagramImageShown")
			native, _ := r.h.p.Tree().Source("App.diagramNativeShown")
			return img.Raw == "true" && native.Raw == "false" && r.h.lastDiagramImage()
		})
	})
}

// Without a browser, View diagram in a GUI window draws the diagram natively, by golib's Mermaid,
// saying why; a type golib does not draw is its source, saying both.
func TestViewDiagramInTheGUIWithoutABrowser(t *testing.T) {
	// a window confirms its images (gui's backend), as the test terminal does not
	noBrowser := func(h *Host) {
		h.browserOverride = func() bool { return false }
		h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
	}
	r := runGUINote(t, fencedNote, noBrowser)
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitFor(t, "the diagram drawn natively in the dialog", func(string) bool {
		return onLoop(r, func() bool {
			native, _ := r.h.p.Tree().Source("App.diagramNativeShown")
			v, ok := componentUnder[*widget.HTMLView](r.h.p, "diagram")
			if native.Raw != "true" || !ok {
				return false
			}
			rc := gui.NewRecordingCanvas(gui.Size{W: 900, H: 700}, gui.Size{W: 8, H: 16})
			v.BoundLayout().(gui.View).Paint(rc)
			return slices.Contains(rc.Ops(), "StrokePath") && slices.Contains(rc.Ops(), "FillEllipse")
		})
	})
	if help := onLoop(r, func() string { return r.h.diagramHelpText }); !strings.HasPrefix(help, "Drawn natively") || !strings.Contains(help, "no headless") {
		t.Errorf("help = %q, want it drawn natively for want of a browser", help)
	}

	pie := runGUINote(t, "# P\n\n```mermaid\npie title Pets\n  \"Dogs\" : 386\n  \"Cats\" : 85\n```\n", noBrowser)
	pie.h.p.Post(func() { pie.h.previewDiagram() })
	pie.s.WaitFor(t, "the pie's source, saying why", func(string) bool {
		return onLoop(pie, func() bool {
			text, _ := pie.h.p.Tree().Source("App.diagramTextShown")
			return text.Raw == "true" && strings.Contains(pie.h.diagramHelpText, "no headless") && strings.Contains(pie.h.diagramHelpText, "not drawn natively")
		})
	})
}
