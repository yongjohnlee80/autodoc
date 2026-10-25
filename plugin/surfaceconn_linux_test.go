package plugin

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/msgpack"
	"golang.org/x/sys/unix"
)

// pair is both ends of a side channel, as the host and the plugin hold them.
func pair(t *testing.T) (host, plug *SurfaceConn) {
	t.Helper()
	h, child, err := NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Dup(int(child.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	p := newSurfaceConn(fd)
	t.Cleanup(func() { h.Close(); p.Close() })
	return h, p
}

// surfaceOn is a Surface on plug, closed before the cell's fds close (its reader, then its acks, are
// done first): a reader that outlived its cell would take the next cell's datagrams and fds.
func surfaceOn(t *testing.T, plug *SurfaceConn) *Surface {
	s := newSurface(plug)
	t.Cleanup(s.Close)
	return s
}

// onHost runs work, a cell's sends and receives on host, on its own goroutine, done before the
// cell's cleanups close any fd: the cleanup shuts host down, which ends a blocked send or receive,
// and waits for work to return. Work that outlived its cell would send to, or take from, whatever
// conn the next cell opened on its fd number.
func onHost(t *testing.T, host *SurfaceConn, work func()) {
	done := make(chan struct{})
	t.Cleanup(func() { host.Shutdown(); <-done })
	go func() { defer close(done); work() }()
}

// openFDs is how many descriptors this process holds.
func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

func memfd(t *testing.T) int {
	t.Helper()
	fd, err := unix.MemfdCreate("test", unix.MFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

// A message and its fds cross whole: the guard owns the received fds, and Take hands them over.
func TestASurfaceMessageCarriesItsFDs(t *testing.T) {
	h, p := pair(t)
	a, b := memfd(t), memfd(t)
	defer unix.Close(a)
	defer unix.Close(b)
	if err := h.Send(BuffersMsg(1, nil), a, b); err != nil {
		t.Fatal(err)
	}
	m, g, err := p.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if WireOp(m) != OpBuffers || g.Len() != 2 {
		t.Fatalf("op %q with %d fds", WireOp(m), g.Len())
	}
	fd := g.Take(0)
	if fd < 0 || g.Take(0) != -1 {
		t.Fatal("Take did not hand fd 0 over exactly once")
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Errorf("a received fd is not close-on-exec (flags %d, %v)", flags, err)
	}
	unix.Close(fd)
	g.CloseAll()
}

// Every rejection closes every fd the message carried: an oversized datagram, a map that doesn't
// decode, and a guard its handler refused. The positive control is a message whose fds are taken.
func TestARejectedSurfaceMessageLeaksNoFD(t *testing.T) {
	h, p := pair(t)
	raw := func(b []byte, fds ...int) {
		t.Helper()
		if err := unix.Sendmsg(h.fd, b, unix.UnixRights(fds...), nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(fd int){
		"oversized": func(fd int) { raw(make([]byte, MaxSurfaceDatagram+64), fd) },
		"not a map": func(fd int) { raw([]byte{0xc3}, fd) }, // msgpack true
		"too many fds": func(int) {
			fds := []int{memfd(t), memfd(t), memfd(t), memfd(t), memfd(t)}
			b, _ := msgpack.Marshal(map[string]any{"op": "x"})
			raw(b, fds...)
			for _, fd := range fds {
				unix.Close(fd)
			}
		},
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			before := openFDs(t)
			fd := memfd(t)
			send(fd)
			unix.Close(fd)
			if _, _, err := p.Recv(); err == nil {
				t.Fatal("the message was accepted")
			}
			if after := openFDs(t); after != before {
				t.Errorf("%d fds before, %d after", before, after)
			}
		})
	}
	t.Run("refused by its handler", func(t *testing.T) {
		before := openFDs(t)
		fd := memfd(t)
		if err := h.Send(map[string]any{"op": "unknown"}, fd); err != nil {
			t.Fatal(err)
		}
		unix.Close(fd)
		_, g, err := p.Recv()
		if err != nil {
			t.Fatal(err)
		}
		g.CloseAll()
		if after := openFDs(t); after != before {
			t.Errorf("%d fds before, %d after", before, after)
		}
	})
}

// A frame and its outcome round-trip, its gen and seq kept.
func TestSurfaceOpsRoundTrip(t *testing.T) {
	f, err := ReadFrameMsg(roundTrip(t, FrameMsg(SurfaceFrame{Seq: 7, Gen: 2, Buffer: 4, InputT: 99})))
	if err != nil || f.Seq != 7 || f.Gen != 2 || f.Buffer != 4 || f.InputT != 99 {
		t.Fatalf("frame %+v, %v", f, err)
	}
	d, err := ReadFrameDoneMsg(roundTrip(t, FrameDoneMsg(SurfaceFrameDone{Seq: 7, Outcome: "drawn", WhenNS: 5})))
	if err != nil || d.Seq != 7 || d.Outcome != "drawn" || d.WhenNS != 5 {
		t.Fatalf("frame.done %+v, %v", d, err)
	}
	if _, err := ReadFrameMsg(map[string]any{"op": OpFrame, "seq": int64(1), "buffer": int64(2)}); err == nil {
		t.Error("a frame without its gen was read")
	}
}

func roundTrip(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := msgpack.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	v, err := msgpack.Unmarshal(b, surfaceLimits)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

// A signal never ends a timed send: the host's sends wait on a full socket while signals (as its
// children's SIGCHLDs; here SIGURG, at the sending thread) interrupt them, and every one goes, in order.
func TestASignalNeverEndsATimedSend(t *testing.T) {
	host, plug := pair(t)
	const count = 2000
	tid, sendErr := make(chan int, 1), make(chan error, 1)
	onHost(t, host, func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid <- unix.Gettid()
		for i := range count {
			if err := host.Send(FrameDoneMsg(SurfaceFrameDone{Seq: uint64(i + 1), Outcome: "drawn"})); err != nil {
				sendErr <- fmt.Errorf("send %d: %w", i+1, err)
				host.Shutdown() // the reader below finds the end, not a datagram that never comes
				return
			}
		}
		sendErr <- nil
	})
	stop := make(chan struct{})
	defer close(stop)
	var signals atomic.Int64
	go func(tid int) {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if unix.Tgkill(os.Getpid(), tid, unix.SIGURG) == nil {
				signals.Add(1)
			}
			time.Sleep(20 * time.Microsecond)
		}
	}(<-tid)
	time.Sleep(50 * time.Millisecond) // the socket fills: the sends wait, and are signalled
	for i := range count {
		if i%8 == 0 {
			time.Sleep(200 * time.Microsecond) // a slow reader: the sends keep waiting
		}
		m, g, err := plug.Recv()
		if err != nil {
			t.Fatalf("a signal ended a send: %v (at recv %d: %v)", <-sendErr, i+1, err)
		}
		g.CloseAll()
		if d, err := ReadFrameDoneMsg(m); err != nil || d.Seq != uint64(i+1) {
			t.Fatalf("datagram %d: %+v, %v", i+1, d, err)
		}
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("a signal ended a send: %v", err)
	}
	if signals.Load() == 0 {
		t.Fatal("no signal was sent: the interruption went unexercised")
	}
}

// Interruptions without end never hold a timed send past its deadline: it is checked before each
// retry, and the send says it passed.
func TestEndlessInterruptionsEndATimedSendAtItsDeadline(t *testing.T) {
	host, _ := pair(t)
	host.timeout = 50 * time.Millisecond
	var interrupting atomic.Bool
	interrupting.Store(true)
	sysSendmsg = func(fd int, p, oob []byte, to unix.Sockaddr, flags int) error {
		if interrupting.Load() {
			return unix.EINTR
		}
		return unix.Sendmsg(fd, p, oob, to, flags)
	}
	done := make(chan error, 1)
	t.Cleanup(func() { interrupting.Store(false); <-done; sysSendmsg = unix.Sendmsg }) // the send ends first
	start := time.Now()
	go func() { done <- host.Send(FrameDoneMsg(SurfaceFrameDone{Seq: 1, Outcome: "drawn"})) }()
	select {
	case err := <-done:
		done <- err // for the cleanup
		if !errors.Is(err, unix.EAGAIN) || time.Since(start) > time.Second {
			t.Fatalf("after %v: %v, want the deadline's EAGAIN at about 50ms", time.Since(start), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interruptions held a timed send past its deadline") // the cleanup lets it go
	}
}
