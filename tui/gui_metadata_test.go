//go:build gui

package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

func TestNoteReadingViewHidesMetadataWithoutChangingTheBuffer(t *testing.T) {
	source := "---\ntitle: hidden title\ntags: [private]\n---\n<!-- hidden comment\n![not-an-embed](secret.png)\n-->\n# Visible body\n"
	r := noteRenderer(&NoteEmbeds{})(NoteRendererSpec{})
	e := guiwidget.NewEditor(guiwidget.WithRenderer(r))
	e.Core().SetValue(source)
	before := e.Core().Value()
	e.SetMode(guiwidget.Rendered)
	lines := strings.Split(e.Core().Value(), "\n")
	sh := gui.NewRecordingCanvas(gui.Size{W: 500, H: 500}, gui.Size{W: 8, H: 16}).Text()
	th := guiwidget.Theme{Prose: gui.Font{Size: 16}, Mono: gui.Font{Size: 14}}
	var visible strings.Builder
	for _, b := range r.Blocks(lines, 0, len(lines)) {
		bl := r.LayOut(b, lines, 400, false, sh, th, nil)
		for _, ll := range bl.Lines {
			for _, sp := range ll.Spans {
				if !sp.Hidden {
					visible.WriteString(sp.Text)
				}
			}
		}
		if b.From < 7 && (bl.Height != 0 || bl.Picture != nil) {
			t.Errorf("metadata takes reading space or draws an embed: block %v %+v", b, bl)
		}
	}
	if text := visible.String(); !strings.Contains(text, "Visible body") || strings.Contains(text, "hidden") || strings.Contains(text, "secret.png") {
		t.Fatalf("reading text %q", text)
	}
	e.SetMode(guiwidget.Raw)
	if got := e.Core().Value(); got != before {
		t.Fatalf("view switch changed source: %q", got)
	}
}
