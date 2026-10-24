package plugin

import (
	"context"
	"image"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// A PIXEL SURFACE, THE PLUGIN'S SIDE (protocol 10, ADR 1792797354; Linux first). The host sends each
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

	seq  atomic.Uint64
	done chan SurfaceFrameDone
	gone chan struct{}
	once sync.Once
}

// SurfaceBuffer is one buffer to draw into: Pix is W×H pixels of Format, Pitch bytes a row.
type SurfaceBuffer struct {
	ID, Gen     int
	W, H, Pitch int
	Format      string
	Pix         []byte
	drawing     bool // handed out by Buffer and not yet presented: its mapping stays until Present
}

// RGBA is the buffer as an image (RGBA8 buffers only).
func (b *SurfaceBuffer) RGBA() *image.RGBA {
	return &image.RGBA{Pix: b.Pix, Stride: b.Pitch, Rect: image.Rect(0, 0, b.W, b.H)}
}

func newSurface(conn *SurfaceConn) *Surface {
	s := &Surface{conn: conn, gens: map[int][]*SurfaceBuffer{}, pending: map[int][]*SurfaceBuffer{},
		free: map[int]*SurfaceBuffer{}, wake: make(chan struct{}, 1), done: make(chan SurfaceFrameDone, 64),
		gone: make(chan struct{})}
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
func (s *Surface) Done() <-chan SurfaceFrameDone { return s.done }

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
	if gen <= s.cur {
		return
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
		go s.conn.Send(map[string]any{"op": OpSurfaceAck, "gen": gen})
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
	seq := s.seq.Add(1)
	err := s.conn.Send(FrameMsg(SurfaceFrame{Seq: seq, Gen: b.Gen, Buffer: b.ID, Dirty: dirty, InputT: inputT}))
	s.mu.Lock()
	b.drawing = false
	if b.Gen < s.cur && b.Pix != nil { // its generation ended while it was drawn: the host answers "retired"
		unix.Munmap(b.Pix)
		b.Pix = nil
	}
	s.mu.Unlock()
	return seq, err
}

// read takes the host's side-channel ops until it goes.
func (s *Surface) read() {
	defer s.once.Do(func() { close(s.gone) })
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
			if s.info.Gen == gen {
				s.install(gen, bufs)
			} else {
				s.pending[gen] = bufs
			}
			s.mu.Unlock()
			s.poke()
		case OpFrameDone:
			g.CloseAll()
			if d, err := ReadFrameDoneMsg(m); err == nil {
				select {
				case s.done <- d:
				default:
					<-s.done // the oldest goes
					s.done <- d
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
