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
	adds    map[int]*surfaceAdd    // the plugin's registrations the host hasn't answered, by id
	added   map[int]*SurfaceBuffer // the current generation's registered buffers, by id
	addID   int                    // the last registration's id + 1: odd ids, rising
	lost    int                    // abandoned registrations the host accepted, this generation
	turn    chan struct{}          // closed, and replaced, when the registered buffers change
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
	Kind        string // "memory", or "dmabuf" for one the plugin registered (AddBuffer)
	Pix         []byte // nil for a registered buffer: its pixels are the plugin's own
	drawing     bool   // handed out by Buffer and not yet presented: its mapping stays until Present (under mu)
	state       int    // a registered buffer's (under mu)
}

// RGBA is the buffer as an image (RGBA8 buffers only).
func (b *SurfaceBuffer) RGBA() *image.RGBA {
	return &image.RGBA{Pix: b.Pix, Stride: b.Pitch, Rect: image.Rect(0, 0, b.W, b.H)}
}

func newSurface(conn *SurfaceConn) *Surface {
	s := &Surface{conn: conn, gens: map[int][]*SurfaceBuffer{}, pending: map[int][]*SurfaceBuffer{},
		free: map[int]*SurfaceBuffer{}, adds: map[int]*surfaceAdd{}, added: map[int]*SurfaceBuffer{}, turn: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan SurfaceFrameDone, 64),
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
	s.dropAdds(ErrSurfaceClosed)
}

// setInfo is plugin.surface: the generation's geometry, matched with its buffers by gen.
func (s *Surface) setInfo(i SurfaceInfo) {
	s.mu.Lock()
	s.info = i
	s.turned()
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
	s.dropAdds(ErrSurfaceRetired) // the plugin's buffers were the old generation's: the host drops its own
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
// the surface is gone. Once a plugin.surface has told the plugin of a generation, Buffer waits for
// that generation's buffers: the side channel brings them apart from it, and an older
// generation's buffer, granted meanwhile, would draw a frame the host answers "retired".
func (s *Surface) Buffer(ctx context.Context) (*SurfaceBuffer, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, ErrSurfaceClosed
		}
		for id, b := range s.free {
			if b.Gen == s.cur && s.cur >= s.info.Gen {
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
	if b.state == addedDrawing {
		b.state = addedPresented
	}
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
				bufs = append(bufs, &SurfaceBuffer{ID: sp.ID, Gen: gen, W: sp.W, H: sp.H, Pitch: sp.Pitch, Format: sp.Format, Kind: "memory", Pix: pix})
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
			// A registration's first buffer.free is its acceptance; after it, a credit is taken only
			// for a frame presented. So a duplicate acceptance, or one for an id never registered,
			// refused, or given up, changes nothing.
			if b := s.added[id]; b != nil && b.state == addedPresented {
				b.state = addedFree
				s.turned()
			} else {
				s.answerAdd(id, nil)
			}
			for gen, bufs := range s.gens {
				for _, b := range bufs {
					if b.ID == id && gen == s.cur {
						s.free[id] = b
					}
				}
			}
			s.mu.Unlock()
			s.poke()
		case OpBufferRefused:
			g.CloseAll()
			if id, reason, err := ReadBufferRefusedMsg(m); err == nil {
				s.mu.Lock()
				s.answerAdd(id, &SurfaceRefused{Reason: reason})
				s.mu.Unlock()
			}
		default:
			g.CloseAll()
		}
	}
}

// NewSurface is the plugin's end of a pixel surface on conn, with the function that takes each
// plugin.surface for it. Serve makes a plugin's own from its environment; this one is for a host's
// tests, which run the SDK against their own end.
func NewSurface(conn *SurfaceConn) (*Surface, func(SurfaceInfo)) {
	s := newSurface(conn)
	return s, s.setInfo
}

// openSurface is the side channel from the environment, close-on-exec at once; nil without one.
func openSurface() *Surface {
	conn, ok, err := OpenPluginSurfaceConn()
	if err != nil || !ok {
		return nil
	}
	return newSurface(conn)
}

// A PLUGIN'S OWN BUFFERS (protocol 10, Linux; docs/decisions/plugins.md#surface-buffers-and-generations).
// A plugin that renders on a GPU may register DMA-BUFs it exported, where the window offers the
// "dmabuf" kind, and present those: the frame never crosses memory. The host answers each
// registration once: it accepts the buffer, or refuses it. Until it accepts, the buffer can't be
// presented; refused, the plugin draws into the host's memory buffers, which Buffer always grants.

// surfaceAdd is a registration the host hasn't answered: its buffer is unavailable until it does.
type surfaceAdd struct {
	buf *SurfaceBuffer
	ans chan error // one answer: nil accepted, *SurfaceRefused, ErrSurfaceRetired or ErrSurfaceClosed
	// abandoned: its caller stopped waiting (under mu). It is never granted, since the caller
	// never learned which of its buffers the id names; the host may still accept it, so it stays
	// counted against MaxAddedBuffers until its generation ends.
	abandoned bool
}

