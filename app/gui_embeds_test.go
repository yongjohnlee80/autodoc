//go:build gui

package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

// diagramHost stands in for the editor's: it records what it is asked and answers with answer.
type diagramHost struct {
	asked  []guiwidget.Block
	src    []string
	answer guiwidget.DiagramAnswer
}

func (h *diagramHost) Diagram(_ guiwidget.Diagrammer, b guiwidget.Block, req guiwidget.DiagramRequest) guiwidget.DiagramAnswer {
	h.asked, h.src = append(h.asked, b), append(h.src, req.Src)
	return h.answer
}

func embedNote(t *testing.T) (*noteEmbeds, string) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"note.md":       "# Note\n",
		"flow.mmd":      "graph TD\n  a --> b\n",
		"note/data.csv": "name,count\nclaims,\"3,000\"\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n := &noteEmbeds{}
	n.setNote(filepath.Join(dir, "note.md"))
	return n, dir
}

func spanTexts(bl guiwidget.BlockLayout) []string {
	var out []string
	for _, ll := range bl.Lines {
		for _, sp := range ll.Spans {
			if !sp.Hidden && strings.TrimSpace(sp.Text) != "" {
				out = append(out, strings.TrimSpace(sp.Text))
			}
		}
	}
	return out
}

// A note's Rendered view draws a line that embeds a diagram or a table, off the cursor, as the
// file written in its place, natively: the diagram asked of the editor's host for the line's own
// block, the table laid out by golib's renderer. Both are the block's picture, so the line keeps
// its caret and clicks; with the cursor on it the line is its source, editable.
func TestARenderedNoteDrawsItsEmbeds(t *testing.T) {
	notes, _ := embedNote(t)
	r := notes.renderer(guiwidget.Diagrams(nil))(nil)
	shaper := gui.NewTextShaper(1)
	th := guiwidget.Theme{Prose: gui.Font{Size: 16}, Mono: gui.Font{Size: 14}}
	lines := []string{"# Note", "", "![The flow](flow.mmd)", "", "![Data](data.csv)", ""}
	flow, data := guiwidget.Block{From: 2, To: 3}, guiwidget.Block{From: 4, To: 5}
	if blocks := r.Blocks(lines, 0, len(lines)); !slices.Contains(blocks, flow) || !slices.Contains(blocks, data) {
		t.Fatalf("the embed lines are not blocks of their own: %v", blocks)
	}

	// the diagram: the host's picture, asked for the line's block with the file's source
	pic := gui.ViewFunc(func(gui.Canvas) {})
	host := &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: pic, Size: gui.Size{W: 200, H: 120}}}
	bl := r.LayOut(flow, lines, 600, false, shaper, th, host)
	if bl.Picture == nil || len(bl.Lines) != 0 || bl.Height != 120 {
		t.Fatalf("the diagram embed: picture %v, %d lines, height %v; want the host's picture alone", bl.Picture != nil, len(bl.Lines), bl.Height)
	}
	if len(host.asked) != 1 || host.asked[0] != flow || host.src[0] != "graph TD\n  a --> b" {
		t.Fatalf("the host was asked %v for %q, want the line's block for the file's source", host.asked, host.src)
	}

	// still drawing: its source as code, in the picture
	host = &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Pending}}
	bl = r.LayOut(flow, lines, 600, false, shaper, th, host)
	code, ok := bl.Picture.(laidBlock)
	if !ok || len(bl.Lines) != 0 || !slices.Contains(spanTexts(code.bl), "graph TD") {
		t.Fatalf("a pending diagram embed is not its source as code: %#v", bl)
	}

	// the table: golib's table, from the CSV in the note's resources folder, painted as a picture
	bl = r.LayOut(data, lines, 600, false, shaper, th, nil)
	table, ok := bl.Picture.(laidBlock)
	if !ok || len(bl.Lines) != 0 || bl.Height <= 0 {
		t.Fatalf("the table embed is not a picture: %#v", bl)
	}
	for _, want := range []string{"name", "count", "claims", "3,000"} {
		if !slices.Contains(spanTexts(table.bl), want) {
			t.Errorf("the table lacks %q: %q", want, spanTexts(table.bl))
		}
	}
	rc := gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "DrawText") {
		t.Errorf("the table's picture drew no text: %v", rc.Ops())
	}

	// with the cursor on it, the line is its source
	host = &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: pic, Size: gui.Size{W: 1, H: 1}}}
	bl = r.LayOut(flow, lines, 600, true, shaper, th, host)
	if bl.Picture != nil || len(bl.Lines) != 1 || bl.Lines[0].Spans[0].Line != 2 || len(host.asked) != 0 {
		t.Fatalf("under the cursor the embed line is not its source: %#v", bl)
	}
}

// Only a line that is an embed alone in its paragraph, of the open note, of a file the view draws,
// is drawn as its file: anything else is laid out as golib lays it.
func TestOnlyAnEmbedAloneIsDrawn(t *testing.T) {
	notes, _ := embedNote(t)
	r := notes.renderer(guiwidget.Diagrams(nil))(nil)
	shaper := gui.NewTextShaper(1)
	th := guiwidget.Theme{Prose: gui.Font{Size: 16}, Mono: gui.Font{Size: 14}}
	for _, lines := range [][]string{
		{"See ![The flow](flow.mmd) here."},
		{"![The flow](flow.mmd)", "more of the paragraph"},
		{"![Missing](missing.mmd)"},
		{"![A picture](pic.png)"},
	} {
		if bl := r.LayOut(guiwidget.Block{From: 0, To: 1}, lines, 600, false, shaper, th, nil); bl.Picture != nil {
			t.Errorf("%q was drawn as a file", lines[0])
		}
	}
	notes.setNote("")
	if bl := r.LayOut(guiwidget.Block{From: 0, To: 1}, []string{"![The flow](flow.mmd)"}, 600, false, shaper, th, nil); bl.Picture != nil {
		t.Error("with no note open an embed was drawn")
	}
}
