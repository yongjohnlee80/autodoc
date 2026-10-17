package decl

import (
	"context"
	"fmt"
	"sync"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui"
)

// host is what the backend sends to: *plugin.Peer, or a test's fake.
type host interface {
	Frame(*plugin.Frame) error
	Close() error
}

// eventQueue is how many events wait for the App's intake before post blocks.
const eventQueue = 1024

// frameBackend is a tui.Backend with no terminal (ADR 1792254331 §4.8): its screen is a grid, and
// each FlushSized sends it to the host, whole, as one host.frame.
type frameBackend struct {
	host   host
	ctx    context.Context // the Card's: done when Close cancels it or Run returns
	events chan tui.Event  // fed by post; closed once, by Stop, when no sender remains

	sendMu   sync.Mutex     // admission only, never held across a send
	stopped  bool           // under sendMu: no new sender is admitted
	senders  sync.WaitGroup // admitted senders still in post
	stopCh   chan struct{}  // closed by Stop: releases a send blocked on a full channel
	stopOnce sync.Once

	mu     sync.Mutex        // want and colors: written by the Handler goroutine
	want   tui.Size          // the size the host last gave (plugin.open, plugin.resize)
	colors map[string]string // the theme's, for resolving a default colour a swap needs

	// loop-owned: only FlushSized and the cursor methods (the App loop goroutine) touch these
	w, h   int
	grid   []tui.Cell
	cursor struct {
		x, y  int
		shown bool
	}
}

var (
	_ tui.Backend      = (*frameBackend)(nil)
	_ tui.SizedFlusher = (*frameBackend)(nil)
)

// newFrameBackend is a backend w×h sending to h, its senders released when ctx is done.
func newFrameBackend(to host, w, hh int, ctx context.Context, colors map[string]string) *frameBackend {
	return &frameBackend{
		host:   to,
		ctx:    ctx,
		events: make(chan tui.Event, eventQueue),
		stopCh: make(chan struct{}),
		want:   tui.Size{W: max(w, 0), H: max(hh, 0)},
		colors: colors,
	}
}

func (b *frameBackend) Start(context.Context) error { return nil }

// Stop keeps golib's Backend contract (tui/backend.go): idempotent, and Events is closed when it
// returns, for every caller. App.Run calls it on every exit path; Close after a failed Open calls it
// too.
func (b *frameBackend) Stop() error {
	b.stopOnce.Do(func() { // a concurrent second Stop waits here until the first has closed Events
		b.sendMu.Lock()
		b.stopped = true // no Add after this: the Wait below cannot race a new sender
		close(b.stopCh)
		b.sendMu.Unlock()
		b.senders.Wait() // every admitted sender has returned
		close(b.events)
	})
	return nil
}

// post hands e to the App's intake. A post after Stop is dropped; one blocked on a full channel is
// released by Stop or by the Card's context (Close, or Run returned).
func (b *frameBackend) post(e tui.Event) {
	b.sendMu.Lock()
	if b.stopped {
		b.sendMu.Unlock()
		return
	}
	b.senders.Add(1)
	b.sendMu.Unlock()
	defer b.senders.Done()
	select {
	case b.events <- e:
	case <-b.stopCh:
	case <-b.ctx.Done():
	}
}

func (b *frameBackend) Events() <-chan tui.Event { return b.events }
func (b *frameBackend) Err() error               { return nil }

// Size is the size the host last gave. The grid follows it only in FlushSized, with the App.
func (b *frameBackend) Size() (tui.Size, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.want, nil
}

func (b *frameBackend) setWant(w, h int) {
	b.mu.Lock()
	b.want = tui.Size{W: max(w, 0), H: max(h, 0)}
	b.mu.Unlock()
}

func (b *frameBackend) setColors(colors map[string]string) {
	b.mu.Lock()
	b.colors = colors
	b.mu.Unlock()
}

// Capabilities: the host draws #rrggbb and takes the pointer; widths are golib's own.
func (b *frameBackend) Capabilities() tui.Capabilities {
	return tui.Capabilities{ColorProfile: tui.ProfileTrueColor, Mouse: tui.TriYes, UnicodeCore: true, DarkBackground: true}
}

// The cursor is latched, as on a terminal: the next frame draws it.
func (b *frameBackend) ShowCursor()                    { b.cursor.shown = true }
func (b *frameBackend) HideCursor()                    { b.cursor.shown = false }
func (b *frameBackend) SetCursor(x, y int)             { b.cursor.x, b.cursor.y = x, y }
func (b *frameBackend) SetCursorShape(tui.CursorShape) {}
func (b *frameBackend) Flush(diff []tui.CellUpdate) error {
	return b.FlushSized(tui.Size{W: b.w, H: b.h}, diff)
}

