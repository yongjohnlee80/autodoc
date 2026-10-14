//go:build gui

package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// The GUI's HTML pane draws a note's mermaid fence as its diagram, natively: the page it renders
// keeps the fence's source bytes for following the cursor, and the diagram is still a diagram, not
// its code. Painted as the window paints the pane, the diagram's shapes and edges are drawn.
func TestTheHTMLPaneDrawsAFencedDiagram(t *testing.T) {
	kb := fileDir(t, "a.md", "# A\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true,
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "· kb")
	note := "# A\n\n```mermaid\ngraph TD\n  A[one] --> B[two]\n  B --> C[(three)]\n```\n"
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.nativeViews = func() bool { return true } })
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
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

// In a GUI window, View diagram draws the diagram natively, by golib's Mermaid, in the dialog's
// HTML view: no headless browser, and not its source.
func TestViewDiagramInTheGUIDrawsItNatively(t *testing.T) {
	kb := fileDir(t, "a.md", "# A\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true,
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "· kb")
	note := "# A\n\n```mermaid\ngraph TD\n  A[one] --> B[two]\n  B --> C[(three)]\n```\n"
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.nativeViews = func() bool { return true } })
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitFor(t, "the diagram drawn natively in the dialog", func(string) bool {
		return onLoop(r, func() bool {
			native, _ := r.h.p.Tree().Source("App.diagramNativeShown")
			text, _ := r.h.p.Tree().Source("App.diagramTextShown")
			v, ok := componentUnder[*widget.HTMLView](r.h.p, "diagram")
			if native.Raw != "true" || text.Raw != "false" || !ok {
				return false
			}
			rc := gui.NewRecordingCanvas(gui.Size{W: 900, H: 700}, gui.Size{W: 8, H: 16})
			v.BoundLayout().(gui.View).Paint(rc)
			return slices.Contains(rc.Ops(), "StrokePath") && slices.Contains(rc.Ops(), "FillEllipse")
		})
	})
	if help := onLoop(r, func() string { return r.h.diagramHelpText }); !strings.HasPrefix(help, "Drawn natively") {
		t.Errorf("help = %q, want it drawn natively", help)
	}
}
