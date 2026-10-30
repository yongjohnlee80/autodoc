//go:build linux

package tui

import (
	"errors"
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	"golang.org/x/sys/unix"
)

// A PLUGIN'S PIXEL SURFACE, THE HOST'S SIDE
// (docs/decisions/plugins.md#surface-buffers-and-generations): the side channel's buffer registry
// and its one owner. The owner allocates each generation's pool (sealed memfds the host maps
// read-only), admits the plugin's frames into a sink (the window's gui.Stream), and answers each
// frame's outcome and then its buffer's reuse credit. Everything else posts to it: the reader,
// receipt waiters, extimage release callbacks and the loop. No window is needed to run it: a test's
// sink stands in for the stream.

// The bounds. surfaceRetiredGens and surfaceMaxBytes:
// docs/decisions/plugins.md#surface-buffers-and-generations.
const (
	surfaceMaxPixels   = 4096        // a frame's side in device pixels: protocol 10's, below the window's MaxSize
	surfaceMaxDMABufs  = 3           // plugin-added, per generation
	surfaceMaxRetiring = 2           // generations still held by a frame; no new one starts past it
	surfaceRetiredGens = 8           // retired generations whose ids are recorded
	surfaceMaxBytes    = 640 << 20   // 4 held 64 MiB frames, a 192 MiB pool and 3 new 64 MiB DMA-BUFs: always fits
	surfaceMaxBacking  = 64 << 20    // a frame's largest charge: 4096 × 4096 × 4
	surfaceSlack       = 1 << 20     // a DMA-BUF's alignment slack past its descriptor's bytes
	surfaceAckTimeout  = time.Second // a generation not acknowledged by then paces the next anyway
	surfaceGenBuffers  = plugin.MaxPoolBuffers + surfaceMaxDMABufs
	surfaceLiveMax     = surfaceGenBuffers * (1 + surfaceMaxRetiring) // 18
	surfaceSettleCap   = 2*surfaceLiveMax + 4                         // per live buffer one receipt and one release
	surfaceOutCap      = 2*surfaceLiveMax + 4                         // per live buffer one frame.done and one buffer.free, plus buffers ops
	dmaBufMagic        = 0x444d4142                                   // "DMAB", fstatfs's f_type for a DMA-BUF
)

// retiredID is a retired generation's id, as it was when the generation ended.
type retiredID uint8

const (
	retiredIdle retiredID = iota + 1 // available then: one frame of it may be in transit, answered "retired" once
	retiredBusy                      // presented then: the plugin had no credit for it
	retiredUsed                      // its one "retired" answer is given
)

type bufState uint8

const (
	bufAvailable bufState = iota + 1 // the plugin may draw into it and present it
	bufPresented                     // a frame from it is in the sink: its outcome and release are owed
	bufRetired                       // its generation ended while a frame held it: destroyed at its release
)

// surfaceBacking is a buffer's memory: the host's read-only mapping, or the plugin's DMA-BUF fd.
// refs is the registry's one plus one per frame holding it; it is destroyed exactly once, at zero.
type surfaceBacking struct {
	mem       []byte
	fd        int
	refs      int
	destroyed bool
	size      int64  // its charge against surfaceMaxBytes
	charged   *int64 // the owner's live total, which it leaves when destroyed
}

func (b *surfaceBacking) unref() {
	if b.refs--; b.refs > 0 || b.destroyed {
		return
	}
	b.destroyed = true
	*b.charged -= b.size
	if b.mem != nil {
		unix.Munmap(b.mem)
		b.mem = nil
	}
	if b.fd >= 0 {
		unix.Close(b.fd)
		b.fd = -1
	}
}

type surfaceBuffer struct {
	spec     plugin.SurfaceBufferSpec
	dmabuf   bool
	modifier uint64
	state    bufState
	back     *surfaceBacking
	seq      uint64
	outcome  bool // its frame's frame.done is written
	released bool // its frame's extimage release has run
}

// settleEvent is a receipt's outcome or a frame's release, for the owner.
type settleEvent struct {
	id       int
	seq      uint64
	released bool // a release, else an outcome
	outcome  string
	when     time.Time
}

type ctlEvent struct {
	msg   map[string]any // an admitted datagram, its fds in guard
	guard *plugin.FDGuard
	err   error // the reader ended
}

