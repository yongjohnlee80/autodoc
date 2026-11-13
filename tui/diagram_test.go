package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget/editor"
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
// that menu item is enabled there. It shows as its source, an image, or, on a terminal that shows
// no image, drawn in characters: its nodes boxed inside the dialog.
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
		r.h.core.SetCursor(editor.TextPos{Line: 2, Col: 3})
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
			if strings.Contains(text.Raw, "embedded --> shown") || r.h.lastDiagramImage() {
				return true
			}
			if !strings.HasPrefix(r.h.diagramHelpText, "Drawn natively") {
				return false
			}
			inside := dialogInside(r.s.String(), "Mermaid preview") // drawn: its nodes boxed, inside the dialog
			return strings.Contains(inside, "embedded") && strings.Contains(inside, "shown") && strings.ContainsAny(inside, "┌┐└┘╭╮╰╯")
		})
	})
}

// dialogInside is the dialog titled title as the screen shows it: the text between its frame's
// sides, each of its rows, its frame excluded; "" when it is not on the screen.
func dialogInside(screen, title string) string {
	rows := strings.Split(screen, "\n")
	for y, row := range rows {
		rs := []rune(row)
		at := strings.Index(row, title)
		if at < 0 {
			continue
		}
		t := len([]rune(row[:at]))
		left, right := -1, -1
		for x := t - 1; x >= 0; x-- {
			if strings.ContainsRune("┌╭", rs[x]) {
				left = x
				break
			}
		}
		for x := t; x < len(rs); x++ {
			if strings.ContainsRune("┐╮", rs[x]) {
				right = x
			}
		}
		if left < 0 || right < 0 {
			continue
		}
		var b strings.Builder
		for _, in := range rows[y+1:] {
			rr := []rune(in)
			if left < len(rr) && strings.ContainsRune("└╰", rr[left]) {
				break
			}
			if right < len(rr) {
				b.WriteString(string(rr[left+1:right]) + "\n")
			}
		}
		return b.String()
	}
	return ""
}