// FlushSized runs on the App loop, the only goroutine that touches the grid. size is the App's
// buffer at this frame: after a size change the App's buffer invalidated every cell
// (buffer.resize), so this diff covers the whole new grid. A frame drawn at the old size, before
// the App took a resize, is sent at the old size; the host clips it to its view.
func (b *frameBackend) FlushSized(size tui.Size, diff []tui.CellUpdate) error {
	if size != (tui.Size{W: b.w, H: b.h}) {
		b.w, b.h = size.W, size.H
		b.grid = make([]tui.Cell, b.w*b.h)
		for i := range b.grid {
			b.grid[i] = tui.Cell{Content: " ", Width: 1}
		}
	}
	for _, u := range diff { // the diff is the App's reused scratch: copied, never kept
		if u.X < 0 || u.Y < 0 || u.X >= b.w || u.Y >= b.h {
			continue
		}
		i := u.Y*b.w + u.X
		b.grid[i] = u.Cell
		if u.Cell.Width == 2 && u.X+1 < b.w { // the diff never carries a head's continuation: it covers both columns
			b.grid[i+1] = tui.Cell{Width: 0, Attrs: u.Cell.Attrs}
		}
	}
	return b.host.Frame(b.frame())
}

// frame converts the grid to a new plugin.Frame; the grid itself is never changed. A continuation
// is skipped: Frame.Text gives its head two cells. A default colour is resolved first, to the host
// panel's own (app.text on app.base, what AutoDoc's palette gives TokenForeground and TokenPanel),
// wherever a swap needs it. AttrReverse swaps fg and bg, and the cursor cell, when shown, swaps
// them again on top; a cursor on a continuation marks its head.
func (b *frameBackend) frame() *plugin.Frame {
	b.mu.Lock()
	colors := b.colors
	b.mu.Unlock()
	f := plugin.NewFrame(b.w, b.h)
	cx, cy := -1, -1
	if b.cursor.shown && b.cursor.x >= 0 && b.cursor.y >= 0 && b.cursor.x < b.w && b.cursor.y < b.h {
		cx, cy = b.cursor.x, b.cursor.y
		if b.grid[cy*b.w+cx].Continuation() && cx > 0 {
			cx--
		}
	}
	for y := range b.h {
		for x := range b.w {
			c := b.grid[y*b.w+x]
			if c.Continuation() {
				continue
			}
			text := c.Content
			if text == "" {
				text = " "
			}
			f.Text(x, y, text, cellStyle(c.Attrs, x == cx && y == cy, colors))
		}
	}
	return f
}

// cellStyle is a cell's attributes in the frame's vocabulary, inverted for the cursor.
func cellStyle(a tui.CellAttrs, cursor bool, colors map[string]string) plugin.Style {
	fg, bg := colour(a.FG), colour(a.BG)
	if a.Mask&tui.AttrReverse != 0 != cursor { // reverse and the cursor cancel out
		if fg == "" {
			fg = colors["app.text"]
		}
		if bg == "" {
			bg = colors["app.base"]
		}
		fg, bg = bg, fg
	}
	return plugin.Style{FG: fg, BG: bg,
		Bold: a.Mask&tui.AttrBold != 0, Italic: a.Mask&tui.AttrItalic != 0, Underline: a.Mask&tui.AttrUnderline != 0}
}

// ansiNames are the 16 ANSI colours by index, as style.ParseColor reads them.
var ansiNames = [16]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"brightblack", "brightred", "brightgreen", "brightyellow", "brightblue", "brightmagenta", "brightcyan", "brightwhite"}

// colour is a CellColor in the frame's vocabulary, which style.ParseColor reads back: "" for the
// default, a name for ANSI-16, "#rrggbb" for truecolor and the rest of ANSI-256 (xterm's 6×6×6 cube
// and grey ramp).
func colour(c tui.CellColor) string {
	switch c.Kind {
	case tui.CellColorANSI:
		return ansiNames[c.Index&15]
	case tui.CellColorANSI256:
		if c.Index < 16 {
			return ansiNames[c.Index]
		}
		r, g, b := xterm256(c.Index)
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	case tui.CellColorRGB:
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return ""
}

// xterm256 is the RGB of ANSI-256 index n, 16 to 255: the 6×6×6 cube, then the 24 greys.
func xterm256(n uint8) (r, g, b uint8) {
	if n >= 232 {
		v := 8 + 10*(n-232)
		return v, v, v
	}
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	i := n - 16
	return levels[i/36], levels[(i/6)%6], levels[i%6]
}

// failure is one frame naming err, at the size the host last gave, each line clipped at its edge.
func (b *frameBackend) failure(err error) {
	want, _ := b.Size()
	f := plugin.NewFrame(want.W, want.H)
	for y, line := range []string{"plugin/decl: the card could not start", err.Error()} {
		f.Text(1, y, line, plugin.Style{FG: "red"})
	}
	_ = b.host.Frame(f)
}
