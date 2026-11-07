package tui

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// A PIXEL SURFACE IN A TERMINAL (docs/decisions/plugins.md#pixel-surfaces-in-a-terminal): where the
// terminal confirmed kitty's graphics protocol, View › Image previews is on, and Preferences ›
// Plugin pixels in a terminal is on (the default), a surface plugin gets its surface as in a window.
// The plugin and AutoDoc share a machine, so its frames still come over the shared-memory side
// channel; what crosses to the terminal, over ssh too, is the picture: each frame encoded as a PNG
// (Go's encoder at BestSpeed) off the loop and shown over the card's cells, as a diagram preview is
// (tuicore.ImageReporter, one image id a card, replaced in place, deleted when the card closes or
// hides). The newest frame wins: one is encoded at a time, and a frame that a newer one overtakes
// while it waits is answered "superseded". At most cellSurfaceFPS frames a second are shown, and
// fewer for large ones: after a picture of n bytes the next waits n / cellSurfaceBudget seconds, so
// a terminal over ssh is never sent more than about cellSurfaceBudget a second. Elsewhere a surface
// plugin draws cells, and so does a card whose terminal stops showing pictures while it runs (the
// preference or image previews turned off): its picture goes, its cells show, and its frames are
// answered "not_shown" until pictures are on again.

const (
	cellSurfaceFPS    = 15      // the most frames a second shown in a terminal
	cellSurfaceBudget = 4 << 20 // about the most bytes a second sent to the terminal
	// cellSurfaceShown is how long a picture handed to its card waits for the card's paint: one
	// that does not come (the card hidden) is answered "not_shown"
	cellSurfaceShown = 250 * time.Millisecond
)

// cellSurfaceKinds are the frames a terminal's surface takes: memory, RGBA or BGRA, as protocol 10
// bounds them.
var cellSurfaceKinds = []plugin.SurfaceKind{
	{Kind: "memory", Format: "rgba8", MaxW: surfaceMaxPixels, MaxH: surfaceMaxPixels},
	{Kind: "memory", Format: "bgra8", MaxW: surfaceMaxPixels, MaxH: surfaceMaxPixels},
}

// cellPictures is a card's picture, between its sink and its view: the newest PNG, its version and
// its image id, the version the view last painted, and a signal of each paint.
type cellPictures struct {
	id         uint32
	post       func(func()) // the loop's: the card painted again
	dirty      func()       // on the loop: marks the card's view for a paint; nil before it has one
	mu         sync.Mutex
	png        []byte
	version    uint64
	paintedVer uint64        // the version the view's last paint showed
	painted    chan struct{} // one signal a paint, dropped when one is already waiting
}

func newCellPictures(post func(func())) *cellPictures {
	return &cellPictures{id: tuicore.NewImageID(), post: post, painted: make(chan struct{}, 1)}
}

// image is the picture now: ok false before the first, or once the card's surface closed.
func (p *cellPictures) image() (tuicore.Image, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.png == nil {
		return tuicore.Image{}, false
	}
	return tuicore.Image{ID: p.id, PNG: p.png, Version: p.version}, true
}

// publish makes b the card's picture (nil: none), and asks the card for a paint: the picture's
// version.
func (p *cellPictures) publish(b []byte) uint64 {
	p.mu.Lock()
	p.png = b
	p.version++
	ver := p.version
	p.mu.Unlock()
	p.post(func() {
		if p.dirty != nil {
			p.dirty()
		}
	})
	return ver
}

// waitPainted reports whether the card painted version ver, or a later one, within
// cellSurfaceShown: a hidden card isn't painted. A paint of an earlier version, its signal still
// waiting, acknowledges nothing.
func (p *cellPictures) waitPainted(ver uint64) bool {
	deadline := time.NewTimer(cellSurfaceShown)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		done := p.paintedVer >= ver
		p.mu.Unlock()
		if done {
			return true
		}
		select {
		case <-p.painted:
		case <-deadline.C:
			return false
		}
	}
}

