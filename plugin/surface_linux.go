package plugin

import (
	"context"
	"image"
	"sync"

	"golang.org/x/sys/unix"
)

// A PIXEL SURFACE, THE PLUGIN'S SIDE (protocol 10,
// docs/decisions/plugins.md#pixel-surfaces; Linux first). The host sends each
// generation's buffers over the side channel, shared memory the plugin maps read-write; the plugin
// draws into a free one and presents it, and is told each frame's outcome, then the buffer's credit.

// Surface is a card's or dialog's pixel surface. Its methods are safe for concurrent use: a plugin
// renders on a goroutine of its own.
type Surface struct {
	conn *SurfaceConn

	mu      sync.Mutex
	info    SurfaceInfo              // the latest plugin.surface
	gens    map[int][]*SurfaceBuffer // a generation's buffers, once both its halves came
	pending map[int][]*SurfaceBuffer // buffers whose plugin.surface is not here yet
	cur     int                      // the generation drawn for: both its halves here
	free    map[int]*SurfaceBuffer
	wake    chan struct{}
	acked   int
	acks    sync.WaitGroup // the acks in flight: Close waits for them before the fd goes

	sendMu   sync.Mutex       // a frame's seq and its send are one step: seqs reach the host in order
	seq      uint64           // under sendMu
	inputTs  map[uint64]int64 // a presented frame's InputT, by seq, until its frame.done (under mu)
	done     chan SurfaceFrameDone
	gone     chan struct{} // closed when the side channel has ended (its reader is done)
	once     sync.Once
	closed   bool // under mu: no buffer is granted, and each mapping goes once nothing draws in it
	closeOne sync.Once
}

// SurfaceBuffer is one buffer to draw into: Pix is W×H pixels of Format, Pitch bytes a row.
type SurfaceBuffer struct {
	ID, Gen     int
	W, H, Pitch int
	Format      string
	Pix         []byte
	drawing     bool // handed out by Buffer and not yet presented: its mapping stays until Present (under mu)
}

// RGBA is the buffer as an image (RGBA8 buffers only).
func (b *SurfaceBuffer) RGBA() *image.RGBA {
	return &image.RGBA{Pix: b.Pix, Stride: b.Pitch, Rect: image.Rect(0, 0, b.W, b.H)}
}

func newSurface(conn *SurfaceConn) *Surface {
	s := &Surface{conn: conn, gens: map[int][]*SurfaceBuffer{}, pending: map[int][]*SurfaceBuffer{},
		free: map[int]*SurfaceBuffer{}, wake: make(chan struct{}, 1), done: make(chan SurfaceFrameDone, 64),
		inputTs: map[uint64]int64{},
		gone:    make(chan struct{})}
	go s.read()
	return s
}

// Info is the surface's latest generation.
func (s *Surface) Info() SurfaceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Done is each frame's outcome, as the host answers it. A plugin that doesn't read it loses the
// oldest beyond 64; the credits come through Buffer regardless.
//
// Done is a bounded, lossy view of the outcomes, not the wire's exactly-once delivery: when the
// handler doesn't keep up, the oldest are dropped. Credits are never dropped: they come through
// Buffer.
func (s *Surface) Done() <-chan SurfaceFrameDone { return s.done }

// Close ends the surface, once: no buffer is granted after it, the side channel shuts down, and
// every mapping is reclaimed, a buffer being drawn into at its Present. Serve calls it as it ends.
func (s *Surface) Close() {
	s.closeOne.Do(func() {
		s.conn.Shutdown() // wakes the reader, and ends an ack's send; the fd stays until both are done
		<-s.gone
		s.acks.Wait() // none starts after gone: install grants nothing once closed
		s.conn.Close()
	})
}

// reclaim is the surface's end (under mu): nothing more is granted, and every mapping not being
// drawn into is unmapped; a drawn one goes at its Present.
func (s *Surface) reclaim() {
	s.closed = true
	unmap := func(b *SurfaceBuffer) {
		if !b.drawing && b.Pix != nil {
			unix.Munmap(b.Pix)
			b.Pix = nil
		}
	}
	for _, bufs := range s.gens {
		for _, b := range bufs {
			unmap(b)
		}
	}
	for _, bufs := range s.pending {
		for _, b := range bufs {
			unmap(b)
		}
	}
	clear(s.gens)
	clear(s.pending)
	clear(s.free)
}

// setInfo is plugin.surface: the generation's geometry, matched with its buffers by gen.
func (s *Surface) setInfo(i SurfaceInfo) {
	s.mu.Lock()
	s.info = i
	if bufs, ok := s.pending[i.Gen]; ok {
		delete(s.pending, i.Gen)
		s.install(i.Gen, bufs)
	}
	s.mu.Unlock()
	s.poke()
}

