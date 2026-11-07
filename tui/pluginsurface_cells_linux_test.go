//go:build linux

package tui

import (
	"bytes"
	"image/png"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// surfaceTerminal is a terminal on which a surface card runs: kitty's graphics confirmed, so its
// frames are pictures (pluginsurface_cells.go). graphics is what the terminal answered.
func surfaceTerminal(t *testing.T, graphics tuicore.Tri, prefs map[string]string) (*running, string, *pluginRun) {
	t.Helper()
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", surfaceCard, 10)
	_, r, logs := cardTUI(t, dir, prefs)
	onLoop(r, func() bool { r.h.graphicsOverride = func() tuicore.Tri { return graphics }; return true })
	run := r.openPlugin("card")
	waitSaid(t, logs, "card", "open ")
	return r, logs, run
}

// images is the terminal's placed images, waited for until there are n.
func waitImages(t *testing.T, r *running, what string, n int) map[uint32]tuicore.ImagePlacement {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := r.s.Backend.Images(); len(got) == n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the terminal holds %d images; want %d:\n%s", what, len(r.s.Backend.Images()), n, r.s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestATerminalShowsASurfaceCardsPixels: on a terminal that confirmed kitty's graphics, a surface
// card gets its surface, sized by its cells, and its frame is drawn as one picture over the card's
// cells. A new size is a new generation and a new frame, the same picture replaced in place, never a
// second one; Plugin pixels turned off while it runs, its picture goes and its cells show, and on
// again it comes back; hidden, the card's picture goes and comes back when it shows; closed, it goes.
func TestATerminalShowsASurfaceCardsPixels(t *testing.T) {
	r, logs, run := surfaceTerminal(t, tuicore.TriYes, nil)
	if onLoop(r, func() bool { return run.surface == nil }) {
		t.Fatal("a terminal that shows pictures gave the card no surface")
	}
	waitSaid(t, logs, "card", "surface gen 1")
	waitSaid(t, logs, "card", "frame.done drawn")
	first := waitImages(t, r, "the first frame", 1)
	var id uint32
	for k := range first {
		id = k
	}
	// placed by the card's columns and rows, whatever its pixels: the terminal scales the picture to
	// them, so it fills the card at any real cell size (9×18, 14×28); its pixels are only its
	// resolution, 10×20 a cell here
	p := first[id]
	cols, rows := onLoop(r, func() int { return run.view.laidW }), onLoop(r, func() int { return run.view.laidH })
	if p.Cols != cols || p.Rows != rows {
		t.Fatalf("the picture is placed over %d×%d cells; want the card's %d×%d", p.Cols, p.Rows, cols, rows)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(p.PNG)); err != nil || cfg.Width != cols*10 || cfg.Height != rows*20 {
		t.Fatalf("the picture is %dx%d px (%v); want the card's cells at 10×20, %dx%d", cfg.Width, cfg.Height, err, cols*10, rows*20)
	}

	r.s.Backend.InjectResize(120, 40) // a new size: the card's cells change, a new generation
	waitSaid(t, logs, "card", "surface gen 2")
	waitSaidN(t, logs, "card", "frame.done drawn", 2)
	if got := waitImages(t, r, "after a new size", 1); got[id].ID != id {
		t.Fatalf("a new size made picture %v; want %d replaced in place", got, id)
	}

	// pictures turned off while the surface is live: its picture goes and its cells show; on again,
	// it comes back
	onLoop(r, func() bool { core{r.h}.SetPref(prefPluginPixels, "false"); return true })
	waitImages(t, r, "Plugin pixels off", 0)
	r.waitShown(t, "open ")
	onLoop(r, func() bool { core{r.h}.SetPref(prefPluginPixels, "true"); return true })
	waitImages(t, r, "Plugin pixels on again", 1)

	panel := cardPanelID("card")
	onLoop(r, func() bool { r.h.hidePanel(panel); return true })
	waitImages(t, r, "the card hidden", 0)
	onLoop(r, func() bool { r.h.showPanel(panel); return true })
	waitImages(t, r, "the card shown again", 1)

	onLoop(r, func() bool { run.close("closed by the test"); return true })
	waitImages(t, r, "the card closed", 0)
}

// TestATerminalSurfaceIsCellsWhereItCantShowPictures: an unconfirmed terminal, image previews off,
// or Preferences › Plugin pixels off: the card gets no surface and draws cells, and no picture is
// placed.
func TestATerminalSurfaceIsCellsWhereItCantShowPictures(t *testing.T) {
	for _, c := range []struct {
		name     string
		graphics tuicore.Tri
		prefs    map[string]string
	}{
		{"graphics unconfirmed", tuicore.TriUnknown, nil},
		{"image previews off", tuicore.TriYes, map[string]string{prefImages: "false"}},
		{"plugin pixels off", tuicore.TriYes, map[string]string{prefPluginPixels: "false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, _, run := surfaceTerminal(t, c.graphics, c.prefs)
			r.waitShown(t, "open ")
			if onLoop(r, func() bool { return run.surface != nil }) {
				t.Fatal("the card got a surface")
			}
			if n := len(r.s.Backend.Images()); n != 0 {
				t.Fatalf("%d pictures placed", n)
			}
		})
	}
}
