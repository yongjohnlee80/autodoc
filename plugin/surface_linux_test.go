package plugin

import (
	"context"
	"image"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The SDK makes its side channel close-on-exec as it opens it: a process the plugin starts doesn't
// inherit it. The positive control is the same fd, inherited before.
func TestAPluginsChildDoesNotInheritTheSideChannel(t *testing.T) {
	_, child, err := NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	fd := int(child.Fd())
	probe := func() string {
		out, _ := exec.Command("/bin/sh", "-c", "test -e /proc/self/fd/"+strconv.Itoa(fd)+" && echo open || echo closed").Output()
		return string(out)
	}
	// fd 3 in a plugin arrives inheritable, as ExtraFiles hands it: the same state, here
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if got := probe(); got != "open\n" {
		t.Fatalf("the control: an inheritable fd is %q in a child", got)
	}
	t.Setenv(SurfaceFDEnv, strconv.Itoa(fd))
	conn, ok, err := OpenPluginSurfaceConn()
	if err != nil || !ok {
		t.Fatal(err)
	}
	_ = conn
	if got := probe(); got != "closed\n" {
		t.Errorf("a child of the plugin sees the side channel %q", got)
	}
}

// Consecutive moves coalesce to the latest and scrolls sum, while a press ends the run.
func TestSurfacePointerMovesCoalesceAndScrollsSum(t *testing.T) {
	q := newQueue()
	push := func(p SurfacePointer) { q.push(MethodSurfacePointer, SurfacePointerParams(p)) }
	push(SurfacePointer{Kind: "move", X: 1})
	push(SurfacePointer{Kind: "move", X: 2})
	push(SurfacePointer{Kind: "move", X: 3})
	push(SurfacePointer{Kind: "scroll", DY: 10})
	push(SurfacePointer{Kind: "scroll", DY: 5, DX: 1})
	push(SurfacePointer{Kind: "press", Button: "left", X: 3})
	push(SurfacePointer{Kind: "move", X: 4})
	var got []SurfacePointer
	q.end()
	for {
		n, ok := q.pop()
		if !ok {
			break
		}
		p, err := ReadSurfacePointer(n.params)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if len(got) != 4 || got[0].X != 3 || got[1].DY != 15 || got[1].DX != 1 || got[2].Kind != "press" || got[3].X != 4 {
		t.Errorf("queued %+v, want the last move, the summed scroll, the press, the move after it", got)
	}
}

// fakeHost is a host's end of a side channel that has given the plugin one pool of n buffers and
// answers each frame at once: its frame.done, then its buffer.free. seqs are the frames' seqs in
// the order they arrived.
func fakeHost(t *testing.T, n int) (*Surface, *[]uint64, *sync.Mutex) {
	t.Helper()
	host, child, err := NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	fd, _ := unix.Dup(int(child.Fd()))
	child.Close()
	t.Cleanup(func() { host.Close() })
	sf := surfaceOn(t, newSurfaceConn(fd))
	var specs []SurfaceBufferSpec
	var fds []int
	for i := range n {
		mfd, _ := unix.MemfdCreate("pool", unix.MFD_CLOEXEC)
		unix.Ftruncate(mfd, 64*4*16)
		specs = append(specs, SurfaceBufferSpec{ID: 2 * (i + 1), Kind: "memory", Format: "rgba8", W: 64, H: 16, Pitch: 256, Size: 64 * 4 * 16})
		fds = append(fds, mfd)
	}
	if err := host.Send(BuffersMsg(1, specs), fds...); err != nil {
		t.Fatal(err)
	}
	for _, f := range fds {
		unix.Close(f)
	}
	sf.setInfo(SurfaceInfo{Gen: 1, W: 64, H: 16, Scale: 1})
	seqs, mu := &[]uint64{}, &sync.Mutex{}
	onHost(t, host, func() {
		for {
			m, g, err := host.Recv()
			if err != nil {
				return
			}
			g.CloseAll()
			if WireOp(m) != OpFrame {
				continue
			}
			f, _ := ReadFrameMsg(m)
			mu.Lock()
			*seqs = append(*seqs, f.Seq)
			mu.Unlock()
			host.Send(FrameDoneMsg(SurfaceFrameDone{Seq: f.Seq, Outcome: "drawn", WhenNS: f.InputT + 1}))
			host.Send(BufferFreeMsg(f.Buffer))
		}
	})
	return sf, seqs, mu
}

// Presents from many goroutines reach the host with their seqs rising: a seq and its send are one
// step, so the host never closes a plugin for the order its own goroutines raced in.
func TestConcurrentSurfacePresentsArriveInSeqOrder(t *testing.T) {
	sf, seqs, mu := fakeHost(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				b, err := sf.Buffer(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := sf.Present(b, image.Rectangle{}, 0); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*seqs)
		mu.Unlock()
		if n == 400 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*seqs) != 400 {
		t.Fatalf("%d frames arrived, want 400", len(*seqs))
	}
	for i := 1; i < len(*seqs); i++ {
		if (*seqs)[i] <= (*seqs)[i-1] {
			t.Fatalf("frame %d arrived with seq %d after %d", i, (*seqs)[i], (*seqs)[i-1])
		}
	}
}

// A frame's outcome carries the InputT it was presented with, even when it comes at once.
func TestASurfaceFramesOutcomeCarriesItsInputT(t *testing.T) {
	sf, _, _ := fakeHost(t, 1)
	b, err := sf.Buffer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seq, err := sf.Present(b, image.Rectangle{}, 777)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-sf.Done():
		if d.Seq != seq || d.InputT != 777 {
			t.Errorf("frame.done %+v, want seq %d with InputT 777", d, seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame.done")
	}
}
