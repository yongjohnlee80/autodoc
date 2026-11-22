//go:build gui

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/gui"
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// folderRenderer records, for each layout of a line embedding data.csv, the line and the note file
// NoteEmbeds held then: the folder the embed was read from.
type folderRenderer struct {
	guiwidget.Renderer
	notes *NoteEmbeds
	mu    sync.Mutex
	laid  []laidEmbed
	all   int // every layout, of any block
}

type laidEmbed struct{ line, note string }

func (f *folderRenderer) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	f.mu.Lock()
	f.all++
	f.mu.Unlock()
	for _, l := range lines[b.From:b.To] {
		if strings.Contains(l, "](data.csv)") {
			f.mu.Lock()
			f.laid = append(f.laid, laidEmbed{line: l, note: f.notes.file()})
			f.mu.Unlock()
		}
	}
	return f.Renderer.LayOut(b, lines, width, inside, t, th, host)
}

func (f *folderRenderer) take() []laidEmbed {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.laid
	f.laid = nil
	return out
}

// TestASwitchBackLaysANotesEmbedsFromItsOwnFolder: two notes in two folders each embed a data.csv
// of their own. Switching back to the first, a buffer the editor has shown before, lays its visible
// blocks at once; each embed is read from the first note's folder, never the second's, which the
// window was told last, so the first note's table is drawn and not the second's.
func TestASwitchBackLaysANotesEmbedsFromItsOwnFolder(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"alpha/note.md": "# Alpha\n\n![data A](data.csv)\n\nafter\n", "alpha/data.csv": "name\nalpha\n",
		"beta/note.md": "# Beta\n\n![data B](data.csv)\n\nafter\n", "beta/data.csv": "name\nbeta\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := startManagedIncluding(t, map[string]string{"kb": dir}, tableInclude)
	notes := &NoteEmbeds{}
	rec := &folderRenderer{notes: notes}
	factory := notes.Renderer(nil)
	style := guidecl.WithRendererFor(feature.NoteRendererKind, func(s tuidecl.RendererSpec) guiwidget.Renderer {
		rec.Renderer = factory(s)
		return rec
	})
	r, nb := runNativeTUI(t, NewSession(d.sock, nil), Options{GUI: true, NoteFile: notes.SetNote,
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native(style))}})
	r.waitAttached(t, "kb")
	r.waitListed(t, 4) // the two notes and their tables
	// paint paints the native views the last frame placed, as a window does, on the loop: the
	// editor's body takes its size and lays out the blocks it shows
	paint := func() {
		nb.mu.Lock()
		ps := append([]tuicore.NativePlacement(nil), nb.placements...)
		nb.mu.Unlock()
		for _, p := range ps {
			if v, ok := p.View.(gui.View); ok {
				v.Paint(gui.NewRecordingCanvas(gui.Size{W: float32(p.Cols) * 8, H: float32(p.Rows) * 16}, gui.Size{W: 8, H: 16}))
			}
		}
	}
	shown := func(p string) {
		t.Helper()
		r.openByPicker(t, p)
		r.waitFile(t, p)
		r.waitBuffer(t, p)
		onLoop(r, func() bool { paint(); return true }) // the body placed by an earlier frame: the same view
	}
	alpha := onLoop(r, func() string { return r.h.pathOnDisk("kb", "alpha/note.md") })
	beta := onLoop(r, func() string { return r.h.pathOnDisk("kb", "beta/note.md") })
	shown("alpha/note.md")
	if laid := rec.take(); !slices.Contains(laid, laidEmbed{line: "![data A](data.csv)", note: alpha}) {
		diag := onLoop(r, func() string {
			e := r.h.editor.(*guiwidget.Editor)
			return fmt.Sprintf("view %v shown %v, layouts of any block %d, text %q", e.View(), e.ShownView(), rec.all, r.h.core.Value())
		})
		t.Fatalf("alpha's embed was not laid out from alpha's folder when it was shown: %+v (%s)", laid, diag)
	}
	shown("beta/note.md")
	rec.take()

	laid := onLoop(r, func() []laidEmbed {
		r.h.activate(r.h.bufferOf(feature.FileRef{WS: "kb", Path: "alpha/note.md"}))
		paint()
		return rec.take()
	})
	if r.file().path != "alpha/note.md" {
		t.Fatalf("the buffer shown is %q, want alpha/note.md", r.file().path)
	}
	if len(laid) == 0 {
		t.Fatal("switching back to alpha laid out no embed: the switch shows nothing to check")
	}
	for _, l := range laid {
		if l.note != alpha {
			t.Errorf("%q was laid out from %s, want alpha's own folder (%s); beta's is %s", l.line, l.note, alpha, beta)
		}
	}
	if last := laid[len(laid)-1]; last.note != alpha {
		t.Errorf("the layout drawn last for %q read %s: beta's table is what alpha shows", last.line, last.note)
	}
}

// runNativeTUI is runTUI on a terminal that draws native views, as the window's is, keeping each
// frame's placements: decltest.RunOn makes the backend its own, so the program is run here.
func runNativeTUI(t *testing.T, sess *Session, opt Options) (*running, *nativeBackend) {
	t.Helper()
	if opt.seams == nil {
		opt.seams = seamsFor(t)
	}
	nb := &nativeBackend{TestBackend: tuicore.NewTestBackend(100, 30, tuicore.WithTestCapabilities(tuicore.Capabilities{NativeViews: true}))}
	h := newHost(sess, opt)
	all := append([]tuidecl.ProgramOption{tuidecl.ErrorSink(func(err error) { t.Errorf("handler error: %v", err) })}, h.options(opt)...)
	all = append(all, tuidecl.AppOptions(tuicore.WithBackend(nb), tuicore.WithMinFrameInterval(0)))
	p, err := tuidecl.NewProgram(all...)
	if err != nil {
		t.Fatalf("the program did not mount: %v", err)
	}
	if err := h.attach(p); err != nil {
		t.Fatalf("attach: %v", errors.Join(err, p.Tree().Destroy()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(decltest.WaitTimeout):
			t.Error("the program did not stop")
		}
	})
	t.Cleanup(h.cancel)
	return &running{h: h, s: &decltest.Screen{Program: p, Backend: nb.TestBackend}}, nb
}
