//go:build linux

package tui

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	"golang.org/x/sys/unix"
)

// fakeSink holds each pushed frame as a stream would: its receipt completes, and its reference is
// dropped, only when the cell says. Close completes every pending receipt "closed" and keeps the
// frames held, as a stream keeps a selected picture leased past Close.
type fakeSink struct {
	mu     sync.Mutex
	held   []*fakeHeld
	issued []*fakeReceipt // every receipt handed out, which Close completes
	auto   string         // non-empty: complete each push at once with this outcome, and drop it
	closed bool
	pushed chan struct{} // a frame is held: the signal take waits on (pushedC)
}

type fakeHeld struct {
	f *extimage.Frame
	r *fakeReceipt
}

type fakeReceipt struct {
	done    chan struct{}
	once    sync.Once
	outcome string
}

func (r *fakeReceipt) Done() <-chan struct{}       { return r.done }
func (r *fakeReceipt) Result() (string, time.Time) { return r.outcome, time.Unix(0, 42) }
func (r *fakeReceipt) complete(o string)           { r.once.Do(func() { r.outcome = o; close(r.done) }) }
func newFakeReceipt() *fakeReceipt                 { return &fakeReceipt{done: make(chan struct{})} }

func (k *fakeSink) Push(f *extimage.Frame) surfaceReceipt {
	r := newFakeReceipt()
	k.mu.Lock()
	k.issued = append(k.issued, r)
	auto := k.auto
	if auto == "" {
		k.held = append(k.held, &fakeHeld{f, r})
		k.signal()
	}
	k.mu.Unlock()
	if auto != "" {
		r.complete(auto)
		f.Drop()
	}
	return r
}

// pushedC is the sink's signal that a frame is held, made at its first use; k.mu held.
func (k *fakeSink) pushedC() chan struct{} {
	if k.pushed == nil {
		k.pushed = make(chan struct{}, 1)
	}
	return k.pushed
}

// signal tells a waiting take a frame is held; k.mu held.
func (k *fakeSink) signal() {
	select {
	case k.pushedC() <- struct{}{}:
	default:
	}
}

func (k *fakeSink) Close() {
	k.mu.Lock()
	k.closed = true
	issued := append([]*fakeReceipt(nil), k.issued...)
	k.mu.Unlock()
	for _, r := range issued {
		r.complete("closed")
	}
}

// surfaceWait is when a surface cell still waiting for what must come (a frame at the sink, a side-
// channel op, a violation, the owner's end) logs every goroutine's stack, once: what the owner was
// doing, should the wait end in a failure. It is not the bound: see patience.
const surfaceWait = 5 * time.Second * raceFactor

// surfaceMargin is what a cell's deadline keeps back for the failure's report.
const surfaceMargin = 10 * time.Second

// patience bounds one wait of a surface cell by the cell's own deadline (go test -timeout, less
// surfaceMargin), never a fixed wall clock: the owner answers each step at once (it never waits on
// the plugin), so only its goroutines not being run hold a frame up, and a loaded -race run of the
// whole gui suite on two CPUs has held one past 20 s. A real stall still fails, with the stacks
// logged at surfaceWait. Each wait returns as soon as what it waits for comes.
type patience struct {
	t      *testing.T
	what   string
	timer  *time.Timer
	logged bool
}

// surfaceWaitFor is the patience of one wait for what, failing as what when it ends.
func surfaceWaitFor(t *testing.T, what string) *patience {
	return &patience{t: t, what: what, timer: time.NewTimer(surfaceWait)}
}

func (p *patience) C() <-chan time.Time { return p.timer.C }
func (p *patience) stop()               { p.timer.Stop() }

// late takes the timer's firing: at the first (surfaceWait), every goroutine's stack is logged
// and the timer set to the cell's deadline; at the second, the cell fails.
func (p *patience) late() {
	p.t.Helper()
	if p.logged {
		p.t.Fatal(p.what)
	}
	p.logged = true
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	p.t.Logf("still waiting after %v (%s); every goroutine:\n%s", surfaceWait, p.what, buf)
	rest := 10 * time.Minute // go test -timeout 0: no deadline
	if d, ok := p.t.Deadline(); ok {
		rest = max(time.Until(d)-surfaceMargin, 0)
	}
	p.timer.Reset(rest)
}

