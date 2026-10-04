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

const flowFile = "# Flow\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Finish]\n```\n"

// browserWait is how long a test waits for a headless browser's render: seconds alone, longer
// beside a race-detected suite.
const browserWait = 30 * time.Second

// placedImage waits for the terminal to hold one image, and returns its PNG.
func (r *running) placedImage(t *testing.T) []byte {
	t.Helper()
	return r.waitPlaced(t, "an image placed", func(tuicore.ImagePlacement) bool { return true }).PNG
}

// waitPlaced waits, as long as a browser's render may take, for the terminal to hold an image that
// ok accepts, and returns it.
func (r *running) waitPlaced(t *testing.T, what string, ok func(tuicore.ImagePlacement) bool) tuicore.ImagePlacement {
	t.Helper()
	for deadline := time.Now().Add(browserWait); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, p := range r.s.Backend.Images() {
			if ok(p) {
				return p
			}
		}
	}
	t.Fatalf("%s never happened:\n%s", what, r.s)
	return tuicore.ImagePlacement{}
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

// With graphics confirmed and a headless browser, the diagram is drawn by mermaid as an image in
// the theme's colours, placed over the dialog's cells; turning image previews off shows its source
// and says why; a terminal that did not confirm graphics says that.
func TestTheDiagramPreviewIsAnImageWhereItCanBe(t *testing.T) {
	skipWithoutUsableBrowser(t)
	d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", flowFile)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	graphics := tuicore.TriYes
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return graphics }
		r.h.switchTheme("sepia")
	})
	r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	r.h.p.Post(func() { r.h.openPath("f.md") })
	r.waitFile(t, "f.md")
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitForText(t, "drawn offline by mermaid")
	img := r.placedImage(t)
	if got := pixel(t, img, 0, 0); got != "#f4ecd8" {
		t.Fatalf("the image's corner is %s, want sepia's paper #f4ecd8", got)
	}
	if strings.Contains(r.s.String(), "flowchart LR") {
		t.Fatal("the source shows beside the image")
	}
	// closing the dialog takes the image off the screen
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the image gone", func(string) bool { return len(r.s.Backend.Images()) == 0 })

	// image previews off: the source, and why
	r.h.p.Post(func() { r.h.toggleImagePreviews(); r.h.previewDiagram() })
	r.s.WaitForText(t, "image previews are off")
	r.s.WaitForText(t, "flowchart LR")
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

// What the hand-written renderer refused — edge text written "A -- yes --> B", quoted labels with
// line breaks, a state diagram — is drawn, not shown as its source.
func TestTheDiagramsItCouldNotDrawAreDrawn(t *testing.T) {
	skipWithoutUsableBrowser(t)
	for name, block := range map[string]string{
		"flowchart": "flowchart TD\n  A[\"event arrives<br/>at node N\"] --> B{\"pointer<br/>disabled?\"}\n  B -- yes --> P[\"skip N\"]\n  B -- no --> C[resolve]\n",
		"state":     "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Armed: press\n  Armed --> Idle: release\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", "# D\n\n```mermaid\n"+block+"```\n")})
			r := runTUI(t, NewSession(d.sock, nil), Options{})
			r.s.WaitForText(t, "· kb")
			r.h.p.Post(func() { r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes } })
			r.h.p.Post(func() { r.h.openPath("f.md") })
			r.waitFile(t, "f.md")
			r.h.p.Post(func() { r.h.previewDiagram() })
			r.placedImage(t)
			if !onLoop(r, func() bool { return r.h.lastDiagramImage() }) {
				t.Fatalf("the preview fell back: %s", onLoop(r, func() string { return r.h.diagramHelpText }))
			}
		})
	}
}

// The HTML preview renders the whole page and scrolls it: the image is the page's full height at
// the dialog's width, j and Page Down move the part shown without sending the image again, and
// Zoom in draws the page again larger in the same dialog, at the same place in it.
func TestTheHTMLPreviewScrollsAndZooms(t *testing.T) {
	skipWithoutUsableBrowser(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	long := "# Long page\n\n" + strings.Repeat("A paragraph of the long page, to scroll through.\n\n", 80)
	d := startManaged(t, map[string]string{"kb": fileDir(t, "n.md", long)})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.browser = func(context.Context, string) error { return nil }
	})
	r.h.p.Post(func() { r.h.openPath("n.md") })
	r.waitFile(t, "n.md")
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitForText(t, "HTML preview · n.md")
	first := r.placedImage(t)
	placed := func() tuicore.ImagePlacement {
		for _, p := range r.s.Backend.Images() {
			return p
		}
		return tuicore.ImagePlacement{}
	}
	p0 := placed()
	w, h := pngSize(t, first)
	if w != p0.Cols*widget.CellPixelsW || h <= p0.Rows*widget.CellPixelsH {
		t.Fatalf("the page is %d×%d for %d×%d cells: not the whole page at the dialog's width", w, h, p0.Cols, p0.Rows)
	}
	if p0.Clip != (tuicore.Rect{W: w, H: p0.Rows * widget.CellPixelsH}) {
		t.Fatalf("the first view is %+v, want the top of the page", p0.Clip)
	}
	r.keys(t, key('j'), tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown})
	r.s.WaitFor(t, "scrolled", func(string) bool { return placed().Clip.Y == p0.Rows*widget.CellPixelsH })
	if p := placed(); p.Version != p0.Version {
		t.Fatal("a scroll made a new image")
	}

	r.keys(t, key('i')) // Zoom in
	r.waitPlaced(t, "the zoomed page", func(p tuicore.ImagePlacement) bool { return p.Version != p0.Version })
	_, zh := pngSize(t, placed().PNG)
	if zh <= h {
		t.Fatalf("zoomed in, the page is %d tall, not taller than %d", zh, h)
	}
	if p := placed(); p.Cols != p0.Cols || p.Rows != p0.Rows || p.Clip.Y == 0 {
		t.Fatalf("zoomed, the image is %d×%d cells from row %d: the dialog changed, or the place in the page was lost", p.Cols, p.Rows, p.Clip.Y)
	}
	for range len(zooms) {
		r.h.p.Post(func() { r.h.zoomPreview(1) })
	}
	r.s.WaitFor(t, "the zoom at its largest", func(string) bool {
		return onLoop(r, func() bool { return r.h.imagePreview.zoom == len(zooms)-1 })
	})
}

func pngSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

// With graphics and a headless browser, HTML preview is an image of the exported page; Open in
// browser opens the very file it was made from.
func TestTheHTMLPreviewIsAnImageWhereItCanBe(t *testing.T) {
	skipWithoutUsableBrowser(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d := startManaged(t, map[string]string{"kb": fileDir(t, "n.md", "# Light page\n\ntext\n")})
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
	r.waitFile(t, "n.md")
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

// tinyPreview runs the TUI on a w×h screen with confirmed graphics, flowFile open.
func tinyPreview(t *testing.T, w, h int) (*running, chan string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d := startManaged(t, map[string]string{"kb": fileDir(t, "f.md", flowFile)})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, w, h)
	r.s.WaitFor(t, "the workspace", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	opened := make(chan string, 4)
	r.h.p.Post(func() {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil }
		r.h.openPath("f.md")
	})
	r.s.WaitFor(t, "f.md open", func(string) bool { n := r.file(); return n.open && n.path == "f.md" })
	return r, opened
}

// On a screen too small for an image the Mermaid preview falls back to the source, saying why,
// rather than staying a blank image area (Lector's review of #30, finding 1).
func TestATinyScreenDiagramFallsBackToItsSource(t *testing.T) {
	if _, ok := widget.HTMLRasterizer(); !ok {
		t.Skip("no headless browser: image mode is never entered")
	}
	r, _ := tinyPreview(t, 12, 3)
	r.h.p.Post(func() { r.h.previewDiagram() })
	r.s.WaitFor(t, "the fallback", func(string) bool {
		return onLoop(r, func() bool { return !r.h.lastDiagramImage() })
	})
	help := onLoop(r, func() string { return r.h.diagramHelpText })
	if !strings.HasPrefix(help, "Diagram source · ") || !(strings.Contains(help, "too small") || strings.Contains(help, "never laid out")) {
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
	skipWithoutUsableBrowser(t)
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

// skipWithoutUsableBrowser skips where no headless browser can render here: none installed, or one
// that refuses to run without its sandbox (a CI runner whose AppArmor forbids user namespaces; the
// rasterizer never turns the sandbox off, and the preview falls back to the browser). Any other
// failure of the probe is a failure.
func skipWithoutUsableBrowser(t *testing.T) {
	t.Helper()
	if _, ok := widget.HTMLRasterizer(); !ok {
		t.Skip("no headless Chromium or Chrome")
	}
	_, err := widget.RasterizeHTML(context.Background(), []byte("<p>probe</p>"), 64, 32)
	if err != nil && strings.Contains(err.Error(), "No usable sandbox") {
		t.Skip("the installed browser has no usable sandbox here")
	}
	if err != nil {
		t.Fatalf("the browser probe failed: %v", err)
	}
}

// The mismatch dialog's edges: without a spawner Restart Now does nothing and Quit quits; on a
// connected session there is nothing to recover; a stop that does not complete stays in the dialog
// with the reason and a retry.
func TestTheMismatchDialogsEdges(t *testing.T) {
	t.Run("no spawner, then Quit", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "s.sock")
		otherDaemon(t, sock, rpc.Protocol-1)
		r := runTUI(t, NewSession(sock, nil), Options{})
		r.s.WaitFor(t, "the dialog", func(sc string) bool { return strings.Contains(sc, "backend version mismatch") })
		r.h.p.Post(r.h.restartMismatch) // nothing would bring a backend back: a no-op
		r.h.p.Post(r.h.quitMismatch)
		select {
		case <-r.s.Quit():
		case <-time.After(3 * time.Second):
			t.Fatal("Quit did not quit")
		}
	})
	t.Run("connected: nothing to recover", func(t *testing.T) {
		d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
		r := runTUI(t, NewSession(d.sock, func() (string, error) { return "", errors.New("no spawn") }), Options{})
		r.s.WaitForText(t, "· kb")
		r.h.p.Post(r.h.restartMismatch)
		if onLoop(r, func() bool { return r.h.mismatchOpen || r.h.mismatchRecovery }) {
			t.Fatal("a connected session started a recovery")
		}
	})
	t.Run("a stop that does not complete", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "adm")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		sock := filepath.Join(dir, "s.sock")
		otherDaemon(t, sock, rpc.Protocol-1)
		r := runTUI(t, NewSession(sock, func() (string, error) { return "", errors.New("unused") }), Options{})
		r.s.WaitFor(t, "the dialog", func(sc string) bool { return strings.Contains(sc, "Restart Now") })
		onLoop(r, func() bool {
			r.h.awaitExit = func(context.Context, int64) bool { return false } // it never goes
			return true
		})
		r.h.p.Post(r.h.restartMismatch)
		r.s.WaitFor(t, "the failure, actionable", func(sc string) bool {
			flat := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
			return strings.Contains(flat, "Restart failed:") && strings.Contains(flat, "has not stopped")
		})
		// a toast may lie over the buttons on screen: the dialog's own state says Restart Now is there
		if !onLoop(r, func() bool { return r.h.mismatchOpen && r.h.mismatchRecovery }) {
			t.Fatal("the failure is not in the recovery dialog")
		}
	})
}