// install makes gen current (under mu): its buffers are free, the older generations' unmapped once
// none is drawn into; and acknowledges it to the host.
func (s *Surface) install(gen int, bufs []*SurfaceBuffer) {
	if gen <= s.cur || s.closed {
		return
	}
	for g, bufs := range s.pending { // halves of generations that never completed, now superseded
		if g < gen {
			for _, b := range bufs {
				unix.Munmap(b.Pix)
			}
			delete(s.pending, g)
		}
	}
	for g, old := range s.gens {
		if g < gen {
			// the plugin's own mappings: the host keeps its own, so an old buffer is unmapped here
			// at once, idle or presented; one being drawn into, after its Present
			for _, b := range old {
				delete(s.free, b.ID)
				if !b.drawing {
					unix.Munmap(b.Pix)
					b.Pix = nil
				}
			}
			delete(s.gens, g)
		}
	}
	s.cur = gen
	s.gens[gen] = bufs
	for _, b := range bufs {
		s.free[b.ID] = b
	}
	if s.acked < gen {
		s.acked = gen
		s.acks.Add(1)
		go func() { defer s.acks.Done(); s.conn.Send(map[string]any{"op": OpSurfaceAck, "gen": gen}) }()
	}
}

func (s *Surface) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Buffer is a free buffer of the current generation, waiting for one; an error once ctx ends or
// the surface is gone.
func (s *Surface) Buffer(ctx context.Context) (*SurfaceBuffer, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, ErrSurfaceClosed
		}
		for id, b := range s.free {
			if b.Gen == s.cur {
				delete(s.free, id)
				b.drawing = true
				s.mu.Unlock()
				return b, nil
			}
		}
		s.mu.Unlock()
		select {
		case <-s.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.gone:
			return nil, ErrSurfaceClosed
		}
	}
}

// Present shows b. Its pixels must be complete: there is no fence. dirty, when not empty, is what
// changed since b's last frame; inputT is the T of the latest input it shows (for latency).
func (s *Surface) Present(b *SurfaceBuffer, dirty image.Rectangle, inputT int64) (uint64, error) {
	// one step: a seq taken by one goroutine is sent before another's, so the host sees them rise
	s.sendMu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Lock()
	s.inputTs[seq] = inputT // before the send: its frame.done may come at once
	// b is given up before the send: once sent, its credit may hand it to another caller at once,
	// so nothing after the send touches it
	b.drawing = false
	if (b.Gen < s.cur || s.closed) && b.Pix != nil { // its generation, or the surface, ended while it was drawn
		unix.Munmap(b.Pix)
		b.Pix = nil
	}
	s.mu.Unlock()
	err := s.conn.Send(FrameMsg(SurfaceFrame{Seq: seq, Gen: b.Gen, Buffer: b.ID, Dirty: dirty, InputT: inputT}))
	s.sendMu.Unlock()
	if presentSent != nil {
		presentSent()
	}
	if err != nil {
		s.mu.Lock()
		delete(s.inputTs, seq)
		s.mu.Unlock()
	}
	return seq, err
}

// doneFull is a test's consumer between the reader's two checks of a full Done queue: nil but in tests.
var doneFull func(done chan SurfaceFrameDone)

// presentSent is a test's pause between a frame's send and its Present's return: nil but in tests.
var presentSent func()

// read takes the host's side-channel ops until it goes.
func (s *Surface) read() {
	defer func() {
		s.mu.Lock()
		s.reclaim() // the peer went, or Close shut it: nothing more is granted
		s.mu.Unlock()
		s.once.Do(func() { close(s.gone) })
		s.poke()
	}()
	for {
		m, g, err := s.conn.Recv()
		if err != nil {
			return
		}
		switch WireOp(m) {
		case OpBuffers:
			gen, specs, err := ReadBuffersMsg(m, g.Len())
			if err != nil {
				g.CloseAll()
				continue
			}
			var bufs []*SurfaceBuffer
			for i, sp := range specs {
				fd := g.Take(i)
				pix, err := unix.Mmap(fd, 0, sp.Size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
				unix.Close(fd)
				if err != nil {
					continue
				}
				bufs = append(bufs, &SurfaceBuffer{ID: sp.ID, Gen: gen, W: sp.W, H: sp.H, Pitch: sp.Pitch, Format: sp.Format, Pix: pix})
			}
			g.CloseAll()
			s.mu.Lock()
			switch {
			case gen <= s.cur || s.closed: // a superseded generation's half: nothing will draw in it
				for _, b := range bufs {
					unix.Munmap(b.Pix)
				}
			case s.info.Gen == gen:
				s.install(gen, bufs)
			default:
				s.pending[gen] = bufs
			}
			s.mu.Unlock()
			s.poke()
		case OpFrameDone:
			g.CloseAll()
			if d, err := ReadFrameDoneMsg(m); err == nil {
				s.mu.Lock()
				d.InputT = s.inputTs[d.Seq]
				delete(s.inputTs, d.Seq)
				s.mu.Unlock()
				// never blocking, whatever the consumer does between the two: drop the oldest, retry
				for sent := false; !sent; {
					select {
					case s.done <- d:
						sent = true
					default:
						if doneFull != nil {
							doneFull(s.done)
						}
						select {
						case <-s.done:
						default:
						}
					}
				}
			}
		case OpBufferFree:
			g.CloseAll()
			id, err := ReadBufferFreeMsg(m)
			if err != nil {
				continue
			}
			s.mu.Lock()
			for gen, bufs := range s.gens {
				for _, b := range bufs {
					if b.ID == id && gen == s.cur {
						s.free[id] = b
					}
				}
			}
			s.mu.Unlock()
			s.poke()
		default:
			g.CloseAll()
		}
	}
}

// openSurface is the side channel from the environment, close-on-exec at once; nil without one.
func openSurface() *Surface {
	conn, ok, err := OpenPluginSurfaceConn()
	if err != nil || !ok {
		return nil
	}
	return newSurface(conn)
}