// surfaceEventually waits until cond holds, polling it: for what the owner does without a word to
// the plugin (a buffer unmapped), whose own signal is cond.
func surfaceEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	p := surfaceWaitFor(t, what)
	defer p.stop()
	for !cond() {
		select {
		case <-p.C():
			p.late()
		case <-time.After(time.Millisecond):
		}
	}
}

// surfaceRig is a host's owner and the plugin's end of its side channel.
type surfaceRig struct {
	owner    *surfaceOwner
	sink     *fakeSink
	plug     *plugin.SurfaceConn
	infos    chan plugin.SurfaceInfo
	violated chan string
	msgs     chan map[string]any

	inoMu sync.Mutex
	inos  map[uint64]bool // the inodes of the buffers sent this rig: its own, whatever else the process maps
}

func newSurfaceRig(t *testing.T) *surfaceRig { return newSurfaceRigCeiling(t, surfaceMaxBytes) }

func newSurfaceRigCeiling(t *testing.T, ceiling int64) *surfaceRig {
	t.Helper()
	host, child, err := plugin.NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Dup(int(child.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	t.Setenv(plugin.SurfaceFDEnv, strconv.Itoa(fd))
	plug, ok, err := plugin.OpenPluginSurfaceConn()
	if err != nil || !ok {
		t.Fatal(err)
	}
	r := &surfaceRig{sink: &fakeSink{}, plug: plug, infos: make(chan plugin.SurfaceInfo, 64),
		violated: make(chan string, 8), msgs: make(chan map[string]any, 4096), inos: map[uint64]bool{}}
	r.owner = newSurfaceOwnerCeiling(host, r.sink, func(i plugin.SurfaceInfo) { r.infos <- i }, func(why string) { r.violated <- why }, ceiling)
	stop, read := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(read)
		for {
			m, g, err := plug.Recv()
			if err != nil {
				close(r.msgs)
				return
			}
			for i := range g.Len() { // before the op is read, so a cell's count after it includes them
				var st unix.Stat_t
				fd := g.Take(i)
				if unix.Fstat(fd, &st) == nil {
					r.inoMu.Lock()
					r.inos[st.Ino] = true
					r.inoMu.Unlock()
				}
				unix.Close(fd) // the plugin's memory buffers: the cells read the ops, not the pixels
			}
			select {
			case r.msgs <- m:
			case <-stop:
				return
			}
		}
	}()
	// The plugin's end is closed only once its reader is done (SurfaceConn.Close's contract, as the
	// SDK's Surface.Close keeps it): closed under a reader still looping, the reader's next Recv
	// took its fd's number as it was reused, by the next cell's host end, and that cell's frames
	// went to this reader, never reaching its owner ("no frame reached the sink").
	t.Cleanup(func() {
		r.owner.Close()
		close(stop)
		plug.Shutdown()
		<-read
		plug.Close()
	})
	return r
}

var rigGeometry = surfaceGeometry{W: 16, H: 8, Scale: 1, Kinds: []plugin.SurfaceKind{{Kind: "memory", Format: "rgba8"}}}

// next is the plugin's next side-channel op. A violation the owner closed the plugin for says so,
// rather than the op that then never comes.
func (r *surfaceRig) next(t *testing.T) map[string]any {
	t.Helper()
	p := surfaceWaitFor(t, "no side-channel op arrived")
	defer p.stop()
	for {
		select {
		case m, ok := <-r.msgs:
			if !ok {
				t.Fatalf("the side channel closed%s", r.violatedSince())
			}
			return m
		case <-p.C():
			p.what = "no side-channel op arrived" + r.violatedSince()
			p.late()
		}
	}
}

// take is the sink's oldest held frame (fakeSink.take), or a violation the owner closed the plugin
// for, which no frame follows.
func (r *surfaceRig) take(t *testing.T) *fakeHeld {
	t.Helper()
	p := surfaceWaitFor(t, "no frame reached the sink")
	defer p.stop()
	for {
		r.sink.mu.Lock()
		if len(r.sink.held) > 0 {
			h := r.sink.held[0]
			r.sink.held = r.sink.held[1:]
			r.sink.mu.Unlock()
			return h
		}
		pushed := r.sink.pushedC()
		r.sink.mu.Unlock()
		select {
		case <-pushed:
		case why := <-r.violated:
			t.Fatalf("no frame reached the sink: the owner closed the plugin: %s", why)
		case <-p.C():
			p.late()
		}
	}
}

// violatedSince is ", after a violation: why", or "" when there was none.
func (r *surfaceRig) violatedSince() string {
	select {
	case why := <-r.violated:
		return ", after a violation: " + why
	default:
		return ""
	}
}

// nextOp is the plugin's next side-channel op named op, skipping others (a new generation's buffers
// may come before a frame's outcome).
func (r *surfaceRig) nextOp(t *testing.T, op string) map[string]any {
	t.Helper()
	for {
		if m := r.next(t); plugin.WireOp(m) == op {
			return m
		}
	}
}

// pool starts a generation and returns its buffer ids, acknowledged as a conforming plugin does.
func (r *surfaceRig) pool(t *testing.T, g surfaceGeometry) (gen int, ids []int) {
	t.Helper()
	gen, ids = r.poolUnacked(t, g)
	if err := r.plug.Send(map[string]any{"op": plugin.OpSurfaceAck, "gen": gen}); err != nil {
		t.Fatal(err)
	}
	return gen, ids
}

// poolUnacked starts a generation, as a busy plugin receives it: not acknowledged.
func (r *surfaceRig) poolUnacked(t *testing.T, g surfaceGeometry) (gen int, ids []int) {
	t.Helper()
	r.owner.Resize(g)
	m := r.next(t)
	if plugin.WireOp(m) != plugin.OpBuffers {
		t.Fatalf("op %q, want buffers", plugin.WireOp(m))
	}
	gen, specs, err := plugin.ReadBuffersMsg(m, plugin.MaxPoolBuffers)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		ids = append(ids, s.ID)
	}
	return gen, ids
}

