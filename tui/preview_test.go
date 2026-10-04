package tui

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/rpc"
)

const flowNote = "# Flow\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Finish]\n```\n"

// placedImage waits for the terminal to hold one image, and returns its PNG.
func (r *running) placedImage(t *testing.T) []byte {
	t.Helper()
	var got []byte
	r.s.WaitFor(t, "an image placed", func(string) bool {
		for _, p := range r.s.Backend.Images() {
			got = p.PNG
			return true
		}
		return false
	})
	return got
}

// pixel is the PNG's colour at (x, y) as #rrggbb.
func pixel(t *testing.T, b []byte, x, y int) string {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	r, g, bl, _ := img.At(x, y).RGBA()
	return "#" + hex2(r>>8) + hex2(g>>8) + hex2(bl>>8)
}

func hex2(v uint32) string {
	return string("0123456789abcdef"[v>>4]) + string("0123456789abcdef"[v&15])
}

// With graphics confirmed and rsvg-convert installed, the diagram is an image in the theme's
// colours, placed over the dialog's cells; turning image previews off shows the terminal graph and
// says why; a terminal that did not confirm graphics says that.
func TestTheDiagramPreviewIsAnImageWhereItCanBe(t *testing.T) {
	if _, ok := widget.SVGRasterizer(); !ok {
		t.Skip("rsvg-convert is not installed")
	}
	d := startManaged(t, map[string]string{"kb": noteDir(t, "f.md", flowNote)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	graphics := tuicore.TriYes
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return graphics }
		r.h.switchTheme("sepia")
	})
	r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	r.h.p.Post(func() { r.h.openPath("f.md") })
	r.waitNote(t, "f.md")
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitForText(t, "rendered offline by rsvg-convert in the sepia theme")
	img := r.placedImage(t)
	if got := pixel(t, img, 0, 0); got != "#f4ecd8" {
		t.Fatalf("the image's corner is %s, want sepia's paper #f4ecd8", got)
	}
	if strings.Contains(r.s.String(), "Start ──") {
		t.Fatal("the terminal graph shows beside the image")
	}
	// closing the dialog takes the image off the screen
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the image gone", func(string) bool { return len(r.s.Backend.Images()) == 0 })

	// image previews off: the terminal graph, and why
	r.h.p.Post(func() { r.h.toggleImagePreviews(); r.h.previewDiagram() })
	r.s.WaitForText(t, "image previews are off")
	if len(r.s.Backend.Images()) != 0 {
		t.Fatal("an image with image previews off")
	}
	r.keys(t, key('q'))

	// on again, on a terminal that did not confirm graphics
	r.h.p.Post(func() { graphics = tuicore.TriUnknown; r.h.toggleImagePreviews(); r.h.previewDiagram() })
	r.s.WaitForText(t, "did not confirm kitty graphics")
	if len(r.s.Backend.Images()) != 0 {
		t.Fatal("an image on an unconfirmed terminal")
	}
}

