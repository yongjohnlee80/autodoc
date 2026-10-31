//go:build linux

package tui

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/yongjohnlee80/autodoc/plugin"
	"golang.org/x/sys/unix"
)

// The plane's layout is checked with every wire integer bounded first: no product can wrap, so a
// huge pitch on a small object is refused, never admitted by an overflowed sum. The positive control
// is the same object at a pitch that fits.
func TestAPlaneLayoutCannotOverflowIntoAdmission(t *testing.T) {
	const size = 4096
	if why := planeLayout(size, 1, 2, 0, 4); why != "" {
		t.Fatalf("the control: %s", why)
	}
	for name, c := range map[string]struct{ w, h, offset, pitch int }{
		"MaxInt64 pitch":        {1, 2, 0, math.MaxInt64},
		"MaxUint32 pitch":       {1, 2, 0, math.MaxUint32},
		"a pitch past 64 MiB":   {1, 2, 0, surfaceMaxBacking + 1},
		"rows past the object":  {64, 4096, 0, 256},
		"an offset past it":     {1, 1, size + 1, 4},
		"a negative offset":     {1, 1, -1, 4},
		"a negative width":      {-1, 1, 0, 4},
		"a height past 4096":    {1, 4097, 0, 4},
		"a pitch below its row": {4, 1, 0, 8},
		"MaxInt64 offset":       {1, 1, math.MaxInt64, 4},
	} {
		if why := planeLayout(size, c.w, c.h, c.offset, c.pitch); why == "" {
			t.Errorf("%s: admitted", name)
		}
	}
}

// An owner with nothing held ends at Close although its peer stays open and never reads: its
// transport is shut down before the writer is joined. The control: the peer was really unread (the
// writer's queue had filled).
func TestAnOwnerEndsAtCloseThoughItsPeerNeverReads(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[1])
	unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, 4096)
	t.Setenv(plugin.SurfaceFDEnv, strconv.Itoa(fds[0]))
	host, _, err := plugin.OpenPluginSurfaceConn()
	if err != nil {
		t.Fatal(err)
	}
	owner := newSurfaceOwner(host, &fakeSink{}, func(plugin.SurfaceInfo) {}, func(string) {})
	for i := 0; i < 1000 && !owner.violated.Load(); i++ {
		owner.emit(plugin.FrameDoneMsg(plugin.SurfaceFrameDone{Seq: uint64(i + 1), Outcome: "closed"}))
	}
	if !owner.violated.Load() {
		t.Fatal("the control: the queue never filled, so the peer's silence went unexercised")
	}
	owner.Close()
	select {
	case <-owner.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("the owner waited on a peer that never reads")
	}
}

// dmaBuf is a real DMA-BUF of size bytes from the system heap; the cell is skipped where there is
// none (a container without /dev/dma_heap).
func dmaBuf(t *testing.T, size uint64) int {
	t.Helper()
	heap, err := unix.Open("/dev/dma_heap/system", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no DMA heap here: %v", err)
	}
	defer unix.Close(heap)
	alloc := struct {
		Len       uint64
		FD        uint32
		Flags     uint32
		HeapFlags uint64
	}{Len: size, Flags: unix.O_RDWR | unix.O_CLOEXEC}
	const dmaHeapIoctlAlloc = 0xc0184800
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(heap), dmaHeapIoctlAlloc, uintptr(unsafe.Pointer(&alloc))); errno != 0 {
		t.Skipf("the DMA heap refused an allocation: %v", errno)
	}
	t.Cleanup(func() { unix.Close(int(alloc.FD)) })
	return int(alloc.FD)
}

// dmaGeometry offers DMA-BUFs (LINEAR, RGBA8), as a window whose caps import them would.
var dmaGeometry = surfaceGeometry{W: 16, H: 8, Scale: 1, Kinds: []plugin.SurfaceKind{
	{Kind: "memory", Format: "rgba8"}, {Kind: "dmabuf", Format: "rgba8"}}}

func addMsg(gen, id, w, h, pitch int) map[string]any {
	return map[string]any{"op": plugin.OpBufferAdd, "gen": gen, "id": id, "kind": "dmabuf", "format": "rgba8",
		"w": w, "h": h, "modifier": 0, "planes": []any{map[string]any{"offset": 0, "pitch": pitch}}}
}

// A real DMA-BUF the right size is admitted, and credited: its buffer.free comes, and a frame of it
// is drawn. One far larger than its frame is refused for size; past the budget, refused for budget;
// neither is a violation, and a retry under a new id is admitted.
func TestADMABufIsAdmittedCreditedOrRefused(t *testing.T) {
	r := newSurfaceRigCeiling(t, surfaceMaxBytes)
	gen, _ := r.pool(t, dmaGeometry)
	good := dmaBuf(t, 16*4*8)
	if err := r.plug.Send(addMsg(gen, 1, 16, 8, 64), good); err != nil {
		t.Fatal(err)
	}
	if id, err := plugin.ReadBufferFreeMsg(r.nextOp(t, plugin.OpBufferFree)); err != nil || id != 1 {
		t.Fatalf("acceptance %d, %v; want buffer.free 1", id, err)
	}
	r.present(t, 1, gen, 1)
	h := r.take(t)
	if d := h.f.Desc(); d.Planes[0].Pitch != 64 {
		t.Errorf("the frame's pitch is %d", d.Planes[0].Pitch)
	}
	h.r.complete("drawn")
	h.f.Drop()
	r.nextOp(t, plugin.OpFrameDone)
	big := dmaBuf(t, 8<<20) // 8 MiB behind a 16×8 frame
	if err := r.plug.Send(addMsg(gen, 3, 16, 8, 64), big); err != nil {
		t.Fatal(err)
	}
	refused := r.nextOp(t, plugin.OpBufferRefused)
	if refused["reason"] != "size" {
		t.Errorf("refused %v, want for size", refused)
	}
	retry := dmaBuf(t, 16*4*8)
	if err := r.plug.Send(addMsg(gen, 5, 16, 8, 64), retry); err != nil {
		t.Fatal(err)
	}
	if id, _ := plugin.ReadBufferFreeMsg(r.nextOp(t, plugin.OpBufferFree)); id != 5 {
		t.Errorf("the retry's acceptance named %d", id)
	}
	r.noViolation(t)
}

// Past the budget a DMA-BUF is refused for budget, not as a violation.
func TestADMABufPastTheBudgetIsRefused(t *testing.T) {
	r := newSurfaceRigCeiling(t, 3*16*4*8+100) // the pool, and not one byte more
	gen, _ := r.pool(t, dmaGeometry)
	fd := dmaBuf(t, 16*4*8)
	if err := r.plug.Send(addMsg(gen, 1, 16, 8, 64), fd); err != nil {
		t.Fatal(err)
	}
	refused := r.nextOp(t, plugin.OpBufferRefused)
	if refused["reason"] != "budget" {
		t.Errorf("refused %v, want for budget", refused)
	}
	r.noViolation(t)
}

// A genuine DMA-BUF with a wrapping pitch is refused at admission, before any mapping.
func TestAGenuineDMABufWithAWrappingPitchIsRefused(t *testing.T) {
	fd := dmaBuf(t, 4096)
	if why := admitDMABuf(fd, 1, 2, 0, 4); why != "" {
		t.Fatalf("the control: %s", why)
	}
	if why := admitDMABuf(fd, 1, 2, 0, math.MaxInt64); !strings.Contains(why, "pitch") {
		t.Errorf("a MaxInt64 pitch: %q", why)
	}
}