func (r *surfaceRig) present(t *testing.T, seq uint64, gen, buf int) {
	t.Helper()
	if err := r.plug.Send(plugin.FrameMsg(plugin.SurfaceFrame{Seq: seq, Gen: gen, Buffer: buf})); err != nil {
		t.Fatal(err)
	}
}

func (r *surfaceRig) noViolation(t *testing.T) {
	t.Helper()
	select {
	case why := <-r.violated:
		t.Fatalf("a violation: %s", why)
	default:
	}
}

func (r *surfaceRig) violation(t *testing.T) string {
	t.Helper()
	p := surfaceWaitFor(t, "no violation")
	defer p.stop()
	for {
		select {
		case why := <-r.violated:
			return why
		case <-p.C():
			p.late()
		}
	}
}

// mappings is how many of this rig's surface buffers the process has mapped: by inode, so another
// cell's owner, still tearing down, never moves the count.
func (r *surfaceRig) mappings(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	r.inoMu.Lock()
	defer r.inoMu.Unlock()
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasPrefix(f[5], "/memfd:autodoc-surface") {
			continue
		}
		if ino, err := strconv.ParseUint(f[4], 10, 64); err == nil && r.inos[ino] {
			n++
		}
	}
	return n
}

// One buffer is presented again and again: each time its frame's frame.done comes first, then its
// buffer.free, and the next frame from it is admitted.
func TestASurfaceBufferIsReusedThroughManyFrames(t *testing.T) {
	r := newSurfaceRig(t)
	gen, ids := r.pool(t, rigGeometry)
	for i := range 5 {
		seq := uint64(i + 1)
		r.present(t, seq, gen, ids[0])
		h := r.take(t)
		h.r.complete("drawn")
		h.f.Drop()
		done, free := r.next(t), r.next(t)
		if plugin.WireOp(done) != plugin.OpFrameDone || plugin.WireOp(free) != plugin.OpBufferFree {
			t.Fatalf("frame %d: ops %q then %q, want frame.done then buffer.free", seq, plugin.WireOp(done), plugin.WireOp(free))
		}
		if d, _ := plugin.ReadFrameDoneMsg(done); d.Seq != seq || d.Outcome != "drawn" {
			t.Fatalf("frame %d: frame.done %+v", seq, d)
		}
	}
	r.noViolation(t)
}