// paintedNow is the view's paint of the picture as it is now: what waitPainted waits for.
func (p *cellPictures) paintedNow() {
	p.mu.Lock()
	p.paintedVer = p.version
	p.mu.Unlock()
	select {
	case p.painted <- struct{}{}:
	default:
	}
}

// cellReceipt is a frame's outcome in a terminal.
type cellReceipt struct {
	done    chan struct{}
	outcome string
	when    time.Time
}

func newCellReceipt() *cellReceipt { return &cellReceipt{done: make(chan struct{})} }

func (r *cellReceipt) Done() <-chan struct{}       { return r.done }
func (r *cellReceipt) Result() (string, time.Time) { return r.outcome, r.when }

func (r *cellReceipt) settle(outcome string) {
	r.outcome, r.when = outcome, time.Now()
	close(r.done)
}

// cellFrame is a frame and its receipt, waiting or in flight.
type cellFrame struct {
	f *extimage.Frame
	r *cellReceipt
}

// cellSink is a terminal's surface sink: one frame in flight (waiting for its turn, encoded, shown)
// and the newest behind it.
type cellSink struct {
	pics   *cellPictures
	encode func(*extimage.Frame) ([]byte, error) // encodePNG; a test's own
	mu     sync.Mutex
	busy   bool       // a frame is in flight
	next   *cellFrame // the newest frame behind it
	closed bool
	stop   chan struct{} // closed by Close: a wait for the next turn ends
	turn   time.Time     // the earliest the next picture may be shown
	// publishing runs as a frame's picture is published, under mu, with the close fenced out: a
	// test's seam, nil otherwise
	publishing func()
}

func newCellSink(pics *cellPictures) *cellSink {
	return &cellSink{pics: pics, encode: encodePNG, stop: make(chan struct{})}
}

func (s *cellSink) Push(f *extimage.Frame) surfaceReceipt {
	r := newCellReceipt()
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		f.Drop()
		r.settle("closed")
		return r
	case s.busy:
		old := s.next
		s.next = &cellFrame{f, r}
		s.mu.Unlock()
		if old != nil { // overtaken while it waited
			old.f.Drop()
			old.r.settle("superseded")
		}
		return r
	}
	s.busy = true
	s.mu.Unlock()
	go s.run(cellFrame{f, r})
	return r
}

// run takes frames in flight one after another until none waits: each waits for its turn, is
// encoded (its buffer released once read) and shown.
func (s *cellSink) run(c cellFrame) {
	for {
		s.mu.Lock()
		wait := time.Until(s.turn)
		s.mu.Unlock()
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-s.stop:
			}
		}
		b, err := s.encode(c.f)
		c.f.Drop()
		// the close check and the publication are one step under mu, as Close's own clearing is:
		// a picture published is never one a close has already cleared
		s.mu.Lock()
		closed := s.closed
		var ver uint64
		if !closed && err == nil {
			if s.publishing != nil {
				s.publishing()
			}
			ver = s.pics.publish(b)
		}
		s.mu.Unlock()
		switch {
		case closed:
			c.r.settle("closed")
		case err != nil:
			c.r.settle("unsupported")
		case s.pics.waitPainted(ver):
			s.mu.Lock()
			s.turn = time.Now().Add(max(time.Second/cellSurfaceFPS, time.Duration(len(b))*time.Second/cellSurfaceBudget))
			s.mu.Unlock()
			c.r.settle("drawn")
		default:
			c.r.settle("not_shown")
		}
		s.mu.Lock()
		if s.next == nil || s.closed {
			s.busy = false
			s.mu.Unlock()
			return
		}
		c, s.next = *s.next, nil
		s.mu.Unlock()
	}
}

// Close ends the sink: a frame behind the one in flight is answered "closed", the one in flight is
// when it ends, and the card's picture goes, under mu, so no frame in flight publishes after it.
func (s *cellSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stop)
	next := s.next
	s.next = nil
	s.pics.publish(nil)
	s.mu.Unlock()
	if next != nil {
		next.f.Drop()
		next.r.settle("closed")
	}
}