type outMsg struct {
	msg map[string]any
	fds []int // closed once sent, or once the writer ends
}

// surfaceOwner owns one surface's registry. Its exported face is the loop's: Resize, Close.
type surfaceOwner struct {
	conn     *plugin.SurfaceConn
	sink     surfaceSink
	announce func(plugin.SurfaceInfo) // plugin.surface on stdout; the loop's send, which never blocks
	violate  func(why string)         // closes the plugin; posted, never blocks

	ctl    chan ctlEvent
	settle chan settleEvent
	out    chan outMsg
	wake   chan struct{} // a Resize or a Close waits in the fields below

	mu        sync.Mutex
	want      *surfaceGeometry // the latest geometry asked for, not yet a generation
	closing   bool
	violated  atomic.Bool
	ended     chan struct{} // the owner has settled everything and gone
	waiters   sync.WaitGroup
	writerEnd chan struct{}
	readerEnd chan struct{}
	ackTimer  *time.Timer // the one wake for the ack's timeout, while it is pending

	// the owner's alone
	bufs     map[int]*surfaceBuffer
	gen      int
	geom     surfaceGeometry
	nextID   int // the host's ids: even
	lastSeq  uint64
	retired  map[int]map[int]retiredID // the last surfaceRetiredGens generations' ids
	retiring map[int]int               // a retired generation → its buffers a frame still holds
	charged  int64                     // every live allocation's bytes
	acked    int                       // the latest generation the plugin acknowledged (surface.ack, or a frame or buffer.add naming it)
	genStart time.Time                 // when the current generation started, for the ack's timeout
	ceiling  int64                     // surfaceMaxBytes; a cell sets a smaller one
	closed   bool
}

func newSurfaceOwner(conn *plugin.SurfaceConn, sink surfaceSink, announce func(plugin.SurfaceInfo), violate func(string)) *surfaceOwner {
	return newSurfaceOwnerCeiling(conn, sink, announce, violate, surfaceMaxBytes)
}

// newSurfaceOwnerCeiling is newSurfaceOwner with a memory ceiling of its own: a cell's smaller one.
func newSurfaceOwnerCeiling(conn *plugin.SurfaceConn, sink surfaceSink, announce func(plugin.SurfaceInfo), violate func(string), ceiling int64) *surfaceOwner {
	s := &surfaceOwner{conn: conn, sink: sink, announce: announce, violate: violate,
		ctl: make(chan ctlEvent), settle: make(chan settleEvent, surfaceSettleCap),
		out: make(chan outMsg, surfaceOutCap), wake: make(chan struct{}, 1),
		ended: make(chan struct{}), writerEnd: make(chan struct{}), readerEnd: make(chan struct{}), bufs: map[int]*surfaceBuffer{}, nextID: 2,
		retired: map[int]map[int]retiredID{}, retiring: map[int]int{}, ceiling: ceiling}
	go s.read()
	go s.write()
	go s.run()
	return s
}

// Resize asks for a generation of geometry g; several before the owner starts one coalesce.
func (s *surfaceOwner) Resize(g surfaceGeometry) {
	s.mu.Lock()
	s.want = &g
	s.mu.Unlock()
	s.poke()
}

// Close stops admission and retires everything; each buffer is destroyed at its last holder.
func (s *surfaceOwner) Close() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.poke()
}

// Ended closes once the owner has settled every buffer and gone.
func (s *surfaceOwner) Ended() <-chan struct{} { return s.ended }

func (s *surfaceOwner) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *surfaceOwner) fail(why string) {
	if s.violated.CompareAndSwap(false, true) {
		s.violate(why)
	}
	s.Close()
}

// post is a release callback's or a waiter's: it never blocks. The settle channel holds two per
// live buffer, which each has at most one frame in flight (outcome before credit), so a full one
// is a bug: the plugin is closed as stalled.
func (s *surfaceOwner) post(e settleEvent) {
	select {
	case s.settle <- e:
	default:
		s.fail("side channel stalled")
	}
}