// A frame released before its outcome is known gets no credit until the outcome: presenting its
// buffer again in between is a violation (the obligations stay one per buffer).
func TestASurfaceCreditWaitsForItsFramesOutcome(t *testing.T) {
	r := newSurfaceRig(t)
	gen, ids := r.pool(t, rigGeometry)
	r.present(t, 1, gen, ids[0])
	h := r.take(t)
	h.f.Drop() // released, its outcome not yet known
	select {
	case m := <-r.msgs:
		t.Fatalf("op %q before the frame's outcome", plugin.WireOp(m))
	case <-time.After(100 * time.Millisecond):
	}
	r.present(t, 2, gen, ids[0])
	if why := r.violation(t); !strings.Contains(why, "isn't available") {
		t.Errorf("violation %q", why)
	}
}

// A frame whose seq doesn't rise is a violation, and so is a frame of a generation not given yet.
func TestASurfaceFrameOutOfOrderIsAViolation(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, r *surfaceRig, gen int, ids []int){
		"a repeated seq": func(t *testing.T, r *surfaceRig, gen int, ids []int) {
			r.present(t, 3, gen, ids[0])
			r.present(t, 3, gen, ids[1])
		},
		"a future gen": func(t *testing.T, r *surfaceRig, gen int, ids []int) { r.present(t, 1, gen+1, ids[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newSurfaceRig(t)
			gen, ids := r.pool(t, rigGeometry)
			send(t, r, gen, ids)
			r.violation(t)
		})
	}
}

// A frame of the generation a resize retired, still in transit, is answered "retired" and refused
// without a violation.
func TestASurfaceFrameOfARetiredGenerationIsAnsweredRetired(t *testing.T) {
	r := newSurfaceRig(t)
	gen, ids := r.pool(t, rigGeometry)
	bigger := rigGeometry
	bigger.W = 32
	r.pool(t, bigger)
	r.present(t, 1, gen, ids[0])
	d, err := plugin.ReadFrameDoneMsg(r.next(t))
	if err != nil || d.Outcome != "retired" {
		t.Fatalf("frame.done %+v, %v; want retired", d, err)
	}
	r.noViolation(t)
}

// Many resizes with no frame leave one generation's buffers mapped, not one per resize.
func TestManySurfaceResizesWithNoFramesStayBounded(t *testing.T) {
	r := newSurfaceRig(t)
	before := r.mappings(t)
	for i := range 20 {
		g := rigGeometry
		g.W = float32(16 + i)
		r.pool(t, g)
	}
	if n := r.mappings(t) - before; n > plugin.MaxPoolBuffers {
		t.Errorf("%d surface buffers mapped after 20 resizes, want at most %d", n, plugin.MaxPoolBuffers)
	}
	r.noViolation(t)
}

// Close destroys the idle buffers at once and a held one only at its release, which may come
// after Stream.Close: the owner ends then, and only then.
func TestASurfaceCloseDestroysEachBufferAfterItsLastHolder(t *testing.T) {
	r := newSurfaceRig(t)
	before := r.mappings(t)
	gen, ids := r.pool(t, rigGeometry)
	r.present(t, 1, gen, ids[0])
	h := r.take(t)
	r.owner.Close()
	surfaceEventually(t, "the idle buffers still mapped after Close", func() bool { return r.mappings(t) <= before+1 })
	if n := r.mappings(t) - before; n != 1 {
		t.Fatalf("%d buffers mapped after Close, want the held one alone", n)
	}
	select {
	case <-r.owner.Ended():
		t.Fatal("the owner ended while a frame still held a buffer")
	case <-time.After(50 * time.Millisecond):
	}
	h.f.Drop() // the positive control: the frame was held past Close
	p := surfaceWaitFor(t, "the owner did not end at the last release")
	defer p.stop()
	for ended := false; !ended; {
		select {
		case <-r.owner.Ended():
			ended = true
		case <-p.C():
			p.late()
		}
	}
	if n := r.mappings(t) - before; n != 0 {
		t.Errorf("%d buffers still mapped after the last release", n)
	}
}

