package plugin

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// addHost is a host's end for the registration cells: it reads the plugin's ops (but its acks),
// closing the fds they bring, and answers as the cell says.
type addHost struct {
	t    *testing.T
	host *SurfaceConn
	s    *Surface
	ops  chan addOp
	mark uint64
	adds sync.WaitGroup // the cell's AddBuffer calls: each has returned before the next cell starts
}

type addOp struct {
	m   map[string]any
	fds int
}

var dmaKinds = []SurfaceKind{{Kind: "memory", Format: "rgba8"}, {Kind: "dmabuf", Format: "rgba8", MaxW: 64, MaxH: 64}}

func newAddHost(t *testing.T) *addHost {
	t.Helper()
	host, plug := pair(t)
	h := &addHost{t: t, host: host, ops: make(chan addOp, 64)}
	t.Cleanup(h.adds.Wait) // after the surface's Close, which answers any still waiting
	h.s = surfaceOn(t, plug)
	onHost(t, host, func() {
		for {
			m, g, err := host.Recv()
			if err != nil {
				return
			}
			n := g.Len()
			g.CloseAll()
			if WireOp(m) != OpSurfaceAck {
				h.ops <- addOp{m, n}
			}
		}
	})
	return h
}

// generation makes gen current, offering DMA-BUFs, with one memory buffer.
func (h *addHost) generation(gen int) {
	h.t.Helper()
	fd := memfd(h.t)
	if err := unix.Ftruncate(fd, 4096); err != nil {
		h.t.Fatal(err)
	}
	spec := SurfaceBufferSpec{ID: 100 * gen, Kind: "memory", Format: "rgba8", W: 1, H: 1, Pitch: 4, Size: 4096}
	if err := h.host.Send(BuffersMsg(gen, []SurfaceBufferSpec{spec}), fd); err != nil {
		h.t.Fatal(err)
	}
	unix.Close(fd)
	h.s.setInfo(SurfaceInfo{Gen: gen, W: 16, H: 8, Scale: 1, Kinds: dmaKinds})
	h.settled()
}

// next is the plugin's next op, which must be op.
func (h *addHost) next(op string) addOp {
	h.t.Helper()
	select {
	case o := <-h.ops:
		if WireOp(o.m) != op {
			h.t.Fatalf("the plugin sent %v, want %s", o.m, op)
		}
		return o
	// NOTE: a bound on a failing run alone: the op is already sent, or its sender is running.
	case <-time.After(10 * time.Second):
		h.t.Fatalf("the plugin never sent %s", op)
	}
	return addOp{}
}

func (h *addHost) send(m map[string]any) {
	h.t.Helper()
	if err := h.host.Send(m); err != nil {
		h.t.Fatal(err)
	}
}

// settled returns once the plugin's reader has taken everything the host sent before: a frame's
// outcome sent now is read after it, and comes out of Done.
func (h *addHost) settled() {
	h.t.Helper()
	h.mark++
	h.send(FrameDoneMsg(SurfaceFrameDone{Seq: 1<<40 + h.mark, Outcome: "mark"}))
	for {
		select {
		case d := <-h.s.Done():
			if d.Seq == 1<<40+h.mark {
				return
			}
		case <-time.After(10 * time.Second):
			h.t.Fatal("the plugin's reader never took the host's ops")
		}
	}
}

// add starts a registration for gen on its own goroutine, and returns its answer and the fd
// registered, the cell's own.
func (h *addHost) add(gen int) (<-chan addAnswer, int) {
	h.t.Helper()
	fd := memfd(h.t)
	h.t.Cleanup(func() { unix.Close(fd) })
	ans := make(chan addAnswer, 1)
	h.adds.Go(func() {
		b, err := h.s.AddBuffer(context.Background(), SurfaceDMABuf{FD: fd, Gen: gen, W: 16, H: 8, Pitch: 64, Format: "rgba8"})
		ans <- addAnswer{b, err}
	})
	return ans, fd
}