// With graphics and a headless browser, HTML preview is an image of the exported page; Open in
// browser opens the very file it was made from.
func TestTheHTMLPreviewIsAnImageWhereItCanBe(t *testing.T) {
	if _, ok := widget.HTMLRasterizer(); !ok {
		t.Skip("no headless Chromium or Chrome")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d := startManaged(t, map[string]string{"kb": noteDir(t, "n.md", "# Light page\n\ntext\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	opened := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil }
		r.h.switchTheme("light")
	})
	r.s.WaitFor(t, "light", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "light" })
	r.h.p.Post(func() { r.h.openPath("n.md") })
	r.waitNote(t, "n.md")
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitForText(t, "HTML preview · n.md")
	img := r.placedImage(t)
	if got := pixel(t, img, 2, 2); got != "#f6f2e7" {
		t.Fatalf("the page's corner is %s, want light's paper #f6f2e7", got)
	}
	select {
	case p := <-opened:
		t.Fatalf("the browser opened (%s) while the image showed", p)
	default:
	}
	r.h.p.Post(func() { r.h.openPreviewInBrowser() })
	select {
	case p := <-opened:
		if p != onLoop(r, func() string { return r.h.htmlPreviewPath }) || !strings.HasSuffix(p, ".html") {
			t.Fatalf("opened %q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open in browser opened nothing")
	}
}

// A recovery restart whose installed backend does not come up stays in its dialog, saying why, and
// Restart Now tries the start again (ADR 0212 §8: a failed restart remains actionable).
func TestAFailedRecoveryRestartStaysActionable(t *testing.T) {
	dir, err := os.MkdirTemp("", "adf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	old := otherDaemon(t, sock, rpc.Protocol-1)
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		switch spawns.Add(1) {
		case 1:
			return "", errors.New("the binary is missing")
		case 2:
			startDaemonWith(t, sock, map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{version: "v2"})
			return "", nil
		}
		return "", errors.New("spawned already") // the test's end drops the connection
	})
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "the mismatch dialog", func(sc string) bool { return strings.Contains(sc, "Restart Now") })
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool {
			select {
			case <-old:
				return true
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	r.h.p.Post(r.h.restartMismatch)
	r.s.WaitFor(t, "the failure in the dialog", func(sc string) bool {
		flat := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
		return strings.Contains(flat, "The installed backend did not start") && strings.Contains(sc, "Restart Now")
	})
	r.h.p.Post(r.h.restartMismatch)
	r.s.WaitForText(t, "autodoc v2 · kb")
	if n := spawns.Load(); n != 2 {
		t.Fatalf("%d spawns, want the failed one and the retry", n)
	}
}

// tinyPreview runs the TUI on a w×h screen with confirmed graphics, flowNote open.
func tinyPreview(t *testing.T, w, h int) (*running, chan string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d := startManaged(t, map[string]string{"kb": noteDir(t, "f.md", flowNote)})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, w, h)
	r.s.WaitFor(t, "the workspace", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	opened := make(chan string, 4)
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil }
		r.h.openPath("f.md")
	})
	r.s.WaitFor(t, "f.md open", func(string) bool { n := r.note(); return n.open && n.path == "f.md" })
	return r, opened
}

// On a screen too small for an image the Mermaid preview falls back to the terminal graph, saying
// why, rather than staying a blank image area (Lector's review of #30, finding 1).
func TestATinyScreenDiagramFallsBackToTheTerminalGraph(t *testing.T) {
	if _, ok := widget.SVGRasterizer(); !ok {
		t.Skip("rsvg-convert is not installed: image mode is never entered")
	}
	r, _ := tinyPreview(t, 12, 3)
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitFor(t, "the fallback", func(string) bool {
		return onLoop(r, func() bool { return !r.h.lastDiagramImage() })
	})
	help := onLoop(r, func() string { return r.h.diagramHelpText })
	if !strings.HasPrefix(help, "Terminal graph · ") || !(strings.Contains(help, "too small") || strings.Contains(help, "never laid out")) {
		t.Fatalf("help = %q", help)
	}
	if len(r.s.Backend.Images()) != 0 {
		t.Fatal("an image was placed on a screen too small for one")
	}
}

// On a screen too small for an image the HTML preview opens the browser instead.
func TestATinyScreenHTMLFallsBackToTheBrowser(t *testing.T) {
	if _, ok := widget.HTMLRasterizer(); !ok {
		t.Skip("no headless browser: image mode is never entered")
	}
	r, opened := tinyPreview(t, 12, 3)
	r.h.p.Post(func() { r.h.previewHTML() })
	select {
	case p := <-opened:
		if !strings.HasSuffix(p, ".html") {
			t.Fatalf("opened %q", p)
		}
	case <-time.After(imageWait + 3*time.Second):
		t.Fatal("the HTML preview neither showed an image nor opened the browser")
	}
	if len(r.s.Backend.Images()) != 0 {
		t.Fatal("an image was placed on a screen too small for one")
	}
}

// A screen resized to a usable size before the wait ends renders the image after all.
func TestAPreviewRendersOnceAResizeGivesItRoom(t *testing.T) {
	if _, ok := widget.SVGRasterizer(); !ok {
		t.Skip("rsvg-convert is not installed")
	}
	r, _ := tinyPreview(t, 12, 3)
	r.h.p.Post(func() { r.h.previewDiagram() })
	time.Sleep(200 * time.Millisecond)
	r.s.Backend.InjectResize(100, 30)
	img := r.placedImage(t)
	if pixel(t, img, 0, 0) == "" {
		t.Fatal("no PNG")
	}
	if !onLoop(r, func() bool { return r.h.lastDiagramImage() }) {
		t.Fatal("the preview fell back although the resize gave it room")
	}
}