// AddBuffer registers d with the host and waits for its answer. Accepted, the buffer is returned
// (Pix is nil: its pixels are the plugin's own) and AddedBuffer grants it from then on. Refused, the
// error is a *SurfaceRefused, and a retry is a new registration. The fd stays the caller's on
// every path: the host has its own once the registration is sent, so the caller may close its
// own whenever it no longer needs it.
//
// If ctx ends before the answer, the registration is abandoned: it is never granted, whatever the
// host answers, and it counts against MaxAddedBuffers until the generation ends (the host may
// have taken it). So give AddBuffer a ctx that ends with the surface, not a short deadline.
func (s *Surface) AddBuffer(ctx context.Context, d SurfaceDMABuf) (*SurfaceBuffer, error) {
	a, err := s.startAdd(ctx, d)
	if err != nil {
		return nil, err
	}
	msg := map[string]any{"op": OpBufferAdd, "gen": d.Gen, "id": a.buf.ID, "kind": "dmabuf", "format": d.Format,
		"w": d.W, "h": d.H, "modifier": d.Modifier, "planes": []any{map[string]any{"offset": 0, "pitch": d.Pitch}}}
	if err := s.conn.Send(msg, d.FD); err != nil {
		s.mu.Lock()
		delete(s.adds, a.buf.ID) // never sent: no answer can come for it
		s.mu.Unlock()
		return nil, err
	}
	if addSent != nil {
		addSent(a.buf.ID)
	}
	select {
	case err := <-a.ans:
		if err != nil {
			return nil, err
		}
		return a.buf, nil
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		select {
		case err := <-a.ans: // answered as the caller gave up: the answer stands
			if err != nil {
				return nil, err
			}
			return a.buf, nil
		default:
		}
		a.abandoned = true
		s.turned() // an AddedBuffer waiting on this registration alone has nothing to wait for
		return nil, ctx.Err()
	}
}

// startAdd waits for d's generation, checks d against what the window offers, and records the
// registration as unanswered, before it is sent: the answer may come at once.
func (s *Surface) startAdd(ctx context.Context, d SurfaceDMABuf) (*surfaceAdd, error) {
	for {
		s.mu.Lock()
		switch {
		case s.closed:
			s.mu.Unlock()
			return nil, ErrSurfaceClosed
		case d.Gen < s.cur || d.Gen < s.info.Gen:
			s.mu.Unlock()
			return nil, ErrSurfaceRetired
		case d.Gen == s.cur:
			defer s.mu.Unlock()
			if !offersDMABuf(s.info.Kinds, d) {
				return nil, ErrSurfaceKind
			}
			if len(s.adds)+len(s.added)+s.lost >= MaxAddedBuffers {
				return nil, ErrSurfaceBuffers
			}
			// ids are the plugin's odd ones, rising for the surface's lifetime: one is never used
			// twice, so an answer can only be for the registration it names
			s.addID += 2
			a := &surfaceAdd{ans: make(chan error, 1),
				buf: &SurfaceBuffer{ID: s.addID - 1, Gen: d.Gen, W: d.W, H: d.H, Pitch: d.Pitch, Format: d.Format, Kind: "dmabuf"}}
			s.adds[a.buf.ID] = a
			return a, nil
		}
		turn := s.turn
		s.mu.Unlock() // its plugin.surface came, its buffers haven't: the generation isn't current yet
		select {
		case <-turn:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.gone:
			return nil, ErrSurfaceClosed
		}
	}
}

func offersDMABuf(kinds []SurfaceKind, d SurfaceDMABuf) bool {
	if d.W <= 0 || d.H <= 0 || d.Pitch < d.W*4 {
		return false
	}
	for _, k := range kinds {
		if k.Kind == "dmabuf" && k.Format == d.Format && k.Modifier == d.Modifier &&
			(k.MaxW == 0 || d.W <= k.MaxW) && (k.MaxH == 0 || d.H <= k.MaxH) {
			return true
		}
	}
	return false
}

// AddedBuffer is a free buffer of those the plugin registered for the current generation, waiting
// for one; ErrSurfaceBuffers when none is registered or waiting for the host's answer (after a
// resize, the new generation has none until the plugin registers them).
func (s *Surface) AddedBuffer(ctx context.Context) (*SurfaceBuffer, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, ErrSurfaceClosed
		}
		waiting := 0
		for _, a := range s.adds {
			if !a.abandoned {
				waiting++
			}
		}
		if waiting+len(s.added) == 0 {
			s.mu.Unlock()
			return nil, ErrSurfaceBuffers
		}
		for _, b := range s.added {
			if b.state == addedFree && s.cur >= s.info.Gen {
				b.state = addedDrawing
				s.mu.Unlock()
				return b, nil
			}
		}
		turn := s.turn
		s.mu.Unlock()
		select {
		case <-turn:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.gone:
			return nil, ErrSurfaceClosed
		}
	}
}

// A registered buffer's state (under mu). Only a presented one takes a credit.
const (
	addedFree = iota + 1
	addedDrawing
	addedPresented
)

// answerAdd is the host's answer to a registration (under mu): err nil is its acceptance, the
// first buffer.free naming it. false when id names no unanswered registration: a duplicate, a
// forged one, or one for a registration already given up.
func (s *Surface) answerAdd(id int, err error) bool {
	a, ok := s.adds[id]
	if !ok {
		return false
	}
	delete(s.adds, id)
	switch {
	case a.abandoned:
		if err == nil {
			s.lost++ // the host holds it: it keeps its place in the generation's count
		}
		return true
	case err == nil:
		a.buf.state = addedFree
		s.added[id] = a.buf
	}
	a.ans <- err
	s.turned()
	return true
}

// dropAdds is the end of every registration (under mu): a new generation's start, or the
// surface's end. An unanswered one is answered err; an answer the host sends later names an id
// no longer held, and changes nothing.
func (s *Surface) dropAdds(err error) {
	for id, a := range s.adds {
		delete(s.adds, id)
		a.ans <- err
	}
	clear(s.added)
	s.lost = 0
	s.turned()
}

// turned wakes everyone waiting on the registered buffers (under mu): a waiter takes s.turn under
// mu with the state it read, so no change between its read and its wait is missed.
func (s *Surface) turned() {
	close(s.turn)
	s.turn = make(chan struct{})
}

// addSent is a test's pause between a registration's send and its wait: nil but in tests.
var addSent func(id int)
