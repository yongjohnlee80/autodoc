//go:build linux

package tui

import (
	"context"
	"errors"
	"image"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	"golang.org/x/sys/unix"
)

// The SDK's registration against the host's own owner, on real DMA-BUFs from the system heap
// (skipped where there is none). One far larger than its frame is refused for size, and the plugin
// draws into a memory buffer instead; one the right size is accepted, presented, and reaches the
// sink as a DMA-BUF frame; its credit hands it out again. Nothing in it is a violation.
func TestTheSDKRegistersAndPresentsADMABufThroughTheHost(t *testing.T) {
	big := dmaBuf(t, 8<<20) // 8 MiB behind a 16×8 frame
	good := dmaBuf(t, 16*4*8)

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
	r := &surfaceRig{sink: &fakeSink{}, infos: make(chan plugin.SurfaceInfo, 8), violated: make(chan string, 8)}
	r.owner = newSurfaceOwnerCeiling(host, r.sink, func(i plugin.SurfaceInfo) { r.infos <- i }, func(why string) { r.violated <- why }, surfaceMaxBytes)
	sf, setInfo := plugin.NewSurface(plug)
	t.Cleanup(func() { r.owner.Close(); sf.Close() })

	r.owner.Resize(dmaGeometry)
	var info plugin.SurfaceInfo
	select {
	case info = <-r.infos:
	case <-time.After(surfaceWait):
		t.Fatal("the owner announced no generation")
	}
	setInfo(info)
	// NOTE: a bound on a failing run alone: every wait below ends at the owner's answer.
	ctx, cancel := context.WithTimeout(context.Background(), 4*surfaceWait)
	defer cancel()
	d := plugin.SurfaceDMABuf{Gen: info.Gen, W: 16, H: 8, Pitch: 64, Format: "rgba8"}

	// done is the frame at the sink, drawn; and its outcome, back at the plugin.
	done := func(what string, kind extimage.Kind) {
		t.Helper()
		h := r.take(t)
		if got := h.f.Desc().Kind; got != kind {
			t.Errorf("%s: the frame at the sink is kind %v, want %v", what, got, kind)
		}
		h.r.complete("drawn")
		h.f.Drop()
		select {
		case fd := <-sf.Done():
			if fd.Outcome != "drawn" {
				t.Errorf("%s: its outcome %q", what, fd.Outcome)
			}
		case <-ctx.Done():
			t.Fatalf("%s: no outcome came back", what)
		}
	}

	d.FD = big
	var refused *plugin.SurfaceRefused
	if _, err := sf.AddBuffer(ctx, d); !errors.As(err, &refused) || refused.Reason != "size" {
		t.Fatalf("an 8 MiB object behind a 16×8 frame: %v; want it refused for size", err)
	}
	mem, err := sf.Buffer(ctx)
	if err != nil || mem.Kind != "memory" {
		t.Fatalf("after the refusal Buffer granted %+v, %v; want a memory buffer", mem, err)
	}
	if _, err := sf.Present(mem, image.Rectangle{}, 0); err != nil {
		t.Fatal(err)
	}
	done("the memory frame after the refusal", extimage.KindMemory)

	d.FD = good
	added, err := sf.AddBuffer(ctx, d)
	if err != nil {
		t.Fatalf("a DMA-BUF the right size: %v; want it accepted", err)
	}
	if added.ID <= 1 {
		t.Errorf("the second registration took id %d, the refused one had 1", added.ID)
	}
	for round := range 2 { // the second round is granted by the first frame's credit
		b, err := sf.AddedBuffer(ctx)
		if err != nil || b != added {
			t.Fatalf("round %d: AddedBuffer granted %+v, %v; want the registered buffer", round, b, err)
		}
		if _, err := sf.Present(b, image.Rectangle{}, 0); err != nil {
			t.Fatal(err)
		}
		done("the registered buffer's frame", extimage.KindDMABuf)
	}
	r.noViolation(t)
}