type addAnswer struct {
	b   *SurfaceBuffer
	err error
}

func (h *addHost) answer(ans <-chan addAnswer) addAnswer {
	h.t.Helper()
	select {
	case a := <-ans:
		return a
	case <-time.After(10 * time.Second):
		h.t.Fatal("AddBuffer never returned")
	}
	return addAnswer{}
}

// unanswered fails if the registration has returned.
func (h *addHost) unanswered(ans <-chan addAnswer, when string) {
	h.t.Helper()
	select {
	case a := <-ans:
		h.t.Fatalf("%s, AddBuffer returned %+v, %v; want it still waiting for the host's answer", when, a.b, a.err)
	default:
	}
}

// waits fails unless AddedBuffer waits: nothing is free to grant, and something may yet be.
func (h *addHost) waits(when string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	b, err := h.s.AddedBuffer(ctx)
	if err == nil {
		h.t.Fatalf("%s, AddedBuffer granted buffer %d; want it to wait", when, b.ID)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		h.t.Fatalf("%s, AddedBuffer: %v; want it to wait", when, err)
	}
}

func (h *addHost) granted(when string) *SurfaceBuffer {
	h.t.Helper()
	// NOTE: a bound on a failing run alone: the buffer is free, so AddedBuffer answers at once.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := h.s.AddedBuffer(ctx)
	if err != nil {
		h.t.Fatalf("%s, AddedBuffer: %v; want a buffer", when, err)
	}
	return b
}

func idOf(t *testing.T, o addOp) int {
	t.Helper()
	id, ok := num(o.m, "id")
	if !ok {
		t.Fatalf("a buffer.add without an id: %v", o.m)
	}
	return id
}

// A registered buffer can't be presented until the host accepts it: AddBuffer waits and
// AddedBuffer grants nothing. Its acceptance is the first buffer.free naming it; from then on it
// is granted, and after a frame, again only at its credit.
func TestARegisteredBufferIsGrantedOnlyAfterItsAcceptance(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	ans, _ := h.add(1)
	o := h.next(OpBufferAdd)
	planes, _ := o.m["planes"].([]any)
	plane, _ := (append(planes, nil))[0].(map[string]any)
	pitch, _ := num(plane, "pitch")
	if gen, _ := num(o.m, "gen"); gen != 1 || idOf(t, o) != 1 || o.fds != 1 || str(o.m, "kind") != "dmabuf" ||
		str(o.m, "format") != "rgba8" || len(planes) != 1 || pitch != 64 {
		t.Fatalf("buffer.add %v with %d fds; want generation 1, id 1, one rgba8 dmabuf plane of pitch 64, one fd", o.m, o.fds)
	}
	h.settled()
	h.unanswered(ans, "before the host answered")
	h.waits("before the host answered")

	h.send(BufferFreeMsg(1))
	a := h.answer(ans)
	if a.err != nil || a.b == nil || a.b.ID != 1 || a.b.Kind != "dmabuf" || a.b.Pix != nil {
		t.Fatalf("the accepted registration: %+v, %v; want buffer 1, a dmabuf without pixels", a.b, a.err)
	}
	b := h.granted("accepted")
	if b != a.b {
		t.Fatalf("AddedBuffer granted %+v, want the registered buffer", b)
	}
	if _, err := h.s.Present(b, image.Rectangle{}, 0); err != nil {
		t.Fatal(err)
	}
	if f, err := ReadFrameMsg(h.next(OpFrame).m); err != nil || f.Buffer != 1 || f.Gen != 1 {
		t.Fatalf("the frame %+v, %v; want buffer 1 of generation 1", f, err)
	}
	h.waits("presented and not credited")
	h.send(BufferFreeMsg(1))
	if b := h.granted("credited"); b.ID != 1 {
		t.Fatalf("granted %d after the credit", b.ID)
	}
}

