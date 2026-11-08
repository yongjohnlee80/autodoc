package tui

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// noteImageTUI runs the TUI on a 100×30 terminal reporting caps, with a note on disk whose second
// paragraph is an image alone, the image beside it in the note's folder.
func noteImageTUI(t *testing.T, caps tuicore.Capabilities) *running {
	t.Helper()
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewGray(image.Rect(0, 0, 80, 32))); err != nil {
		t.Fatal(err)
	}
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "n.md", "# Shelf\n\n![a gull](gull.png)\n\nafter the picture\n", "gull.png", pic.String())}, tableInclude)
	h := newHost(NewSession(d.sock, nil), Options{})
	s := decltest.RunOn(t, tuicore.NewTestBackend(100, 30, tuicore.WithTestCapabilities(caps)), h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("n.md"); return true })
	r.waitFile(t, "n.md")
	r.s.WaitFor(t, "the note rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitForText(t, "after the picture")
	return r
}

// An image alone in its paragraph, in a note on disk, is drawn in the Rendered view where the
// terminal draws kitty's graphics (docs/decisions/editor.md#a-terminal-notes-rendered-view): loaded
// from the note's folder, placed over its own rows between the text before and after it, its alt
// text not shown. Where the terminal draws no images, the paragraph is its alt text, and nothing is
// placed.
func TestANotesImageIsDrawnInTheRenderedView(t *testing.T) {
	row := func(sc, s string) int {
		for i, l := range strings.Split(sc, "\n") {
			if strings.Contains(l, s) {
				return i
			}
		}
		return -1
	}
	r := noteImageTUI(t, tuicore.Capabilities{ColorProfile: tuicore.ProfileTrueColor, KittyGraphics: tuicore.TriYes})
	var pl tuicore.ImagePlacement
	r.s.WaitFor(t, "the image placed", func(string) bool {
		imgs := r.s.Backend.Images()
		for _, p := range imgs {
			pl = p
		}
		return len(imgs) == 1
	})
	sc := r.s.String()
	if top, after := row(sc, "Shelf"), row(sc, "after the picture"); pl.Cols == 0 || pl.Rows == 0 || len(pl.PNG) == 0 || !(top < pl.Y && pl.Y+pl.Rows <= after) {
		t.Fatalf("the image placed at rows %d-%d (%d × %d), the text at %d and %d:\n%s", pl.Y, pl.Y+pl.Rows-1, pl.Cols, pl.Rows, top, after, sc)
	}
	if strings.Contains(sc, "a gull") {
		t.Fatalf("the drawn image's alt text shows:\n%s", sc)
	}

	plain := noteImageTUI(t, tuicore.Capabilities{ColorProfile: tuicore.ProfileTrueColor})
	plain.s.WaitForText(t, "a gull")
	time.Sleep(100 * time.Millisecond) // a load that would land has landed
	if n := len(plain.s.Backend.Images()); n != 0 {
		t.Fatalf("a terminal without kitty graphics: %d image(s) placed", n)
	}
}
