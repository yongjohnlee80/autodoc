// Package hosttest plays AutoDoc's side of the plugin protocol for the examples' tests: it runs a
// Handler under plugin.ServeConn over a pair of pipes, as AutoDoc runs a plugin's process, sends
// the host's notifications and reads the frames the plugin sends back. It opens the example at the
// protocol its plugin.toml says, as AutoDoc does, and sends the pointer as AutoDoc sends it at that
// protocol.
package hosttest

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/manifests"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// Host is a test's end of the link.
type Host struct {
	t        *testing.T
	link     *plugin.Link
	got      chan note
	last     string // the last frame read, a row per line
	protocol int    // the protocol the plugin was opened at: its manifest's
}

type note struct {
	method string
	params []any
}

// Start serves h and opens it at w×h in theme, at the protocol of the plugin.toml in the test's
// folder (the example's own), as AutoDoc opens it. A manifest AutoDoc would refuse fails the test,
// with AutoDoc's reason.
func Start(t *testing.T, h plugin.Handler, w, hh int, theme plugin.Theme) *Host {
	t.Helper()
	m := Manifest(t)
	toPlugin, fromHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromPlugin, toHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- plugin.ServeConn(ctx, plugin.FileConn(toPlugin, toHost), h) }()
	hs := &Host{t: t, got: make(chan note, 256), protocol: m.Protocol}
	hs.link, err = plugin.NewLink(ctx, plugin.FileConn(fromPlugin, fromHost), func(m string, p []any) { hs.got <- note{m, p} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hs.link.Notify(context.Background(), plugin.MethodClose, plugin.EmptyParams())
		select {
		case <-served:
		// NOTE: a test's bound: a plugin that never stops after plugin.close fails the test
		// instead of hanging it; ServeConn's return itself is waited on.
		case <-time.After(5 * time.Second):
			t.Error("the plugin did not stop after plugin.close")
		}
		cancel()
		_ = hs.link.Close()
	})
	hs.send(plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: m.Protocol, Width: w, Height: hh, Theme: theme}))
	return hs
}

// Manifest is the plugin.toml in the test's folder, as AutoDoc reads it; one AutoDoc would refuse
// fails the test, with AutoDoc's reason.
func Manifest(t *testing.T) *manifests.Plugin {
	t.Helper()
	src, err := os.ReadFile("plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifests.ParsePlugin(src)
	if err != nil {
		t.Fatalf("AutoDoc refuses this plugin.toml: %v", err)
	}
	return m
}

// Protocol is the protocol the plugin was opened at.
func (hs *Host) Protocol() int { return hs.protocol }

func (hs *Host) send(method string, params []any) {
	hs.t.Helper()
	if err := hs.link.Notify(context.Background(), method, params); err != nil {
		hs.t.Fatalf("%s: %v", method, err)
	}
}

// Key types k into the plugin.
func (hs *Host) Key(k plugin.Key) { hs.t.Helper(); hs.send(plugin.MethodKey, plugin.KeyParams(k)) }

// Type types each character of s.
func (hs *Host) Type(s string) {
	hs.t.Helper()
	for _, r := range s {
		hs.Key(plugin.Key{Key: string(r), Text: string(r)})
	}
}

// Press is a left click at (x, y) in the frame, as AutoDoc sends one: the press (protocol 6), and
// from protocol 12 its release there.
func (hs *Host) Press(x, y int) { hs.t.Helper(); hs.PressCount(x, y, 1) }

// PressCount is Press as the count-th press of a multiple click: 2 for a double click's second.
func (hs *Host) PressCount(x, y, count int) {
	hs.t.Helper()
	hs.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: count})
	if hs.protocol >= 12 {
		hs.Pointer(plugin.Pointer{Kind: "release", Button: "left", X: x, Y: y, Count: 1})
	}
}

// Drag presses the left button at (x0, y0), drags it to (x1, y1) a cell at a time along each axis,
// as AutoDoc sends a drag only when the pointer reaches another cell, and releases it there
// (protocol 12; below it AutoDoc sends no drag, and the test fails).
func (hs *Host) Drag(x0, y0, x1, y1 int) {
	hs.t.Helper()
	if hs.protocol < 12 {
		hs.t.Fatalf("a drag at protocol %d: AutoDoc sends one from 12", hs.protocol)
	}
	hs.Pointer(plugin.Pointer{Kind: "press", Button: "left", X: x0, Y: y0, Count: 1})
	step := func(a, b int) int {
		switch {
		case a < b:
			return a + 1
		case a > b:
			return a - 1
		}
		return a
	}
	for x, y := x0, y0; x != x1 || y != y1; {
		x, y = step(x, x1), step(y, y1)
		hs.Pointer(plugin.Pointer{Kind: "drag", Button: "left", X: x, Y: y, Count: 1})
	}
	hs.Pointer(plugin.Pointer{Kind: "release", Button: "left", X: x1, Y: y1, Count: 1})
}

// Pointer sends one pointer event as it is: a press, a drag, a release, a cancel, a wheel step.
func (hs *Host) Pointer(at plugin.Pointer) {
	hs.t.Helper()
	hs.send(plugin.MethodPointer, plugin.PointerParams(at))
}

// Theme switches the theme.
func (hs *Host) Theme(t plugin.Theme) {
	hs.t.Helper()
	hs.send(plugin.MethodTheme, plugin.ThemeParams(t))
}

// Screen waits for the screen to hold want, the last frame read or one after it, and returns that
// frame, a row per line.
func (hs *Host) Screen(want string) string {
	hs.t.Helper()
	return hs.WaitFor(strconv.Quote(want), func(screen string) bool { return strings.Contains(screen, want) })
}

// WaitFor waits for a frame, the last read or one after it, that ok accepts, and returns it, a
// row per line; what names it in a failure.
func (hs *Host) WaitFor(what string, ok func(screen string) bool) string {
	hs.t.Helper()
	if ok(hs.last) {
		return hs.last
	}
	// NOTE: a test's bound: a frame that never comes fails the test instead of hanging it; each
	// frame itself is waited on as it arrives, never polled.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case n := <-hs.got:
			if n.method != plugin.MethodFrame {
				continue
			}
			rows, _, err := plugin.ReadFrame(n.params)
			if err != nil {
				hs.t.Fatalf("host.frame: %v", err)
			}
			var b strings.Builder
			for _, r := range rows {
				for _, run := range r {
					b.WriteString(run.Text)
				}
				b.WriteByte('\n')
			}
			hs.last = b.String()
			if ok(hs.last) {
				return hs.last
			}
		case <-deadline:
			hs.t.Fatalf("no frame showed %s; the last:\n%s", what, hs.last)
		}
	}
}
