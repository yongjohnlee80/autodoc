package plugin

import (
	"image"
	"math"
	"testing"

	"github.com/yongjohnlee80/golib/msgpack"
)

// wire is params as they arrive: encoded and decoded, so numbers come back as the decoder makes them.
func wire(t *testing.T, params []any) []any {
	t.Helper()
	b, err := msgpack.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	v, err := msgpack.Unmarshal(b, msgpack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return v.([]any)
}

// Protocol 10's stdout messages round-trip, and a malformed one is refused.
func TestProtocolTensMessagesRoundTrip(t *testing.T) {
	info := SurfaceInfo{Gen: 3, W: 320.5, H: 200, Scale: 2, Kinds: []SurfaceKind{{Kind: "memory", Format: "rgba8", MaxW: 4096, MaxH: 4096}}}
	got, err := ReadSurface(wire(t, SurfaceParams(info)))
	if err != nil || got.Gen != 3 || got.W != 320.5 || got.Scale != 2 || len(got.Kinds) != 1 || got.Kinds[0].MaxW != 4096 {
		t.Errorf("plugin.surface %+v, %v", got, err)
	}
	if w, h := got.Pixels(); w != 641 || h != 400 {
		t.Errorf("Pixels %d×%d, want 641×400", w, h)
	}
	for _, bad := range []map[string]any{{"gen": 1, "w": 0, "h": 1, "scale": 1}, {"w": 1, "h": 1, "scale": 1}, {"gen": 1, "w": math.Inf(1), "h": 1, "scale": 1}} {
		if _, err := ReadSurface(wire(t, one(bad))); err == nil {
			t.Errorf("plugin.surface %v was read", bad)
		}
	}
	p := SurfacePointer{Kind: "press", Button: "right", X: 1.5, Y: 2.5, DX: 0, Count: 2, Ctrl: true, Super: true, T: 99}
	gp, err := ReadSurfacePointer(wire(t, SurfacePointerParams(p)))
	if err != nil || gp != p {
		t.Errorf("plugin.surface.pointer %+v, %v; want %+v", gp, err, p)
	}
	s := SurfacePointer{Kind: "scroll", X: 1, Y: 1, DX: -3, DY: 4}
	if gs, err := ReadSurfacePointer(wire(t, SurfacePointerParams(s))); err != nil || gs.DX != -3 || gs.DY != 4 {
		t.Errorf("a scroll %+v, %v", gs, err)
	}
	for _, bad := range []map[string]any{{"kind": "press", "button": "fourth", "x": 1, "y": 1}, {"kind": "hover", "x": 1, "y": 1}, {"kind": "move"}} {
		if _, err := ReadSurfacePointer(wire(t, one(bad))); err == nil {
			t.Errorf("pointer %v was read", bad)
		}
	}
	if text, err := ReadPaste(wire(t, PasteParams("héllo"))); err != nil || text != "héllo" {
		t.Errorf("plugin.paste %q, %v", text, err)
	}
	c := Caret{X: 1, Y: 2, W: 3, H: 4, Baseline: 5}
	if gc, ok, err := ReadCaret(wire(t, CaretParams(c))); err != nil || !ok || gc != c {
		t.Errorf("host.surface.caret %+v %v %v", gc, ok, err)
	}
	if _, ok, err := ReadCaret(wire(t, CaretParams(Caret{}))); err != nil || ok {
		t.Errorf("no caret: ok %v, %v", ok, err)
	}
	if _, _, err := ReadCaret(wire(t, one(map[string]any{"x": 1}))); err == nil {
		t.Error("a partial caret was read")
	}
	if sh, err := ReadPointerShape(wire(t, PointerShapeParams("hand"))); err != nil || sh != "hand" {
		t.Errorf("host.surface.cursor %q, %v", sh, err)
	}
	if text, err := ReadClipboardWrite(wire(t, ClipboardWriteParams("copied"))); err != nil || text != "copied" {
		t.Errorf("host.clipboard.write %q, %v", text, err)
	}
	for _, read := range []func([]any) error{
		func(p []any) error { _, err := ReadSurface(p); return err },
		func(p []any) error { _, err := ReadSurfacePointer(p); return err },
		func(p []any) error { _, err := ReadPaste(p); return err },
		func(p []any) error { _, _, err := ReadCaret(p); return err },
		func(p []any) error { _, err := ReadPointerShape(p); return err },
		func(p []any) error { _, err := ReadClipboardWrite(p); return err },
	} {
		if read([]any{"not a map"}) == nil {
			t.Error("a reader took a non-map")
		}
	}
}

// The side channel's ops round-trip, and a malformed one is refused.
func TestTheSideChannelsOpsRoundTripAndRefuse(t *testing.T) {
	specs := []SurfaceBufferSpec{{ID: 2, Kind: "memory", Format: "rgba8", W: 2, H: 2, Pitch: 8, Size: 16}}
	gen, got, err := ReadBuffersMsg(roundTrip(t, BuffersMsg(4, specs)), 1)
	if err != nil || gen != 4 || len(got) != 1 || got[0].Gen != 4 || got[0].Pitch != 8 {
		t.Errorf("buffers %d %+v %v", gen, got, err)
	}
	if _, _, err := ReadBuffersMsg(roundTrip(t, BuffersMsg(4, specs)), 2); err == nil {
		t.Error("buffers with an fd count that doesn't match was read")
	}
	impossible := []SurfaceBufferSpec{{ID: 2, Kind: "memory", Format: "rgba8", W: 2, H: 2, Pitch: 4, Size: 16}}
	if _, _, err := ReadBuffersMsg(roundTrip(t, BuffersMsg(4, impossible)), 1); err == nil {
		t.Error("a pitch below its row was read")
	}
	f, err := ReadFrameMsg(roundTrip(t, FrameMsg(SurfaceFrame{Seq: 1, Gen: 1, Buffer: 2, Dirty: image.Rect(1, 2, 3, 4)})))
	if err != nil || f.Dirty != image.Rect(1, 2, 3, 4) {
		t.Errorf("frame %+v, %v", f, err)
	}
	for _, bad := range []map[string]any{
		{"op": OpFrame, "seq": int64(1), "gen": int64(1), "buffer": int64(2), "dirty": []any{int64(1)}},
		{"op": OpFrame, "seq": int64(1), "gen": int64(1), "buffer": int64(2), "dirty": []any{int64(-1), int64(0), int64(1), int64(1)}},
		{"op": OpFrame, "seq": int64(-1), "gen": int64(1), "buffer": int64(2)},
	} {
		if _, err := ReadFrameMsg(bad); err == nil {
			t.Errorf("frame %v was read", bad)
		}
	}
	if id, err := ReadBufferFreeMsg(roundTrip(t, BufferFreeMsg(6))); err != nil || id != 6 {
		t.Errorf("buffer.free %d, %v", id, err)
	}
	if _, err := ReadBufferFreeMsg(map[string]any{"op": OpBufferFree}); err == nil {
		t.Error("buffer.free without its buffer was read")
	}
	if _, err := ReadFrameDoneMsg(map[string]any{"op": OpFrameDone}); err == nil {
		t.Error("frame.done without its seq was read")
	}
}
