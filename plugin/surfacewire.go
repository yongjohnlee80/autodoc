package plugin

import (
	"errors"
	"fmt"
	"image"
)

// THE SIDE CHANNEL'S OPS — what a surface's datagrams carry beside stdout (ADR 1792797354 §4.2):
// the host's buffer pools, the plugin's frames, and each frame's outcome and its buffer's reuse
// credit. One datagram is one msgpack map {op, ...}; the fds it names ride on it as SCM_RIGHTS.

// The side channel's limits.
const (
	MaxSurfaceDatagram = 2048 // under macOS's AF_UNIX SOCK_DGRAM default, net.local.dgram.maxdgram
	MaxSurfaceFDs      = 4
	MaxPoolBuffers     = 3 // one shown, one pending, one being drawn
)

// SurfaceFDEnv names the side channel's fd in the plugin's environment.
const SurfaceFDEnv = "AUTODOC_PLUGIN_SURFACE_FD"

// The ops.
const (
	OpBuffers       = "buffers"    // host → plugin: a generation's pool, an fd per buffer
	OpBufferAdd     = "buffer.add" // plugin → host: a DMA-BUF it exported, an fd per plane
	OpBufferDrop    = "buffer.remove"
	OpSurfaceAck    = "surface.ack"    // plugin → host: it holds a generation's plugin.surface and buffers
	OpFrame         = "frame"          // plugin → host: present a buffer
	OpFrameDone     = "frame.done"     // host → plugin: that frame's outcome
	OpBufferFree    = "buffer.free"    // host → plugin: the buffer may be drawn into again
	OpBufferRefused = "buffer.refused" // host → plugin: a buffer.add not taken ("budget", "size"); not a violation
)

// ErrSurfaceWire is a malformed side-channel message: a violation, which closes the plugin.
var ErrSurfaceWire = errors.New("plugin: malformed side-channel message")

// SurfaceBufferSpec is one buffer of a pool: its id (lifetime-unique; the host's even, a plugin's
// odd), the generation it belongs to, and its layout in pixels.
type SurfaceBufferSpec struct {
	ID, Gen      int
	Kind, Format string // "memory"; "rgba8" or "bgra8"
	W, H, Pitch  int
	Size         int // bytes of the shared object
}

// SurfaceFrame is a frame: present buffer Buffer of generation Gen. Seq rises strictly. A frame of
// a generation the host has retired is answered "retired", not refused: it was in transit. Dirty,
// when not empty, is the part that changed since the buffer's last frame. InputT is the T of the
// latest input it shows.
type SurfaceFrame struct {
	Seq    uint64
	Gen    int
	Buffer int
	Dirty  image.Rectangle
	InputT int64
}

// SurfaceFrameDone is a frame's outcome: "drawn", "superseded", "not_shown", "unsupported",
// "closed", or "retired" (its generation ended before it arrived). WhenNS is when it was drawn.
type SurfaceFrameDone struct {
	Seq     uint64
	Outcome string
	WhenNS  int64
}

// BuffersMsg is the buffers op: specs[i] is the buffer whose fd is the datagram's i-th.
func BuffersMsg(gen int, specs []SurfaceBufferSpec) map[string]any {
	list := make([]any, len(specs))
	for i, s := range specs {
		list[i] = map[string]any{"id": s.ID, "kind": s.Kind, "format": s.Format, "w": s.W, "h": s.H, "pitch": s.Pitch, "size": s.Size}
	}
	return map[string]any{"op": OpBuffers, "gen": gen, "buffers": list}
}

// FrameMsg is the frame op.
func FrameMsg(f SurfaceFrame) map[string]any {
	m := map[string]any{"op": OpFrame, "seq": f.Seq, "gen": f.Gen, "buffer": f.Buffer}
	if !f.Dirty.Empty() {
		m["dirty"] = []any{f.Dirty.Min.X, f.Dirty.Min.Y, f.Dirty.Dx(), f.Dirty.Dy()}
	}
	if f.InputT != 0 {
		m["input_t"] = f.InputT
	}
	return m
}

// FrameDoneMsg is the frame.done op.
func FrameDoneMsg(d SurfaceFrameDone) map[string]any {
	return map[string]any{"op": OpFrameDone, "seq": d.Seq, "outcome": d.Outcome, "when_ns": d.WhenNS}
}