// errFrameKind is a frame a terminal can't show: not a memory RGBA or BGRA one.
var errFrameKind = errors.New("a terminal shows memory RGBA and BGRA frames alone")

// encodePNG is a memory frame as a PNG, at BestSpeed: about half the bytes of kitty's own zlib'd
// RGBA for a picture, at a frame rate a card's size keeps (the decision's numbers).
func encodePNG(f *extimage.Frame) ([]byte, error) {
	d := f.Desc()
	if d.Kind != extimage.KindMemory || len(d.Planes) != 1 || (d.Format != extimage.FormatRGBA8 && d.Format != extimage.FormatBGRA8) {
		return nil, errFrameKind
	}
	w, h, pl := d.Size.X, d.Size.Y, d.Planes[0]
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		src := pl.Data[int(pl.Offset)+y*int(pl.Pitch):][:4*w]
		dst := m.Pix[y*m.Stride:][:4*w]
		copy(dst, src)
		if d.Format == extimage.FormatBGRA8 {
			for x := 0; x < len(dst); x += 4 {
				dst[x], dst[x+2] = dst[x+2], dst[x]
			}
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, m); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// newCellSurfaceSink is a terminal's sink, where the card's picture can be shown: the sink, and its
// pictures, which the card's view reports.
func (pl *plugins) newCellSurfaceSink() (surfaceSink, any, bool) {
	if !pl.c.TerminalPictures() {
		return nil, nil, false
	}
	pics := newCellPictures(pl.c.Post)
	return newCellSink(pics), pics, true
}

// cellPicturesOf is the view's run's pictures, in a terminal; nil elsewhere.
func (v *pluginView) cellPicturesOf() *cellPictures {
	if v.run == nil || v.run.surface == nil {
		return nil
	}
	p, _ := v.run.surfaceStream.(*cellPictures)
	return p
}

var _ tuicore.ImageReporter = (*pluginView)(nil)

// Image is the card's picture over its cells, in a terminal (tuicore.ImageReporter): the App
// places it, replaces it in place by its id, and deletes it when the card closes or hides, or the
// terminal stops showing pictures.
func (v *pluginView) Image() (tuicore.Image, bool) {
	if v.cellSurfaceLive() {
		return v.cellPicturesOf().image()
	}
	return tuicore.Image{}, false
}

// cellSurfaceLive reports whether the card shows its surface's picture now: it has one, in a
// terminal still showing pictures (the preference and image previews read at each paint). Off,
// the card shows its cells.
func (v *pluginView) cellSurfaceLive() bool {
	return v.cellPicturesOf() != nil && v.pl != nil && v.pl.c.TerminalPictures()
}

// paintCellSurface is Render's in a terminal surface's mode: tells the owner the geometry the
// card's cells make (a new size is a new generation) and the sink that the card painted.
func (v *pluginView) paintCellSurface(sz tuicore.Size) {
	p := v.cellPicturesOf()
	ctx := v.Context()
	if p == nil || ctx == nil {
		return
	}
	if p.dirty == nil {
		p.dirty = ctx.MarkDirty
	}
	cp := surfaceCellPixels(ctx)
	g := surfaceGeometry{W: float32(sz.W) * cp.W, H: float32(sz.H) * cp.H, Scale: 1, Kinds: cellSurfaceKinds}
	if !g.same(v.lastSurface) {
		v.lastSurface = g
		v.run.surface.Resize(g)
	}
	p.paintedNow()
}

// prefPluginPixels is Preferences › Plugin pixels in a terminal: on by default.
const prefPluginPixels = "tui.plugins.terminalPixels"

// terminalPictures reports whether a plugin's pixel surface shows in this terminal: not a window
// (which draws streams), kitty's graphics confirmed, image previews on, and the preference on.
func (h *Host) terminalPictures() bool {
	return h.p != nil && !h.native() && h.graphics() == tuicore.TriYes && h.prefs.images &&
		h.prefs.Value(prefPluginPixels) != "false"
}
