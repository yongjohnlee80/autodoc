// surface-go is the pixel-surface spike's reference plugin (ADR 1792797354): it streams an animated,
// CPU-rendered test pattern into its card and draws every input event it receives over it, IME
// commits included. Its log (the plugin's stderr) says, each second, the frames drawn and the
// latency from the host dispatching a pointer event to the frame showing it being drawn.
package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type spike struct {
	plugin.Base
	peer   *plugin.Peer
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	events   []string // the latest, newest last
	inputT   int64    // the T of the latest pointer event
	info     plugin.SurfaceInfo
	started  bool
	stats    string
	cellsW   int
	cellsH   int
	inFlight map[uint64]int64 // a frame's seq → the input T it shows
}

func (s *spike) note(format string, a ...any) {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf(format, a...))
	if len(s.events) > 14 {
		s.events = s.events[len(s.events)-14:]
	}
	s.mu.Unlock()
}

func (s *spike) Open(p *plugin.Peer, o plugin.Open) {
	s.peer, s.cellsW, s.cellsH = p, o.Width, o.Height
	s.inFlight = map[uint64]int64{}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.drawCells() // the terminal's view, and the GUI's before the first frame
}

// drawCells is the card where the host draws no pixels.
func (s *spike) drawCells() {
	f := plugin.NewFrame(s.cellsW, s.cellsH)
	f.Text(1, 1, "This card draws pixels:", plugin.Style{})
	f.Text(1, 2, "open it in AutoDoc's GUI (--gui).", plugin.Style{})
	s.peer.Frame(f)
}

func (s *spike) Resize(w, h int) { s.cellsW, s.cellsH = w, h; s.drawCells() }

func (s *spike) Key(k plugin.Key) {
	switch {
	case len([]rune(k.Text)) > 1:
		s.note("IME commit %q", k.Text)
	case k.Text != "":
		s.note("key %q", k.Text)
	default:
		s.note("key %s ctrl=%v alt=%v shift=%v", k.Key, k.Ctrl, k.Alt, k.Shift)
	}
}

func (s *spike) Paste(text string) { s.note("paste %q", text) }

func (s *spike) SurfacePointer(p plugin.SurfacePointer) {
	s.mu.Lock()
	s.inputT = p.T
	s.mu.Unlock()
	if p.Kind == "scroll" {
		s.note("scroll dx=%.1f dy=%.1f", p.DX, p.DY)
		return
	}
	if p.Kind == "move" {
		return // shown as the crosshair, not as a line
	}
	s.note("%s %s at %.1f,%.1f count=%d", p.Kind, p.Button, p.X, p.Y, p.Count)
}

func (s *spike) Surface(sf *plugin.Surface, info plugin.SurfaceInfo) {
	s.mu.Lock()
	s.info = info
	start := !s.started
	s.started = true
	s.mu.Unlock()
	s.note("surface gen %d: %.0f×%.0f dp at scale %.2f", info.Gen, info.W, info.H, info.Scale)
	// the caret: a text line at the bottom, where the IME's candidate window opens
	s.peer.SetCaret(plugin.Caret{X: 12, Y: info.H - 28, W: 2, H: 16, Baseline: 12})
	if start {
		go s.render(sf)
		go s.measure(sf)
	}
}

func (s *spike) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// render draws a frame into each free buffer, as fast as the host credits them.
func (s *spike) render(sf *plugin.Surface) {
	t0 := time.Now()
	for {
		b, err := sf.Buffer(s.ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		events := slices.Clone(s.events)
		inputT, stats := s.inputT, s.stats
		s.mu.Unlock()
		paint(b, time.Since(t0), events, stats)
		seq, err := sf.Present(b, image.Rectangle{}, inputT)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.inFlight[seq] = inputT
		s.mu.Unlock()
	}
}

// measure reads each frame's outcome: the frames drawn per second, and for a frame showing a
// pointer event, the time from its dispatch to the frame being drawn.
func (s *spike) measure(sf *plugin.Surface) {
	// NOTE: the measurement's window: the counts are reported, and reset, once a second.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var drawn, superseded int
	var lat []time.Duration
	lastInput := int64(0)
	for {
		select {
		case <-s.ctx.Done():
			return
		case d := <-sf.Done():
			s.mu.Lock()
			in := s.inFlight[d.Seq]
			delete(s.inFlight, d.Seq)
			s.mu.Unlock()
			switch d.Outcome {
			case "drawn":
				drawn++
				if in != 0 && in != lastInput && d.WhenNS > in { // the first frame drawn showing that input
					lastInput = in
					lat = append(lat, time.Duration(d.WhenNS-in))
				}
			case "superseded":
				superseded++
			}
		case <-tick.C:
			line := fmt.Sprintf("fps %d, superseded %d", drawn, superseded)
			if len(lat) > 0 {
				slices.Sort(lat)
				var sum time.Duration
				for _, l := range lat {
					sum += l
				}
				line += fmt.Sprintf(", pointer→frame avg %.1f ms p95 %.1f ms (n=%d)",
					float64(sum)/float64(len(lat))/1e6, float64(lat[len(lat)*95/100])/1e6, len(lat))
			}
			fmt.Fprintln(os.Stderr, line) // the plugin's log: the spike's measurement
			s.mu.Lock()
			s.stats = line
			s.mu.Unlock()
			drawn, superseded, lat = 0, 0, lat[:0]
		}
	}
}

// paint is the test pattern: moving colour bars, a bouncing square, and the event overlay.
func paint(b *plugin.SurfaceBuffer, t time.Duration, events []string, stats string) {
	img := b.RGBA()
	bgra := b.Format == "bgra8"
	phase := int(t.Milliseconds() / 8)
	for y := 0; y < b.H; y++ {
		row := img.Pix[y*b.Pitch : y*b.Pitch+b.W*4]
		for x := 0; x < b.W; x++ {
			v := uint8((x + phase) * 255 / max(b.W, 1))
			r, g, bl := v, uint8(y*255/max(b.H, 1)), uint8(255-v)
			if bgra {
				r, bl = bl, r
			}
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = r, g, bl, 255
		}
	}
	side := max(b.H/6, 8)
	period := max(b.W-side, 1)
	x := phase % (2 * period)
	if x > period {
		x = 2*period - x
	}
	draw.Draw(img, image.Rect(x, b.H/2-side/2, x+side, b.H/2+side/2), image.White, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, b.W, 20+16*len(events)), image.NewUniform(color.RGBA{0, 0, 0, 170}), image.Point{}, draw.Over)
	d := &font.Drawer{Dst: img, Src: image.White, Face: basicfont.Face7x13}
	d.Dot = fixed.P(8, 16)
	d.DrawString(stats)
	for i, e := range events {
		d.Dot = fixed.P(8, 32+16*i)
		d.DrawString(e)
	}
}

func main() {
	if err := plugin.Serve(context.Background(), &spike{}); err != nil {
		fmt.Fprintln(os.Stderr, "surface-go:", err)
		os.Exit(1)
	}
}
