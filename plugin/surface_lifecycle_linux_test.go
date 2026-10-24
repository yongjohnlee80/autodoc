package plugin

import (
	"context"
	"image"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A full Done queue never stops the reader, whatever a consumer does between the reader's checks:
// with a consumer draining it in bursts, every one of 100,000 outcomes is taken off the socket.
func TestAFullDoneQueueNeverStopsTheReader(t *testing.T) {
	host, plug := pair(t)
	s := newSurface(plug)
	stop := make(chan struct{})
	defer close(stop)
	drained := atomic.Int64{}
	go func() { // drains the queue whole whenever it fills: the race a blocking discard loses
		for {
			select {
			case <-stop:
				return
			default:
			}
			if len(s.done) == cap(s.done) {
				for range cap(s.done) {
					select {
					case <-s.done:
						drained.Add(1)
					default:
					}
				}
			} else {
				runtime.Gosched()
			}
		}
	}()
	const count = 100_000
	var sent atomic.Int64
	go func() {
		for i := range count {
			if host.Send(FrameDoneMsg(SurfaceFrameDone{Seq: uint64(i + 1), Outcome: "drawn"})) != nil {
				return
			}
			sent.Add(1)
		}
	}()
	// a stopped reader is a stall: the socket fills and the sends stop (its pace, as -race's, is not)
	for last, since := int64(-1), time.Now(); sent.Load() != count; time.Sleep(10 * time.Millisecond) {
		if n := sent.Load(); n != last {
			last, since = n, time.Now()
		} else if time.Since(since) > 3*time.Second {
			t.Fatalf("the reader stopped taking outcomes after %d of %d", n, count)
		}
	}
	if drained.Load() == 0 {
		t.Fatal("the consumer never drained a full queue: the race went unexercised")
	}
}

// Presents from many goroutines on the real socket arrive with seqs rising, round after round.
func TestConcurrentPresentsNeverInvertOnTheSocket(t *testing.T) {
	host, plug := pair(t)
	s := newSurface(plug)
	const rounds = 2000
	observed := make(chan uint64, 3*rounds)
	go func() {
		for range 3 * rounds {
			m, g, err := host.Recv()
			if err != nil {
				return
			}
			g.CloseAll()
			if f, err := ReadFrameMsg(m); err == nil {
				observed <- f.Seq
			}
		}
	}()
	var last uint64
	for range rounds {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := s.Present(&SurfaceBuffer{ID: 2 + 2*i, Gen: 1, drawing: true}, image.Rectangle{}, 0); err != nil {
					t.Error(err)
				}
			}()
		}
		close(start)
		wg.Wait()
		for range 3 {
			n := <-observed
			if n <= last {
				t.Fatalf("seq %d arrived after %d", n, last)
			}
			last = n
		}
	}
}

// installed is a surface with one generation of n memory buffers from host, waited for.
func installed(t *testing.T, host *SurfaceConn, s *Surface, gen, n int) {
	t.Helper()
	var specs []SurfaceBufferSpec
	var fds []int
	for i := range n {
		fd := memfd(t)
		if err := unix.Ftruncate(fd, 4096); err != nil {
			t.Fatal(err)
		}
		specs = append(specs, SurfaceBufferSpec{ID: 100*gen + 2*(i+1), Kind: "memory", Format: "rgba8", W: 1, H: 1, Pitch: 4, Size: 4096})
		fds = append(fds, fd)
	}
	if err := host.Send(BuffersMsg(gen, specs), fds...); err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		unix.Close(fd)
	}
	s.setInfo(SurfaceInfo{Gen: gen, W: 1, H: 1, Scale: 1})
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		ready := s.cur == gen
		s.mu.Unlock()
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation %d never installed", gen)
		}
		time.Sleep(time.Millisecond)
	}
}

// surfaceMaps is how many memfd mappings this process holds.
func surfaceMaps(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "memfd:test")
}