func (s *surfaceOwner) read() {
	defer close(s.readerEnd)
	for {
		m, g, err := s.conn.Recv()
		select {
		case s.ctl <- ctlEvent{msg: m, guard: g, err: err}:
		case <-s.ended:
			if g != nil {
				g.CloseAll()
			}
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *surfaceOwner) write() {
	defer close(s.writerEnd)
	broken := false
	for o := range s.out {
		if !broken {
			if err := s.conn.Send(o.msg, o.fds...); err != nil {
				broken = true // a disconnect or a send past its timeout: what is owed is dropped, settled locally
				if !errors.Is(err, plugin.ErrSurfaceClosed) {
					s.fail("side channel stalled") // a peer that doesn't read: the send's deadline passed
				}
			}
		}
		for _, fd := range o.fds {
			unix.Close(fd)
		}
	}
}

// emit queues m for the plugin, never blocking: out holds what every live buffer can owe.
func (s *surfaceOwner) emit(m map[string]any, fds ...int) {
	select {
	case s.out <- outMsg{m, fds}:
	default:
		for _, fd := range fds {
			unix.Close(fd)
		}
		s.fail("side channel stalled")
	}
}

func (s *surfaceOwner) run() {
	defer func() {
		close(s.out)
		s.conn.Shutdown() // first: a send blocked on a peer that doesn't read returns now, and the reader wakes
		<-s.writerEnd     // it closes every fd still queued
		close(s.ended)
		<-s.readerEnd
		s.conn.Close() // the fd only now: no Recvmsg can meet a reused number
	}()
	for {
		s.mu.Lock()
		closing, want := s.closing, s.want
		s.mu.Unlock()
		if closing && !s.closed {
			s.shutdown()
		}
		if s.closed && s.drained() {
			return
		}
		if want != nil && !s.closed && len(s.retiring) < surfaceMaxRetiring && s.paced() {
			s.mu.Lock()
			s.want = nil
			s.mu.Unlock()
			if s.gen == 0 || !want.same(s.geom) {
				s.startGeneration(*want)
			}
		}
		select {
		case e := <-s.ctl:
			if e.err != nil {
				if !s.closed {
					s.fail("its side channel closed") // a surface that can't answer is the plugin's end
				}
				continue
			}
			if s.closed {
				e.guard.CloseAll()
				continue
			}
			if why := s.admit(e.msg, e.guard); why != "" {
				s.fail(why)
			}
		case e := <-s.settle:
			s.onSettle(e)
		case <-s.wake:
		}
	}
}

// drained: nothing is owed, every buffer destroyed.
func (s *surfaceOwner) drained() bool {
	for _, b := range s.bufs {
		if !b.back.destroyed {
			return false
		}
	}
	return true
}

func (s *surfaceOwner) shutdown() {
	s.closed = true
	s.sink.Close() // every pending receipt completes; a selected frame stays leased until presented
	for id, b := range s.bufs {
		if b.state == bufAvailable {
			b.back.unref()
			delete(s.bufs, id)
		} else if b.state == bufPresented {
			b.state = bufRetired
		}
	}
}

// poolPixels is g's pool size: the surface's device pixels, a side at most surfaceMaxPixels (the
// stream scales a smaller frame into the surface).
func poolPixels(g surfaceGeometry) (int, int) {
	w, h := g.pixels()
	return min(w, surfaceMaxPixels), min(h, surfaceMaxPixels)
}

// paced: the plugin has acknowledged the current generation, or the ack's timeout has passed (a
// wake is set for it), so a new generation may start.
func (s *surfaceOwner) paced() bool {
	if s.gen == 0 || s.acked >= s.gen {
		s.stopAckTimer()
		return true
	}
	if left := surfaceAckTimeout - time.Since(s.genStart); left > 0 {
		if s.ackTimer == nil { // one wake per wait, not one per pass of the loop
			s.ackTimer = time.AfterFunc(left, s.poke)
		}
		return false
	}
	s.stopAckTimer()
	return true
}

// stopAckTimer drops the ack's wake (the owner's alone: the timer itself only pokes).
func (s *surfaceOwner) stopAckTimer() {
	if s.ackTimer != nil {
		s.ackTimer.Stop()
		s.ackTimer = nil
	}
}

// startGeneration retires the current generation and installs g's pool.
func (s *surfaceOwner) startGeneration(g surfaceGeometry) {
	w, h := poolPixels(g)
	format, ok := poolFormat(g.Kinds)
	if w <= 0 || h <= 0 || w > surfaceMaxPixels || h > surfaceMaxPixels || !ok {
		return // a size or caps the window can't draw: no generation, the placeholder stays
	}
	// First: idle old buffers are destroyed, so the reservation below always fits
	// (docs/decisions/plugins.md#surface-buffers-and-generations).
	s.retire()
	pitch := w * 4
	size := pitch * h
	if s.charged+int64(plugin.MaxPoolBuffers)*int64(size) > s.ceiling {
		s.fail("the surface's memory budget can't hold its new buffers")
		return
	}
	s.gen++
	s.geom = g
	s.genStart = time.Now()
	var specs []plugin.SurfaceBufferSpec
	var fds []int
	for range plugin.MaxPoolBuffers {
		fd, mem, err := sealedMemfd(size)
		if err != nil {
			for _, f := range fds {
				unix.Close(f)
			}
			s.fail("the host could not allocate the surface's buffers: " + err.Error())
			return
		}
		spec := plugin.SurfaceBufferSpec{ID: s.nextID, Gen: s.gen, Kind: "memory", Format: format, W: w, H: h, Pitch: pitch, Size: size}
		s.nextID += 2
		s.charged += int64(size)
		s.bufs[spec.ID] = &surfaceBuffer{spec: spec, state: bufAvailable, back: &surfaceBacking{mem: mem, fd: -1, refs: 1, size: int64(size), charged: &s.charged}}
		specs, fds = append(specs, spec), append(fds, fd)
	}
	s.emit(plugin.BuffersMsg(s.gen, specs), fds...) // the writer closes the host's copies once sent
	s.announce(plugin.SurfaceInfo{Gen: s.gen, W: g.W, H: g.H, Scale: g.Scale, Kinds: g.Kinds})
}

// retire ends the current generation's buffers: an available one is destroyed now, a held one at
// its release, without a buffer.free. Each id is recorded as it was, for frames still in transit.
func (s *surfaceOwner) retire() {
	if s.gen == 0 {
		return
	}
	rec := map[int]retiredID{}
	for id, b := range s.bufs {
		if b.spec.Gen != s.gen {
			continue
		}
		switch b.state {
		case bufAvailable:
			rec[id] = retiredIdle
			b.back.unref()
			delete(s.bufs, id)
		case bufPresented:
			rec[id] = retiredBusy
			b.state = bufRetired
			s.retiring[s.gen]++
		}
	}
	s.retired[s.gen] = rec
	delete(s.retired, s.gen-surfaceRetiredGens)
}

// retiredFrame answers a frame of a retired generation: "retired" once for an id that was idle
// when it ended (it was in transit); a violation for a busy, never-issued or answered one, or a
// generation past the record.
func (s *surfaceOwner) retiredFrame(f plugin.SurfaceFrame) string {
	rec, ok := s.retired[f.Gen]
	if !ok || rec[f.Buffer] != retiredIdle {
		return "a frame of a retired generation's busy, unknown or answered buffer"
	}
	rec[f.Buffer] = retiredUsed
	s.emit(plugin.FrameDoneMsg(plugin.SurfaceFrameDone{Seq: f.Seq, Outcome: "retired"}))
	return ""
}

func poolFormat(kinds []plugin.SurfaceKind) (string, bool) {
	for _, k := range kinds {
		if k.Kind == "memory" && (k.Format == "rgba8" || k.Format == "bgra8") {
			return k.Format, true
		}
	}
	return "", false
}

// sealedMemfd is a memfd of size bytes that can't shrink or grow, mapped read-only for the host.
func sealedMemfd(size int) (int, []byte, error) {
	fd, err := unix.MemfdCreate("autodoc-surface", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, nil, err
	}
	fail := func(err error) (int, []byte, error) { unix.Close(fd); return -1, nil, err }
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		return fail(err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_SEAL); err != nil {
		return fail(err)
	}
	mem, err := unix.Mmap(fd, 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return fail(err)
	}
	return fd, mem, nil
}

// admit handles one datagram from the plugin; a non-empty answer is the violation that closes it.
// Every fd in g is closed here unless admission took it.
func (s *surfaceOwner) admit(m map[string]any, g *plugin.FDGuard) string {
	defer g.CloseAll()
	switch plugin.WireOp(m) {
	case plugin.OpFrame:
		if g.Len() != 0 {
			return "a frame carried descriptors"
		}
		f, err := plugin.ReadFrameMsg(m)
		if err != nil {
			return err.Error()
		}
		if f.Gen == s.gen {
			s.acked = s.gen
		}
		return s.present(f)
	case plugin.OpSurfaceAck:
		if gen, ok := intOf(m, "gen"); ok && gen == s.gen {
			s.acked = gen
		}
		return ""
	case plugin.OpBufferAdd:
		return s.addDMABuf(m, g)
	case plugin.OpBufferDrop:
		id, ok := intOf(m, "id")
		b := s.bufs[id]
		if !ok || b == nil || !b.dmabuf || b.state != bufAvailable {
			return "buffer.remove of a buffer the plugin can't remove"
		}
		b.back.unref()
		delete(s.bufs, id)
		return ""
	}
	return fmt.Sprintf("unknown side-channel op %q", plugin.WireOp(m))
}

func intOf(m map[string]any, k string) (int, bool) {
	switch n := m[k].(type) {
	case int64:
		return int(n), true
	case uint64:
		return int(n), n < 1<<31
	}
	return 0, false
}

// present admits a frame. Its reservation is undone on any failure: no release comes for a frame
// that was never made.
func (s *surfaceOwner) present(f plugin.SurfaceFrame) string {
	if f.Seq <= s.lastSeq { // first, on every path: a seq is answered once at most
		return "a frame's seq did not rise"
	}
	s.lastSeq = f.Seq
	switch {
	case f.Gen < s.gen:
		return s.retiredFrame(f)
	case f.Gen > s.gen:
		return "a frame of a generation not yet given"
	}
	b := s.bufs[f.Buffer]
	if b == nil || b.spec.Gen != f.Gen || b.state != bufAvailable {
		return "a frame of a buffer that isn't available"
	}
	if !f.Dirty.Empty() && !f.Dirty.In(image.Rect(0, 0, b.spec.W, b.spec.H)) {
		return "a frame's dirty rect lies outside its buffer"
	}
	b.state, b.seq, b.outcome, b.released = bufPresented, f.Seq, false, false
	b.back.refs++
	id, seq := b.spec.ID, f.Seq
	fr, err := extimage.NewFrame(b.desc(f.Dirty), func() { s.post(settleEvent{id: id, seq: seq, released: true}) })
	if err != nil {
		b.state = bufAvailable
		b.back.refs--
		return "a frame extimage refused: " + err.Error()
	}
	r := s.sink.Push(fr)
	s.waiters.Add(1)
	go func() {
		defer s.waiters.Done()
		<-r.Done() // Close completes every pending receipt
		o, when := r.Result()
		s.post(settleEvent{id: id, seq: seq, outcome: o, when: when})
	}()
	return ""
}

func (b *surfaceBuffer) desc(dirty image.Rectangle) extimage.Desc {
	format := extimage.FormatRGBA8
	if b.spec.Format == "bgra8" {
		format = extimage.FormatBGRA8
	}
	d := extimage.Desc{Format: format, Size: image.Pt(b.spec.W, b.spec.H), Dirty: dirty}
	if b.dmabuf {
		d.Kind, d.Modifier = extimage.KindDMABuf, b.modifier
		d.Planes = []extimage.Plane{{FD: b.back.fd, Pitch: uint32(b.spec.Pitch)}}
	} else {
		d.Kind = extimage.KindMemory
		d.Planes = []extimage.Plane{{Data: b.back.mem, Pitch: uint32(b.spec.Pitch)}}
	}
	return d
}

// onSettle takes a frame's outcome or its release. The outcome is written before the credit.
func (s *surfaceOwner) onSettle(e settleEvent) {
	b := s.bufs[e.id]
	if b == nil || b.seq != e.seq || (b.state != bufPresented && b.state != bufRetired) {
		return
	}
	if e.released {
		b.released = true
	} else {
		b.outcome = true
		var when int64
		if !e.when.IsZero() {
			when = e.when.UnixNano()
		}
		if !s.closed {
			s.emit(plugin.FrameDoneMsg(plugin.SurfaceFrameDone{Seq: e.seq, Outcome: e.outcome, WhenNS: when}))
		}
	}
	if !b.outcome || !b.released {
		return
	}
	b.back.unref()
	if b.state == bufRetired {
		b.back.unref() // the registry's own ref: destroyed now, exactly once
		delete(s.bufs, e.id)
		if n, ok := s.retiring[b.spec.Gen]; ok { // shutdown's retirements aren't counted: they pace nothing
			if n <= 1 {
				delete(s.retiring, b.spec.Gen)
			} else {
				s.retiring[b.spec.Gen] = n - 1
			}
		}
		return
	}
	b.state = bufAvailable
	if !s.closed {
		s.emit(plugin.BufferFreeMsg(b.spec.ID))
	}
}

// addDMABuf admits a plugin's DMA-BUF (Linux; protocol 10 takes LINEAR, one plane): the object
// must be a DMA-BUF (fstatfs), the plane must lie inside its real size (lseek), and the generation's
// caps must list it. A buffer.add of a generation the host retired was in transit: its fds close.
func (s *surfaceOwner) addDMABuf(m map[string]any, g *plugin.FDGuard) string {
	gen, okG := intOf(m, "gen")
	if okG && gen < s.gen {
		// in transit across a resize: its fds close (the guard), with no answer; its id is recorded
		// idle, so the one frame the plugin may have sent from it is answered retired
		if rec, ok := s.retired[gen]; ok {
			if id, ok := intOf(m, "id"); ok && id%2 == 1 && rec[id] == 0 && pluginIDs(rec) < surfaceMaxDMABufs {
				rec[id] = retiredIdle
			}
		}
		return ""
	}
	if okG && gen == s.gen {
		s.acked = s.gen
	}
	id, okI := intOf(m, "id")
	w, okW := intOf(m, "w")
	h, okH := intOf(m, "h")
	format, _ := m["format"].(string)
	planes, _ := m["planes"].([]any)
	var modifier uint64
	switch v := m["modifier"].(type) {
	case uint64:
		modifier = v
	case int64:
		modifier = uint64(v)
	}
	switch {
	case !okG || !okI || !okW || !okH || gen != s.gen:
		return "buffer.add wants this generation's gen, an id, w and h"
	case id%2 == 0 || s.bufs[id] != nil:
		return "buffer.add of an even or reused id"
	case len(planes) != 1 || g.Len() != 1:
		return "buffer.add takes one plane, one fd"
	case !s.offers("dmabuf", format, modifier, w, h):
		return "buffer.add of a kind this window's caps don't list"
	case s.dmabufs() >= surfaceMaxDMABufs:
		return "too many DMA-BUFs"
	}
	p, _ := planes[0].(map[string]any)
	offset, okO := intOf(p, "offset")
	pitch, okP := intOf(p, "pitch")
	if !okO || !okP {
		return "buffer.add's plane wants offset and pitch"
	}
	fd := g.Take(0)
	if why := admitDMABuf(fd, w, h, offset, pitch); why != "" {
		unix.Close(fd)
		if why == errDMABufSize {
			s.emit(map[string]any{"op": plugin.OpBufferRefused, "id": id, "reason": "size"})
			return "" // too big a backing: refused, not a violation; the plugin may use memory frames
		}
		return why
	}
	if offset != 0 {
		unix.Close(fd)
		return "buffer.add's plane must start at 0"
	}
	spec := plugin.SurfaceBufferSpec{ID: id, Gen: gen, Kind: "dmabuf", Format: format, W: w, H: h, Pitch: pitch}
	size, _ := unix.Seek(fd, 0, 2)
	if s.charged+size > s.ceiling {
		unix.Close(fd)
		s.emit(map[string]any{"op": plugin.OpBufferRefused, "id": id, "reason": "budget"})
		return "" // not yet: refused, not a violation; the plugin may retry after a credit
	}
	s.charged += size
	s.bufs[id] = &surfaceBuffer{spec: spec, dmabuf: true, modifier: modifier, state: bufAvailable,
		back: &surfaceBacking{fd: fd, refs: 1, size: size, charged: &s.charged}}
	// The acceptance credit: presentable from now
	// (docs/decisions/plugins.md#surface-buffers-and-generations).
	s.emit(plugin.BufferFreeMsg(id))
	return ""
}

func (s *surfaceOwner) dmabufs() int {
	n := 0
	for _, b := range s.bufs {
		if b.dmabuf && b.spec.Gen == s.gen {
			n++
		}
	}
	return n
}

func (s *surfaceOwner) offers(kind, format string, modifier uint64, w, h int) bool {
	for _, k := range s.geom.Kinds {
		if k.Kind == kind && k.Format == format && k.Modifier == modifier && modifier == 0 &&
			(k.MaxW == 0 || w <= k.MaxW) && (k.MaxH == 0 || h <= k.MaxH) {
			return true
		}
	}
	return false
}

// admitDMABuf is the object check: a DMA-BUF (fstatfs's magic), big enough for its plane by its
// own size (lseek to its end), with overflow-checked arithmetic. "" when it is admitted.
func admitDMABuf(fd, w, h, offset, pitch int) string {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil || st.Type != dmaBufMagic {
		return "buffer.add: not a DMA-BUF"
	}
	size, err := unix.Seek(fd, 0, 2)
	if err != nil || size <= 0 {
		return "buffer.add: a DMA-BUF without a size"
	}
	return planeLayout(size, w, h, offset, pitch)
}

// planeLayout is the layout check against an object of size bytes. Every wire integer is bounded
// before any arithmetic, so no product can overflow: w and h at most 4096, pitch at most 64 MiB
// (which also fits extimage's uint32 Pitch), and offset within the object. Then the plane's end
// (the last row needs only its pixels) is at most 2^38, well inside int64.
func planeLayout(size int64, w, h, offset, pitch int) string {
	switch {
	case w <= 0 || h <= 0 || w > surfaceMaxPixels || h > surfaceMaxPixels:
		return "buffer.add: an impossible size"
	case pitch < w*4 || pitch > surfaceMaxBacking:
		return "buffer.add: a pitch below its row or past 64 MiB"
	case offset < 0 || int64(offset) > size:
		return "buffer.add: an offset outside the DMA-BUF"
	}
	end := int64(offset) + int64(pitch)*int64(h-1) + int64(w)*4
	if end > size {
		return fmt.Sprintf("buffer.add: the plane ends at %d, past the DMA-BUF's %d bytes", end, size)
	}
	if size > int64(pitch)*int64(h)+surfaceSlack || size > surfaceMaxBacking {
		return errDMABufSize // its real size passes what its frame can show: every held charge stays at most 64 MiB
	}
	return ""
}

// errDMABufSize is admitDMABuf's answer for a backing larger than its frame allows: buffer.refused
// {reason: "size"}, not a violation.
const errDMABufSize = "buffer.add: a DMA-BUF larger than its frame"

// surfaceSinkOf is where a run's frames go: the window's stream (pluginsurface_gui.go), or none in
// a terminal. A cell sets its own, so the host's whole surface path runs without a window.
var surfaceSinkOf = (*plugins).newSurfaceSink

// openSurfaceChannel is e's side channel, when e declares a surface and the window draws streams:
// the child's end (fd 3), attach to start its owner once the run exists, and abort for a start that
// failed. nil, nil, nil: no surface, and the plugin draws cells.
func (pl *plugins) openSurfaceChannel(e pluginEntry) (child *os.File, attach func(*pluginRun), abort func()) {
	if e.m.Surface == nil || e.m.Protocol < 10 {
		return nil, nil, nil
	}
	sink, stream, ok := surfaceSinkOf(pl)
	if !ok {
		return nil, nil, nil
	}
	host, child, err := plugin.NewSurfacePair()
	if err != nil {
		sink.Close()
		return nil, nil, nil
	}
	attach = func(r *pluginRun) {
		r.surfaceStream = stream
		r.surface = newSurfaceOwner(host, sink,
			func(i plugin.SurfaceInfo) {
				pl.c.Post(func() { r.send(plugin.MethodSurface, plugin.SurfaceParams(i)) })
			},
			func(why string) {
				pl.c.Post(func() {
					if why != "" {
						r.close(fmt.Sprintf("%s: %s", e.m.Name, why))
					}
				})
			})
	}
	abort = func() {
		host.Close()
		sink.Close()
	}
	return child, attach, abort
}

// pluginIDs is how many of a retired generation's recorded ids are the plugin's (odd).
func pluginIDs(rec map[int]retiredID) int {
	n := 0
	for id := range rec {
		if id%2 == 1 {
			n++
		}
	}
	return n
}
