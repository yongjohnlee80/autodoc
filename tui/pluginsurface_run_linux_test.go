//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
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

// The pointer reaches a surface in frame-local dp for each kind it takes, from the event's cell and
// its place inside the cell, with its modifiers; an event it doesn't take (no button, a kind it
// doesn't know), or one before the view is laid out, isn't sent. A terminal's view has no native view.
func TestASurfacePointerIsInDpForEachKind(t *testing.T) {
	if p, ok := (&pluginView{}).surfacePointer(tuicore.MouseEvent{Kind: tuicore.MouseMotion}); ok {
		t.Errorf("a view never laid out sent %+v", p)
	}
	withFakeSurfaces(t)
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", surfaceCard, 10)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	waitSaid(t, logs, "card", "open ")
	onLoop(r, func() bool {
		v := run.view
		if _, ok := v.NativeView(); ok {
			t.Error("a terminal's view has a native view")
		}
		cp := v.Context().CellPixels()
		for _, c := range []struct {
			m    tuicore.MouseEvent
			want plugin.SurfacePointer
		}{
			{tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, Mods: tuicore.ModCtrl | tuicore.ModShift, Count: 2},
				plugin.SurfacePointer{Kind: "press", Button: "right", Count: 2, Ctrl: true, Shift: true}},
			{tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseMiddle, Mods: tuicore.ModAlt | tuicore.ModSuper},
				plugin.SurfacePointer{Kind: "release", Button: "middle", Alt: true, Super: true}},
			{tuicore.MouseEvent{Kind: tuicore.MouseMotion}, plugin.SurfacePointer{Kind: "move"}},
			{tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelUp}, plugin.SurfacePointer{Kind: "scroll", DY: -cp.H}},
			{tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown}, plugin.SurfacePointer{Kind: "scroll", DY: cp.H}},
			{tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelLeft}, plugin.SurfacePointer{Kind: "scroll", DX: -cp.W}},
			{tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelRight}, plugin.SurfacePointer{Kind: "scroll", DX: cp.W}},
		} {
			c.m.X, c.m.Y, c.m.SubX, c.m.SubY = 3, 2, 0.5, 0.25
			got, ok := v.surfacePointer(c.m)
			c.want.X, c.want.Y = 3.5*cp.W, 2.25*cp.H
			c.want.T = got.T
			if !ok || got != c.want || got.T == 0 {
				t.Errorf("%+v: %+v (%v), want %+v", c.m, got, ok, c.want)
			}
		}
		for _, m := range []tuicore.MouseEvent{
			{Kind: tuicore.MousePress, Button: tuicore.MouseNone},
			{Kind: tuicore.MouseKind(99)},
		} {
			if p, ok := v.surfacePointer(m); ok {
				t.Errorf("%+v was sent as %+v", m, p)
			}
			if v.HandleEvent(m) {
				t.Errorf("%+v was taken", m)
			}
		}
		return true
	})
}

// A card whose surface has its own context menu gets the right press, and the host opens none.
func TestASurfaceWithItsOwnMenuGetsTheRightPress(t *testing.T) {
	withFakeSurfaces(t)
	dir := t.TempDir()
	installPluginAt(t, dir, "card", "dialog", "echo", rightCard+"[surface]\ncontext_menu = \"plugin\"\n", 10)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	waitSaid(t, logs, "card", "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: x, Y: y, Count: 1})
	waitSaid(t, logs, "card", "spointer press right")
	if onLoop(r, func() bool { return run.view.menu != nil }) {
		t.Error("the host opened its menu over the plugin's own")
	}
}