// After the host goes, the surface grants no buffer and keeps no mapping, but for one being drawn,
// which goes at its Present. The positive control: before, Buffer grants one.
func TestASurfaceGrantsNothingAndKeepsNoMappingOnceTheHostGoes(t *testing.T) {
	host, plug := pair(t)
	before := surfaceMaps(t)
	s := newSurface(plug)
	installed(t, host, s, 1, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	drawn, err := s.Buffer(ctx)
	if err != nil {
		t.Fatalf("the control: no buffer before the host went: %v", err)
	}
	host.Close()
	select {
	case <-s.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the surface never saw the host go")
	}
	if _, err := s.Buffer(ctx); err != ErrSurfaceClosed {
		t.Errorf("Buffer after the host went: %v, want ErrSurfaceClosed", err)
	}
	if n := surfaceMaps(t) - before; n != 1 {
		t.Errorf("%d mappings kept, want the one being drawn", n)
	}
	s.Present(drawn, image.Rectangle{}, 0)
	if n := surfaceMaps(t) - before; n != 0 {
		t.Errorf("%d mappings kept after the drawn one was presented", n)
	}
}

// A generation's half that a newer one overtook is reclaimed, not kept pending.
func TestASupersededGenerationsHalfIsReclaimed(t *testing.T) {
	host, plug := pair(t)
	before := surfaceMaps(t)
	s := newSurface(plug)
	fd := memfd(t)
	unix.Ftruncate(fd, 4096)
	host.Send(BuffersMsg(1, []SurfaceBufferSpec{{ID: 2, Kind: "memory", Format: "rgba8", W: 1, H: 1, Pitch: 4, Size: 4096}}), fd)
	unix.Close(fd)
	time.Sleep(50 * time.Millisecond) // gen 1's buffers wait for a plugin.surface that never comes
	installed(t, host, s, 2, 1)
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 0 {
		t.Errorf("%d generations still pending after a newer installed", pending)
	}
	if n := surfaceMaps(t) - before; n != 1 {
		t.Errorf("%d mappings, want the current generation's one", n)
	}
	s.Close()
}

// Close ends the surface whatever the host does: Buffer refuses, every mapping goes, and it
// returns although the host's end stays open and silent.
func TestASurfaceCloseNeedsNothingFromTheHost(t *testing.T) {
	host, plug := pair(t)
	_ = host
	before := surfaceMaps(t)
	s := newSurface(plug)
	installed(t, host, s, 1, 3)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on the host")
	}
	if _, err := s.Buffer(context.Background()); err != ErrSurfaceClosed {
		t.Errorf("Buffer after Close: %v", err)
	}
	if n := surfaceMaps(t) - before; n != 0 {
		t.Errorf("%d mappings kept after Close", n)
	}
}

// A presented buffer is another caller's once the host credits it, even before its Present
// returns: the presenter never touches it after the send, so its new holder's pin and mapping stay.
func TestAnEarlyCreditLeavesTheBuffersNewHolderItsPin(t *testing.T) {
	s, _, _ := fakeHost(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := s.Buffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var b *SurfaceBuffer
	presentSent = func() { b, err = s.Buffer(ctx) } // the credit came at once: B holds it, A not returned
	t.Cleanup(func() { presentSent = nil })
	if _, err := s.Present(a, image.Rectangle{}, 0); err != nil {
		t.Fatal(err)
	}
	if err != nil || b != a {
		t.Fatalf("the credit never handed the buffer on: %v", err)
	}
	s.Close() // every mapping not being drawn into goes
	s.mu.Lock()
	drawing, mapped := b.drawing, len(b.Pix)
	s.mu.Unlock()
	if !drawing || mapped == 0 {
		t.Fatalf("A's Present released B's buffer: drawing %v, mapping %d bytes", drawing, mapped)
	}
	b.Pix[0] = 0xff // B still draws into it
}