// A refusal, for size or for budget, is AddBuffer's answer, not an error of the surface: the
// plugin draws into the memory buffers Buffer still grants. The refused id is done with: a retry
// registers a new one, and an acceptance naming the refused one, forged or late, grants nothing.
func TestARefusedRegistrationFallsBackToMemoryAndRetriesUnderANewID(t *testing.T) {
	for _, reason := range []string{"size", "budget"} {
		t.Run(reason, func(t *testing.T) {
			h := newAddHost(t)
			h.generation(1)
			ans, _ := h.add(1)
			id := idOf(t, h.next(OpBufferAdd))
			h.send(map[string]any{"op": OpBufferRefused, "id": id, "reason": reason})
			var refused *SurfaceRefused
			if a := h.answer(ans); !errors.As(a.err, &refused) || refused.Reason != reason {
				t.Fatalf("the refused registration: %+v, %v; want a SurfaceRefused for %s", a.b, a.err, reason)
			}
			if _, err := h.s.AddedBuffer(context.Background()); !errors.Is(err, ErrSurfaceBuffers) {
				t.Errorf("AddedBuffer with nothing registered: %v, want ErrSurfaceBuffers", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if b, err := h.s.Buffer(ctx); err != nil || b.Kind != "memory" || len(b.Pix) == 0 {
				t.Fatalf("after the refusal Buffer granted %+v, %v; want a memory buffer", b, err)
			}

			retry, _ := h.add(1)
			again := idOf(t, h.next(OpBufferAdd))
			if again <= id || again%2 != 1 {
				t.Fatalf("the retry registered id %d after the refused %d; want a new odd id above it", again, id)
			}
			h.send(BufferFreeMsg(id)) // an acceptance of the refused id
			h.settled()
			h.unanswered(retry, "after an acceptance naming the refused id")
			h.waits("after an acceptance naming the refused id")
			h.send(BufferFreeMsg(again))
			if a := h.answer(retry); a.err != nil || a.b.ID != again {
				t.Fatalf("the retry: %+v, %v; want buffer %d accepted", a.b, a.err, again)
			}
		})
	}
}

// An acceptance changes nothing unless it answers a registration still waiting: one repeated, and
// one naming an id never registered, grant no buffer, whether the registered one is free or being
// drawn into.
func TestADuplicateOrForgedAcceptanceChangesNothing(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	ans, _ := h.add(1)
	id := idOf(t, h.next(OpBufferAdd))
	h.send(BufferFreeMsg(id))
	h.answer(ans)

	h.send(BufferFreeMsg(id))     // repeated while the buffer is free
	h.send(BufferFreeMsg(id + 6)) // never registered
	h.settled()
	b := h.granted("accepted once")
	h.send(BufferFreeMsg(id)) // repeated while the plugin draws into it
	h.send(BufferFreeMsg(id + 6))
	h.settled()
	h.waits("with the one buffer being drawn into")
	h.s.mu.Lock()
	n := len(h.s.added)
	h.s.mu.Unlock()
	if n != 1 {
		t.Errorf("%d registered buffers, want the one", n)
	}
	if _, err := h.s.Present(b, image.Rectangle{}, 0); err != nil {
		t.Fatal(err)
	}
}

// A registration the host hasn't answered when its generation ends is given up: AddBuffer says
// so, and the host's acceptance, arriving afterwards, credits nothing. Ids rise across
// generations, so that late acceptance can't name the new generation's registration either: the
// new one stays unavailable until its own.
func TestAnAcceptanceAfterItsGenerationEndedCreditsNothing(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	old, _ := h.add(1)
	oldID := idOf(t, h.next(OpBufferAdd))
	h.generation(2)
	if a := h.answer(old); !errors.Is(a.err, ErrSurfaceRetired) {
		t.Fatalf("the registration whose generation ended: %+v, %v; want ErrSurfaceRetired", a.b, a.err)
	}
	if _, err := h.s.AddBuffer(context.Background(), SurfaceDMABuf{FD: 0, Gen: 1, W: 16, H: 8, Pitch: 64, Format: "rgba8"}); !errors.Is(err, ErrSurfaceRetired) {
		t.Errorf("a registration for the ended generation: %v, want ErrSurfaceRetired", err)
	}

	next, _ := h.add(2)
	nextID := idOf(t, h.next(OpBufferAdd))
	if nextID <= oldID {
		t.Fatalf("generation 2's registration took id %d, generation 1's had %d; want ids rising across generations", nextID, oldID)
	}
	h.send(BufferFreeMsg(oldID)) // the old registration's acceptance, late
	h.settled()
	h.unanswered(next, "after the old generation's acceptance")
	h.waits("after the old generation's acceptance")
	h.send(BufferFreeMsg(nextID))
	if a := h.answer(next); a.err != nil || a.b.ID != nextID || a.b.Gen != 2 {
		t.Fatalf("generation 2's registration: %+v, %v; want buffer %d accepted", a.b, a.err, nextID)
	}
	// and an accepted buffer ends with its generation: the next has none until it registers them
	h.generation(3)
	h.send(BufferFreeMsg(nextID))
	h.settled()
	if _, err := h.s.AddedBuffer(context.Background()); !errors.Is(err, ErrSurfaceBuffers) {
		t.Errorf("AddedBuffer in a generation with nothing registered: %v, want ErrSurfaceBuffers", err)
	}
}

// The fd registered stays the caller's on every path: accepted, refused, given up at its
// generation's end, never sent (a kind not offered, too many, the host gone). The SDK neither
// closes it nor keeps a descriptor of its own.
func TestARegistrationNeitherClosesNorKeepsItsFD(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	open := func(fd int) bool { _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); return err == nil }
	var fds []int
	before := -1
	check := func(path string) {
		t.Helper()
		h.settled()
		for _, fd := range fds {
			if !open(fd) {
				t.Fatalf("%s: the caller's fd %d was closed", path, fd)
			}
		}
		// the cell's own fds are the only ones opened since
		if n := openFDs(t) - len(fds); before >= 0 && n != before {
			t.Fatalf("%s: %d descriptors beyond the caller's, %d before: the SDK keeps one, or closed one", path, n, before)
		} else if before < 0 {
			before = n
		}
	}
	check("before any registration")

	ans, fd := h.add(1)
	fds = append(fds, fd)
	accepted := idOf(t, h.next(OpBufferAdd))
	h.send(BufferFreeMsg(accepted))
	if a := h.answer(ans); a.err != nil {
		t.Fatal(a.err)
	}
	check("accepted")

	ans, fd = h.add(1)
	fds = append(fds, fd)
	h.send(map[string]any{"op": OpBufferRefused, "id": idOf(t, h.next(OpBufferAdd)), "reason": "budget"})
	if a := h.answer(ans); a.err == nil {
		t.Fatal("the refusal never reached AddBuffer")
	}
	check("refused")

	fd = memfd(t)
	t.Cleanup(func() { unix.Close(fd) })
	fds = append(fds, fd)
	d := SurfaceDMABuf{FD: fd, Gen: 1, W: 16, H: 8, Pitch: 64, Format: "bgra8"}
	if _, err := h.s.AddBuffer(context.Background(), d); !errors.Is(err, ErrSurfaceKind) {
		t.Fatalf("a kind the window doesn't offer: %v, want ErrSurfaceKind", err)
	}
	check("not offered")

	a2, fd2 := h.add(1)
	a3, fd3 := h.add(1)
	fds = append(fds, fd2, fd3)
	h.next(OpBufferAdd)
	h.next(OpBufferAdd)
	d.Format = "rgba8"
	if _, err := h.s.AddBuffer(context.Background(), d); !errors.Is(err, ErrSurfaceBuffers) {
		t.Fatalf("a registration beyond %d: %v, want ErrSurfaceBuffers", MaxAddedBuffers, err)
	}
	check("too many")

	h.generation(2)
	for _, ans := range []<-chan addAnswer{a2, a3} {
		if a := h.answer(ans); !errors.Is(a.err, ErrSurfaceRetired) {
			t.Fatalf("given up at its generation's end: %v", a.err)
		}
	}
	check("given up")
	select {
	case o := <-h.ops:
		t.Fatalf("the plugin sent %v for a registration it should never have sent", o.m)
	default:
	}

	h.host.Shutdown()
	<-h.s.gone
	d.Gen = 2
	if _, err := h.s.AddBuffer(context.Background(), d); err == nil {
		t.Fatal("a registration after the host went was taken")
	}
	for _, fd := range fds {
		if !open(fd) {
			t.Fatalf("the host gone: the caller's fd %d was closed", fd)
		}
	}
}

// A registration whose caller stopped waiting is abandoned: the caller never learned its id, so it
// couldn't tell which of its buffers a grant would be. The host's acceptance, arriving afterwards,
// grants nothing; and since the host holds the buffer, it keeps its place among the generation's
// three, so a registration that would be the host's fourth is still never sent.
func TestARegistrationItsCallerGaveUpOnIsNeverGranted(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	fd := memfd(t)
	t.Cleanup(func() { unix.Close(fd) })
	d := SurfaceDMABuf{FD: fd, Gen: 1, W: 16, H: 8, Pitch: 64, Format: "rgba8"}
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	h.adds.Go(func() { _, err := h.s.AddBuffer(ctx, d); gaveUp <- err })
	given := idOf(t, h.next(OpBufferAdd)) // sent, and not answered
	cancel()
	select {
	case err := <-gaveUp:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AddBuffer, its ctx cancelled before the answer: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AddBuffer never returned at its ctx's end")
	}
	h.send(BufferFreeMsg(given)) // the host accepted it all the same
	h.settled()
	if b, err := h.s.AddedBuffer(context.Background()); !errors.Is(err, ErrSurfaceBuffers) {
		t.Fatalf("after the abandoned registration's acceptance AddedBuffer answered %+v, %v; want ErrSurfaceBuffers: nothing the plugin knows is registered", b, err)
	}

	for range 2 { // the generation's other two
		ans, _ := h.add(1)
		h.send(BufferFreeMsg(idOf(t, h.next(OpBufferAdd))))
		if a := h.answer(ans); a.err != nil {
			t.Fatal(a.err)
		}
	}
	// NOTE: a bound on a failing run alone: refused before it is sent, AddBuffer answers at once.
	fourth, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if _, err := h.s.AddBuffer(fourth, d); !errors.Is(err, ErrSurfaceBuffers) {
		t.Fatalf("a registration the host would count its fourth: %v, want ErrSurfaceBuffers, unsent", err)
	}
	select {
	case o := <-h.ops:
		t.Fatalf("the plugin sent %v: the host's fourth DMA-BUF of the generation, a violation", o.m)
	default:
	}
	for range 2 {
		if b := h.granted("with two registered"); b.ID == given {
			t.Fatalf("AddedBuffer granted the abandoned registration's buffer %d", given)
		}
	}
	h.waits("with both registered buffers being drawn into")

	h.generation(2) // the count ends with the generation
	ans, _ := h.add(2)
	h.next(OpBufferAdd)
	h.unanswered(ans, "in the next generation")
}

// A registration answered between its send and its wait is not lost: the answer is kept for it.
func TestAnAnswerBeforeAddBufferWaitsIsItsAnswer(t *testing.T) {
	h := newAddHost(t)
	h.generation(1)
	addSent = func(id int) {
		h.send(BufferFreeMsg(id))
		h.settled()
	}
	t.Cleanup(func() { addSent = nil })
	ans, _ := h.add(1)
	if a := h.answer(ans); a.err != nil || a.b == nil {
		t.Fatalf("answered at once: %+v, %v; want it accepted", a.b, a.err)
	}
}