// A plugin that never reads its side channel stalls the writer: the host closes it as stalled, and
// no release callback ever blocks the goroutine that drops the frame (the render path).
func TestAStalledSurfacePluginIsClosedAndNeverBlocksARelease(t *testing.T) {
	host, child, err := plugin.NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	plugFD, err := unix.Dup(int(child.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	sink := &fakeSink{auto: "drawn"}
	violated := make(chan string, 4)
	owner := newSurfaceOwner(host, sink, func(plugin.SurfaceInfo) {}, func(why string) { violated <- why })
	defer owner.Close()
	owner.Resize(rigGeometry)
	time.Sleep(50 * time.Millisecond)
	t.Setenv(plugin.SurfaceFDEnv, strconv.Itoa(plugFD))
	plug, _, err := plugin.OpenPluginSurfaceConn()
	if err != nil {
		t.Fatal(err)
	}
	defer plug.Close()
	// the plugin presents buffer 2 over and over without reading: each frame settles at once in the
	// sink, its frame.done and buffer.free pile up unread
	for seq := uint64(1); ; seq++ {
		if err := plug.Send(plugin.FrameMsg(plugin.SurfaceFrame{Seq: seq, Gen: 1, Buffer: 2})); err != nil {
			break
		}
		select {
		case why := <-violated:
			if why == "" {
				t.Fatalf("the plugin was closed without a reason")
			}
			return
		default:
		}
		if seq > 2_000_000 {
			t.Fatal("no stall after two million frames")
		}
	}
	p := surfaceWaitFor(t, "a plugin that never read was not closed")
	defer p.stop()
	for closed := false; !closed; {
		select {
		case <-violated:
			closed = true
		case <-p.C():
			p.late()
		}
	}
}

// Only a DMA-BUF is admitted as one: a regular file, a memfd, a pipe and a socket are refused before
// anything maps them, and a closed fd too.
func TestOnlyADMABufIsAdmittedAsOne(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notadmabuf")
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(1 << 22)
	defer f.Close()
	mfd, _ := unix.MemfdCreate("x", unix.MFD_CLOEXEC)
	defer unix.Close(mfd)
	var p [2]int
	unix.Pipe2(p[:], unix.O_CLOEXEC)
	defer unix.Close(p[0])
	defer unix.Close(p[1])
	s, _ := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	defer unix.Close(s[0])
	defer unix.Close(s[1])
	closed, _ := unix.Dup(mfd)
	unix.Close(closed)
	for name, fd := range map[string]int{"a regular file": int(f.Fd()), "a memfd": mfd, "a pipe": p[0], "a socket": s[0], "a closed fd": closed} {
		if why := admitDMABuf(fd, 1024, 1024, 0, 4096); !strings.Contains(why, "not a DMA-BUF") {
			t.Errorf("%s: %q, want it refused as not a DMA-BUF", name, why)
		}
	}
}

// While the plugin is busy, it acknowledges nothing: 30 resizes in a drag start no generation past
// the one it has, and its frame of that generation, 300 ms late, is admitted, with no violation.
func TestASurfaceResizeDragIsPacedByThePlugin(t *testing.T) {
	r := newSurfaceRig(t)
	gen, ids := r.poolUnacked(t, rigGeometry)
	for i := range 30 {
		g := rigGeometry
		g.W = float32(17 + i)
		r.owner.Resize(g)
	}
	time.Sleep(300 * time.Millisecond)
	r.present(t, 1, gen, ids[0])
	h := r.take(t)
	h.r.complete("drawn")
	h.f.Drop()
	if d, err := plugin.ReadFrameDoneMsg(r.nextOp(t, plugin.OpFrameDone)); err != nil || d.Outcome != "drawn" {
		t.Fatalf("frame.done %+v, %v: the late frame was not admitted", d, err)
	}
	r.noViolation(t)
}

// A plugin that never acknowledges is paced by the timeout alone: the next generation comes.
func TestASurfaceThatNeverAcknowledgesStillResizes(t *testing.T) {
	r := newSurfaceRig(t)
	r.poolUnacked(t, rigGeometry)
	g := rigGeometry
	g.W = 40
	start := time.Now()
	gen, _ := r.poolUnacked(t, g)
	if gen != 2 || time.Since(start) < surfaceAckTimeout/2 {
		t.Errorf("generation %d after %v: want 2, after about the ack timeout", gen, time.Since(start))
	}
}

// At the ceiling, with idle buffers, no traffic and no callbacks owed, a smaller resize still
// starts its generation: retiring the old one first frees what the new one needs.
func TestASaturatedSurfaceBudgetStillResizes(t *testing.T) {
	big := rigGeometry
	big.W, big.H = 64, 64
	one := int64(64 * 4 * 64)
	r := newSurfaceRigCeiling(t, 3*one) // the first pool fills it exactly
	before := r.mappings(t)
	r.pool(t, big)
	small := rigGeometry
	gen, _ := r.pool(t, small)
	if gen != 2 {
		t.Fatalf("generation %d, want 2", gen)
	}
	if n := r.mappings(t) - before; n != plugin.MaxPoolBuffers {
		t.Errorf("%d buffers mapped, want the new pool alone", n)
	}
	r.noViolation(t)
}

// The same with an old frame retained: its buffer stays mapped (and charged) until it is dropped.
func TestASaturatedSurfaceBudgetKeepsAHeldFrame(t *testing.T) {
	big := rigGeometry
	big.W, big.H = 64, 64
	one := int64(64 * 4 * 64)
	r := newSurfaceRigCeiling(t, 4*one) // a pool and the one held frame
	before := r.mappings(t)
	gen, ids := r.pool(t, big)
	r.present(t, 1, gen, ids[0])
	h := r.take(t)
	same := big
	same.W = 63.5 // another geometry of the same pixels (ceil 64): a new generation, the same charge
	r.pool(t, same)
	if n := r.mappings(t) - before; n != plugin.MaxPoolBuffers+1 {
		t.Fatalf("%d buffers mapped, want the new pool and the held one", n)
	}
	h.r.complete("drawn")
	h.f.Drop()
	surfaceEventually(t, "the held frame's buffer still mapped after its drop", func() bool { return r.mappings(t)-before == plugin.MaxPoolBuffers })
	if n := r.mappings(t) - before; n != plugin.MaxPoolBuffers {
		t.Errorf("%d buffers mapped after the held frame was dropped", n)
	}
	r.noViolation(t)
}

// A buffer.add that crossed a retirement is dropped: no acceptance credit comes for it, so a
// conforming SDK never presents it. A peer that presents it anyway (not a race a conforming SDK
// can reach) gets one "retired", from the bounded record, and a second is a violation.
func TestASurfaceAddCrossingARetirementGetsNoCredit(t *testing.T) {
	r := newSurfaceRig(t)
	gen, _ := r.pool(t, rigGeometry)
	bigger := rigGeometry
	bigger.W = 32
	r.pool(t, bigger)
	fd, _ := unix.MemfdCreate("x", unix.MFD_CLOEXEC)
	defer unix.Close(fd)
	add := map[string]any{"op": plugin.OpBufferAdd, "gen": gen, "id": 1, "kind": "dmabuf", "format": "rgba8",
		"w": 16, "h": 8, "modifier": 0, "planes": []any{map[string]any{"offset": 0, "pitch": 64}}}
	if err := r.plug.Send(add, fd); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-r.msgs:
		t.Fatalf("op %q for a dropped add: it gets no credit", plugin.WireOp(m))
	case <-time.After(100 * time.Millisecond):
	}
	r.noViolation(t)
	r.present(t, 1, gen, 1)
	d, err := plugin.ReadFrameDoneMsg(r.next(t))
	if err != nil || d.Outcome != "retired" {
		t.Fatalf("frame.done %+v, %v; want retired", d, err)
	}
	r.present(t, 2, gen, 1)
	r.violation(t) // its one answer is given
}
