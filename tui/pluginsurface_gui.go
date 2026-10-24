//go:build gui

package tui

import (
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	"github.com/yongjohnlee80/golib/gui"
)

// A PIXEL SURFACE IN THE WINDOW: the sink is the window's gui.Stream, and the card's view is a native
// view over its own cells that draws it (DrawStream), tells the owner the geometry it was painted at,
// and reports the plugin's caret to the window's IME (gui.CaretView).

type streamSink struct{ st *gui.Stream }

func (s streamSink) Push(f *extimage.Frame) surfaceReceipt { return streamReceipt{s.st.Push(f)} }
func (s streamSink) Close()                                { s.st.Close() }

type streamReceipt struct{ r *gui.Receipt }

func (r streamReceipt) Done() <-chan struct{}       { return r.r.Done() }
func (r streamReceipt) Result() (string, time.Time) { return outcomeName(r.r.Outcome()), r.r.When() }

func outcomeName(o gui.Outcome) string {
	switch o {
	case gui.Drawn:
		return "drawn"
	case gui.Superseded:
		return "superseded"
	case gui.NotShown:
		return "not_shown"
	case gui.Unsupported:
		return "unsupported"
	}
	return "closed"
}

// newSurfaceSink is a stream of the window, when there is one: the sink, and the stream its view
// draws.
func (pl *plugins) newSurfaceSink() (surfaceSink, any, bool) {
	b, ok := pl.c.SurfaceBackend().(*gui.Backend)
	if !ok {
		return nil, nil, false
	}
	st := b.NewStream()
	return streamSink{st}, st, true
}

// surfaceKinds are the frame kinds the window's caps draw, as plugin.surface names them.
func surfaceKinds(c extimage.Caps) []plugin.SurfaceKind {
	var out []plugin.SurfaceKind
	for _, t := range c.Tuples {
		k := plugin.SurfaceKind{Modifier: t.Modifier, MaxW: t.MaxSize.X, MaxH: t.MaxSize.Y}
		switch t.Kind {
		case extimage.KindMemory:
			k.Kind = "memory"
		case extimage.KindDMABuf:
			k.Kind = "dmabuf"
		default:
			continue
		}
		switch t.Format {
		case extimage.FormatRGBA8:
			k.Format = "rgba8"
		case extimage.FormatBGRA8:
			k.Format = "bgra8"
		default:
			continue
		}
		out = append(out, k)
	}
	return out
}

// surfaceView is a surface plugin's native view.
type surfaceView struct {
	v  *pluginView
	st *gui.Stream
	b  *gui.Backend
}

// Paint draws the stream over the view's whole canvas, and tells the owner the geometry it was
// painted at: a new size, scale or caps is a new generation.
func (s surfaceView) Paint(c gui.Canvas) {
	sz := c.Size()
	if r := s.v.run; r != nil && r.surface != nil {
		if caps, ok := s.b.FrameCaps(); ok {
			g := surfaceGeometry{W: sz.W, H: sz.H, Scale: c.Scale(), Kinds: surfaceKinds(caps)}
			if !g.same(s.v.lastSurface) {
				s.v.lastSurface = g
				r.surface.Resize(g)
			}
		}
	}
	gui.DrawStream(c, s.st, gui.Rect{W: sz.W, H: sz.H}) // false: no frame yet, the card's own background shows
}

// Caret is the plugin's last host.surface.caret, for the window's IME: only while a text-input
// surface has the keys.
func (s surfaceView) Caret() (gui.Rect, float32, bool) {
	r := s.v.run
	if r == nil || r.e.m.Surface == nil || !r.e.m.Surface.TextInput || !(r.e.m.Modal() || r.focused) {
		return gui.Rect{}, 0, false
	}
	r.mu.Lock()
	c, ok := r.caret, r.caretOK
	r.mu.Unlock()
	return gui.Rect{X: c.X, Y: c.Y, W: c.W, H: c.H}, c.Baseline, ok
}

// surfaceNative is the view's native view: its surface's, when it has one.
func (v *pluginView) surfaceNative() (any, bool) {
	r := v.run
	if r == nil || r.surface == nil {
		return nil, false
	}
	st, _ := r.surfaceStream.(*gui.Stream)
	b, _ := v.pl.c.SurfaceBackend().(*gui.Backend)
	if st == nil || b == nil {
		return nil, false
	}
	return surfaceView{v: v, st: st, b: b}, true
}
