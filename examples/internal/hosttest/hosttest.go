// Package hosttest plays AutoDoc's side of the plugin protocol for the examples' tests: it runs a
// Handler under plugin.ServeConn over a pair of pipes, as AutoDoc runs a plugin's process, sends
// the host's notifications and reads the frames the plugin sends back.
package hosttest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// Host is a test's end of the link.
type Host struct {
	t    *testing.T
	link *plugin.Link
	got  chan note
	last string // the last frame read, a row per line
}

type note struct {
	method string
	params []any
}

// Start serves h and opens it at w×h in theme, at the newest protocol, answering the handshake.
func Start(t *testing.T, h plugin.Handler, w, hh int, theme plugin.Theme) *Host {
	t.Helper()
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
	hs := &Host{t: t, got: make(chan note, 256)}
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
	hs.send(plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: plugin.Protocol, Width: w, Height: hh, Theme: theme}))
	return hs
}

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

// Press is a left press at (x, y) in the frame (protocol 6).
func (hs *Host) Press(x, y int) {
	hs.t.Helper()
	hs.send(plugin.MethodPointer, plugin.PointerParams(plugin.Pointer{Kind: "press", Button: "left", X: x, Y: y, Count: 1}))
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
	if strings.Contains(hs.last, want) {
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
			if strings.Contains(hs.last, want) {
				return hs.last
			}
		case <-deadline:
			hs.t.Fatalf("no frame showed %q; the last:\n%s", want, hs.last)
		}
	}
}
