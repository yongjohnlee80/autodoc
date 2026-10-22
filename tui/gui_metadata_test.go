//go:build gui

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuiwidget "github.com/yongjohnlee80/golib/tui/widget"
)

func TestNoteReadingViewHidesMetadataWithoutChangingTheBuffer(t *testing.T) {
	source := "---\ntitle: hidden title\ntags: [private]\n---\n<!-- hidden comment\n![not-an-embed](secret.png)\n-->\n# Visible body\n"
	r := noteRenderer(&NoteEmbeds{})(NoteRendererSpec{})
	e := guiwidget.NewEditor(guiwidget.WithRenderer(r))
	e.Core().SetValue(source)
	before := e.Core().Value()
	e.SetView(tuiwidget.ViewRendered)
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
	e.SetView(tuiwidget.ViewRaw)
	if got := e.Core().Value(); got != before {
		t.Fatalf("view switch changed source: %q", got)
	}
}

type metadataObserver struct {
	guiwidget.Renderer
	first string
}

func (r *metadataObserver) ReflowFrom(line int) int {
	if v, ok := r.Renderer.(guiwidget.ReflowRenderer); ok {
		return v.ReflowFrom(line)
	}
	return line
}
func (r *metadataObserver) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, sh *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	bl := r.Renderer.LayOut(b, lines, width, inside, sh, th, host)
	if b.From == 0 {
		var text strings.Builder
		for _, ll := range bl.Lines {
			for _, sp := range ll.Spans {
				if !sp.Hidden {
					text.WriteString(sp.Text)
				}
			}
		}
		r.first = text.String()
	}
	return bl
}

func TestLaterCommentCloseReflowsTheActualNoteEditor(t *testing.T) {
	r := &metadataObserver{Renderer: noteRenderer(&NoteEmbeds{})(NoteRendererSpec{})}
	editor := guiwidget.NewEditor(guiwidget.WithRenderer(r), guiwidget.WithView(tuiwidget.ViewRendered))
	editor.SetValue("before <!-- note\nstill note\nend\n\nlast")
	backend := &nativeBackend{TestBackend: tuicore.NewTestBackend(100, 50, tuicore.WithTestCapabilities(tuicore.Capabilities{NativeViews: true}))}
	app := tuicore.NewApp(editor, tuicore.WithBackend(backend))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for backend.Flushes() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if backend.Flushes() == 0 {
		t.Fatal("no frame")
	}
	onApp := func(fn func()) {
		t.Helper()
		done := make(chan struct{})
		app.Update(func() { fn(); close(done) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("loop stalled")
		}
	}
	paint := func() {
		backend.mu.Lock()
		ps := append([]tuicore.NativePlacement(nil), backend.placements...)
		backend.mu.Unlock()
		for _, p := range ps {
			if v, ok := p.View.(gui.View); ok {
				v.Paint(gui.NewRecordingCanvas(gui.Size{W: float32(p.Cols) * 8, H: float32(p.Rows) * 16}, gui.Size{W: 8, H: 16}))
			}
		}
	}
	onApp(func() {
		editor.Core().SetLine(4, 0)
		paint()
		if r.first != "before <!-- note" {
			t.Errorf("initial projection %q", r.first)
		}
	})
	onApp(func() {
		if _, err := editor.Core().Insert(tuiwidget.TextPos{Line: 2, Col: 3}, " -->"); err != nil {
			t.Error(err)
		}
		editor.Core().SetLine(4, 0)
		paint()
		if r.first != "before " {
			t.Errorf("earlier note layout stale after close: %q", r.first)
		}
		if err := editor.Core().Delete(tuiwidget.TextRange{From: tuiwidget.TextPos{Line: 2, Col: 3}, To: tuiwidget.TextPos{Line: 2, Col: 7}}); err != nil {
			t.Error(err)
		}
		editor.Core().SetLine(4, 0)
		paint()
		if r.first != "before <!-- note" {
			t.Errorf("earlier note layout stale after removing close: %q", r.first)
		}
	})
}
