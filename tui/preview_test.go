package tui

import (
	"bytes"
	"context"
	"image/png"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
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
