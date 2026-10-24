//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// surfaceCard is a protocol-10 card with a pixel surface that takes text.
const surfaceCard = rightCard + "[surface]\ntext_input = true\n"

// withFakeSurfaces gives every surface plugin a fake sink in place of a window's stream, for the
// test: the host's whole surface path runs, with frames drawn at once.
func withFakeSurfaces(t *testing.T) *fakeSink {
	t.Helper()
	sink := &fakeSink{auto: "drawn"}
	was := surfaceSinkOf
	surfaceSinkOf = func(*plugins) (surfaceSink, any, bool) { return sink, nil, true }
	t.Cleanup(func() { surfaceSinkOf = was })
	return sink
}

// A protocol-10 card runs its surface end to end: it gets the side channel as fd 3, a generation's
// pool, presents a frame and hears it drawn; its caret and clipboard write reach the host; the
// pointer reaches it in dp, with press, release, move and scroll; the user's paste reaches it; and
// a right press still opens the card's menu.
func TestAProtocolTenCardRunsItsSurface(t *testing.T) {
	withFakeSurfaces(t)
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", surfaceCard, 10)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	waitSaid(t, logs, "card", "open ")
	onLoop(r, func() bool {
		if run.surface == nil {
			t.Error("a protocol-10 card with [surface] got no surface")
			return false
		}
		run.surface.Resize(rigGeometry) // the geometry a window's paint would report
		return true
	})
	waitSaid(t, logs, "card", "surface gen 1")
	waitSaid(t, logs, "card", "frame.done drawn")
	deadline := time.Now().Add(10 * time.Second)
	for !onLoop(r, func() bool { run.mu.Lock(); defer run.mu.Unlock(); return run.caretOK }) {
		if time.Now().After(deadline) {
			t.Fatal("the plugin's caret never reached the host")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for string(r.s.Backend.Clipboard()) != "from the plugin" {
		if time.Now().After(deadline) {
			t.Fatalf("the clipboard holds %q", r.s.Backend.Clipboard())
		}
		time.Sleep(10 * time.Millisecond)
	}
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y, Count: 1})
	waitSaid(t, logs, "card", "spointer press left")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	waitSaid(t, logs, "card", "spointer release left")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseMotion, X: x + 1, Y: y})
	waitSaid(t, logs, "card", "spointer move")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: x, Y: y})
	waitSaid(t, logs, "card", "spointer scroll")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseMiddle, X: x, Y: y, Count: 1})
	waitSaid(t, logs, "card", "spointer press middle")
	r.keys(t, tuicore.PasteEvent{Text: "pasted"})
	waitSaid(t, logs, "card", "paste pasted")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: x, Y: y, Count: 1})
	r.waitShown(t, "Close (Esc)") // context_menu = "host": the card's own menu
}

// A surface plugin whose manifest requires pixels isn't opened where the host draws none.
func TestARequiredSurfaceIsNotOpenedWithoutPixels(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", rightCard+"[surface]\nrequired = true\n", 10)
	_, r, _ := cardTUI(t, dir, nil)
	if run := r.openPlugin("card"); run != nil {
		t.Fatal("a plugin that requires pixels ran where there are none")
	}
}

// Without pixels a surface plugin runs on cells, as any plugin does.
func TestASurfacePluginDrawsCellsWithoutPixels(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", surfaceCard, 10)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	waitSaid(t, logs, "card", "open ")
	if onLoop(r, func() bool { return run.surface != nil }) {
		t.Error("a terminal host gave a surface")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "card", "plugin.toml"))
	if !strings.Contains(string(b), "protocol = 10") {
		t.Fatal("the manifest isn't protocol 10")
	}
}
