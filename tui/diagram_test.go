package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

func TestMermaidBlockUsesTheOneUnderTheCursor(t *testing.T) {
	source := "# Note\n\n```mermaid\nflowchart TD\nA-->B\n```\n\n```mermaid\nsequenceDiagram\nAlice->>Bob: hi\n```\n"
	block, ok := mermaidBlock([]byte(source), strings.Index(source, "Alice"), nil)
	if !ok || !strings.Contains(block, "sequenceDiagram") {
		t.Fatalf("selected block = %q, %v", block, ok)
	}
	block, ok = mermaidBlock([]byte(source), 0, nil)
	if !ok || !strings.Contains(block, "flowchart TD") {
		t.Fatalf("default block = %q, %v", block, ok)
	}
}

// A line embedding a .mmd file is a diagram as a fence is: the one shown when the cursor is on its
// line, or the file's first when the cursor is on none. An embed its reader cannot read is passed
// over, and with no reader (an untitled draft) embeds are not diagrams.
func TestAnEmbeddedDiagramIsADiagramAtItsLine(t *testing.T) {
	source := "# Note\n\n![flow](flow.mmd)\n\n```mermaid\nsequenceDiagram\nAlice->>Bob: hi\n```\n\n![gone](gone.mmd)\n"
	files := map[string]string{"flow.mmd": "flowchart TD\n  A --> B\n"}
	read := func(dest string) (string, bool) { s, ok := files[dest]; return s, ok }
	for _, c := range []struct {
		cursor int
		want   string
	}{
		{strings.Index(source, "![flow]") + 3, "flowchart TD"},                    // on the embed's line
		{strings.Index(source, "(flow.mmd)") + len("(flow.mmd)"), "flowchart TD"}, // its line's end
		{strings.Index(source, "Alice"), "sequenceDiagram"},                       // the fence
		{0, "flowchart TD"}, // none: the first
		{strings.Index(source, "![gone]"), "flowchart TD"}, // unreadable: passed over
	} {
		if got, ok := mermaidBlock([]byte(source), c.cursor, read); !ok || !strings.Contains(got, c.want) {
			t.Errorf("cursor %d: %q (ok %v), want %q", c.cursor, got, ok, c.want)
		}
	}
	if got, ok := mermaidBlock([]byte("![flow](flow.mmd)\n"), 0, nil); ok {
		t.Errorf("with no reader an embed was a diagram: %q", got)
	}
	if got, ok := mermaidBlock([]byte("![pic](pic.png)\n"), 0, read); ok {
		t.Errorf("an image was a diagram: %q", got)
	}
}

// The diagram view shows the .mmd file a note embeds when the cursor is on the embed's line, read
// beside the note on disk, as File › Preview Mermaid diagram and the editor's View diagram do;
// that menu item is enabled there.
func TestTheDiagramViewShowsTheEmbedUnderTheCursor(t *testing.T) {
	r, _, _, abs := runOutside(t)
	dir := filepath.Dir(abs)
	for name, text := range map[string]string{
		"notes.md": "# Notes\n\n![flow](flow.mmd)\n",
		"flow.mmd": "flowchart TD\n  embedded --> shown\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	enabled := onLoop(r, func() bool {
		r.h.core.SetCursor(widget.TextPos{Line: 2, Col: 3})
		_, ok := mermaidBlock([]byte(r.h.core.Value()), r.h.cursorBytes(), r.h.diagramReader())
		r.h.previewDiagram()
		return ok
	})
	if !enabled {
		t.Error("View diagram is not enabled on the embed's line")
	}
	r.s.WaitFor(t, "the embedded diagram shown", func(string) bool {
		return onLoop(r, func() bool {
			text, _ := r.h.p.Tree().Source("App.diagramText")
			return strings.Contains(text.Raw, "embedded --> shown") || r.h.lastDiagramImage()
		})
	})
}