// BufferFreeMsg is the buffer.free op.
func BufferFreeMsg(id int) map[string]any { return map[string]any{"op": OpBufferFree, "buffer": id} }

// WireOp is a message's op.
func WireOp(m map[string]any) string { return str(m, "op") }

func seqOf(m map[string]any) (uint64, bool) {
	switch n := m["seq"].(type) {
	case uint64:
		return n, true
	case int64:
		return uint64(n), n >= 0
	}
	return 0, false
}

// ReadBuffersMsg reads a buffers op, refusing more buffers than a pool holds or than fds came.
func ReadBuffersMsg(m map[string]any, fds int) (gen int, specs []SurfaceBufferSpec, err error) {
	gen, ok := num(m, "gen")
	list, _ := m["buffers"].([]any)
	if !ok || len(list) == 0 || len(list) > MaxPoolBuffers || len(list) != fds {
		return 0, nil, fmt.Errorf("%w: buffers wants a gen and 1–%d buffers, one fd each", ErrSurfaceWire, MaxPoolBuffers)
	}
	for _, v := range list {
		b, _ := v.(map[string]any)
		var s SurfaceBufferSpec
		var oks [5]bool
		s.ID, oks[0] = num(b, "id")
		s.W, oks[1] = num(b, "w")
		s.H, oks[2] = num(b, "h")
		s.Pitch, oks[3] = num(b, "pitch")
		s.Size, oks[4] = num(b, "size")
		s.Gen, s.Kind, s.Format = gen, str(b, "kind"), str(b, "format")
		for _, o := range oks {
			if !o {
				return 0, nil, fmt.Errorf("%w: a buffer wants id, w, h, pitch and size", ErrSurfaceWire)
			}
		}
		if s.Kind != "memory" || s.W <= 0 || s.H <= 0 || s.Pitch < s.W*4 || s.Size < s.Pitch*s.H {
			return 0, nil, fmt.Errorf("%w: buffer %d: an impossible layout", ErrSurfaceWire, s.ID)
		}
		specs = append(specs, s)
	}
	return gen, specs, nil
}

// ReadFrameMsg reads a frame op.
func ReadFrameMsg(m map[string]any) (SurfaceFrame, error) {
	seq, okS := seqOf(m)
	buf, okB := num(m, "buffer")
	gen, okG := num(m, "gen")
	if !okS || !okB || !okG {
		return SurfaceFrame{}, fmt.Errorf("%w: frame wants seq, gen and buffer", ErrSurfaceWire)
	}
	f := SurfaceFrame{Seq: seq, Gen: gen, Buffer: buf}
	f.InputT, _ = m["input_t"].(int64)
	if d, ok := m["dirty"].([]any); ok {
		if len(d) != 4 {
			return SurfaceFrame{}, fmt.Errorf("%w: frame's dirty wants x, y, w, h", ErrSurfaceWire)
		}
		var v [4]int
		for i := range v {
			n, ok := number(d[i])
			if !ok || n < 0 {
				return SurfaceFrame{}, fmt.Errorf("%w: frame's dirty wants nonnegative numbers", ErrSurfaceWire)
			}
			v[i] = n
		}
		f.Dirty = image.Rect(v[0], v[1], v[0]+v[2], v[1]+v[3])
	}
	return f, nil
}

// ReadFrameDoneMsg reads a frame.done op.
func ReadFrameDoneMsg(m map[string]any) (SurfaceFrameDone, error) {
	seq, ok := seqOf(m)
	if !ok {
		return SurfaceFrameDone{}, fmt.Errorf("%w: frame.done wants seq", ErrSurfaceWire)
	}
	when, _ := m["when_ns"].(int64)
	return SurfaceFrameDone{Seq: seq, Outcome: str(m, "outcome"), WhenNS: when}, nil
}

// ReadBufferFreeMsg reads a buffer.free op.
func ReadBufferFreeMsg(m map[string]any) (int, error) {
	id, ok := num(m, "buffer")
	if !ok {
		return 0, fmt.Errorf("%w: buffer.free wants buffer", ErrSurfaceWire)
	}
	return id, nil
}
