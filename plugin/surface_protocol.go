package plugin

import (
	"fmt"
	"math"
)

// PROTOCOL 10'S MESSAGES ON STDOUT — a pixel surface's geometry, the pointer in dp, paste, and what
// the plugin tells the host of its caret, its pointer's shape and the clipboard (ADR 1792797354).
// The frames themselves cross the side channel (surfacewire.go).

// The notifications of protocol 10.
const (
	// MethodSurface is a generation of the surface: its size in dp, the device scale and the frame
	// kinds the window draws. It follows plugin.open, and comes again at each resize, scale or caps
	// change; a host without pixels never sends it.
	MethodSurface = "plugin.surface"
	// MethodSurfacePointer is the pointer on a surface, in dp: it replaces plugin.pointer there.
	MethodSurfacePointer = "plugin.surface.pointer"
	// MethodPaste is the user's paste chord on a focused surface.
	MethodPaste = "plugin.paste"

	// MethodCaret is where the surface's text caret is, for the host's IME: it draws the preedit there
	// and places the candidate window by it.
	MethodCaret = "host.surface.caret"
	// MethodPointerShape is the pointer's shape over the surface.
	MethodPointerShape = "host.surface.cursor"
	// MethodClipboardWrite writes text to the clipboard. There is no read: a read is the user's paste.
	MethodClipboardWrite = "host.clipboard.write"
)

// SurfaceKind is a frame kind the window draws: "memory" or "dmabuf", its format ("rgba8",
// "bgra8"), its DRM modifier, and its largest size in pixels.
type SurfaceKind struct {
	Kind, Format string
	Modifier     uint64
	MaxW, MaxH   int
}

// SurfaceInfo is one generation of a surface: Gen matches the buffers the side channel brings for it.
// W and H are in dp; Scale is device pixels per dp, so a frame of W×Scale by H×Scale pixels is
// drawn one to one.
type SurfaceInfo struct {
	Gen   int
	W, H  float32
	Scale float32
	Kinds []SurfaceKind
}

// Pixels is the surface's size in device pixels, rounded up.
func (s SurfaceInfo) Pixels() (w, h int) {
	return int(math.Ceil(float64(s.W * s.Scale))), int(math.Ceil(float64(s.H * s.Scale)))
}

// SurfacePointer is the pointer on a surface. Kind is "move", "press", "release", "scroll" or
// "leave"; Button is "left", "middle" or "right" for a press or release. X and Y are frame-local dp,
// and may lie outside the surface while a button holds the pointer. DX and DY are a scroll's, in dp.
// T is when the host dispatched it (Unix nanoseconds), for latency.
type SurfacePointer struct {
	Kind, Button            string
	X, Y, DX, DY            float32
	Count                   int
	Ctrl, Alt, Shift, Super bool
	T                       int64
}

// Caret is the surface's text caret in dp, frame-local, with its baseline; the zero Caret is none.
type Caret struct{ X, Y, W, H, Baseline float32 }

func (s SurfaceKind) wire() map[string]any {
	return map[string]any{"kind": s.Kind, "format": s.Format, "modifier": s.Modifier, "max_w": s.MaxW, "max_h": s.MaxH}
}

// SurfaceParams are plugin.surface's.
func SurfaceParams(s SurfaceInfo) []any {
	kinds := make([]any, len(s.Kinds))
	for i, k := range s.Kinds {
		kinds[i] = k.wire()
	}
	return one(map[string]any{"gen": s.Gen, "w": s.W, "h": s.H, "scale": s.Scale, "kinds": kinds})
}

// SurfacePointerParams are plugin.surface.pointer's.
func SurfacePointerParams(p SurfacePointer) []any {
	m := map[string]any{"kind": p.Kind, "x": p.X, "y": p.Y, "t": p.T}
	if p.Button != "" {
		m["button"] = p.Button
	}
	if p.Count != 0 {
		m["count"] = p.Count
	}
	if p.DX != 0 || p.DY != 0 {
		m["dx"], m["dy"] = p.DX, p.DY
	}
	for k, v := range map[string]bool{"ctrl": p.Ctrl, "alt": p.Alt, "shift": p.Shift, "super": p.Super} {
		if v {
			m[k] = true
		}
	}
	return one(m)
}

// PasteParams are plugin.paste's.
func PasteParams(text string) []any { return one(map[string]any{"text": text}) }

// CaretParams are host.surface.caret's; the zero Caret sends {}, no caret.
func CaretParams(c Caret) []any {
	if c == (Caret{}) {
		return EmptyParams()
	}
	return one(map[string]any{"x": c.X, "y": c.Y, "w": c.W, "h": c.H, "baseline": c.Baseline})
}

// PointerShapeParams are host.surface.cursor's.
func PointerShapeParams(shape string) []any { return one(map[string]any{"shape": shape}) }

// ClipboardWriteParams are host.clipboard.write's.
func ClipboardWriteParams(text string) []any { return one(map[string]any{"text": text}) }

// dpNum is a number as the decoder gives it (or the encoder holds it), whole or not, as a float32
// that is finite.
func dpNum(m map[string]any, k string) (float32, bool) {
	var f float64
	switch n := m[k].(type) {
	case float64:
		f = n
	case float32: // as the encoder holds it, and the queue's coalescing writes it
		f = float64(n)
	case int64:
		f = float64(n)
	case uint64:
		f = float64(n)
	case int:
		f = float64(n)
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > math.MaxFloat32 {
		return 0, false
	}
	return float32(f), true
}

// MaxSurfaceKinds bounds plugin.surface's kinds: a window draws a handful.
const MaxSurfaceKinds = 32

// ReadSurface reads plugin.surface.
func ReadSurface(params []any) (SurfaceInfo, error) {
	m, err := arg(params)
	if err != nil {
		return SurfaceInfo{}, err
	}
	gen, okG := num(m, "gen")
	w, okW := dpNum(m, "w")
	h, okH := dpNum(m, "h")
	scale, okS := dpNum(m, "scale")
	if !okG || !okW || !okH || !okS || w <= 0 || h <= 0 || scale <= 0 {
		return SurfaceInfo{}, fmt.Errorf("%w: plugin.surface wants gen, a positive w and h, and a scale", ErrParams)
	}
	raw, _ := m["kinds"].([]any)
	if len(raw) > MaxSurfaceKinds {
		return SurfaceInfo{}, fmt.Errorf("%w: plugin.surface: %d kinds", ErrParams, len(raw))
	}
	s := SurfaceInfo{Gen: gen, W: w, H: h, Scale: scale}
	for _, v := range raw {
		k, ok := v.(map[string]any)
		if !ok {
			continue
		}
		mw, _ := num(k, "max_w")
		mh, _ := num(k, "max_h")
		mod, _ := k["modifier"].(uint64)
		if i, ok := k["modifier"].(int64); ok && i >= 0 {
			mod = uint64(i)
		}
		s.Kinds = append(s.Kinds, SurfaceKind{Kind: str(k, "kind"), Format: str(k, "format"), Modifier: mod, MaxW: mw, MaxH: mh})
	}
	return s, nil
}

// ReadSurfacePointer refuses a pointer event of an unknown kind or button.
func ReadSurfacePointer(params []any) (SurfacePointer, error) {
	m, err := arg(params)
	if err != nil {
		return SurfacePointer{}, err
	}
	x, okX := dpNum(m, "x")
	y, okY := dpNum(m, "y")
	dx, _ := dpNum(m, "dx")
	dy, _ := dpNum(m, "dy")
	count, _ := num(m, "count")
	t, _ := m["t"].(int64)
	p := SurfacePointer{Kind: str(m, "kind"), Button: str(m, "button"), X: x, Y: y, DX: dx, DY: dy, Count: count,
		Ctrl: flag(m, "ctrl"), Alt: flag(m, "alt"), Shift: flag(m, "shift"), Super: flag(m, "super"), T: t}
	switch {
	case !okX || !okY:
		return SurfacePointer{}, fmt.Errorf("%w: plugin.surface.pointer wants x and y", ErrParams)
	case p.Kind == "press" || p.Kind == "release":
		if p.Button != "left" && p.Button != "middle" && p.Button != "right" {
			return SurfacePointer{}, fmt.Errorf("%w: plugin.surface.pointer: button %q", ErrParams, p.Button)
		}
	case p.Kind != "move" && p.Kind != "scroll" && p.Kind != "leave":
		return SurfacePointer{}, fmt.Errorf("%w: plugin.surface.pointer: kind %q", ErrParams, p.Kind)
	}
	return p, nil
}

// ReadPaste reads plugin.paste.
func ReadPaste(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	return str(m, "text"), nil
}

// ReadCaret reads host.surface.caret: ok false for {}, no caret.
func ReadCaret(params []any) (c Caret, ok bool, err error) {
	m, err := arg(params)
	if err != nil {
		return Caret{}, false, err
	}
	if len(m) == 0 {
		return Caret{}, false, nil
	}
	var all [5]bool
	c.X, all[0] = dpNum(m, "x")
	c.Y, all[1] = dpNum(m, "y")
	c.W, all[2] = dpNum(m, "w")
	c.H, all[3] = dpNum(m, "h")
	c.Baseline, all[4] = dpNum(m, "baseline")
	for _, v := range all {
		if !v {
			return Caret{}, false, fmt.Errorf("%w: host.surface.caret wants x, y, w, h and baseline, or nothing", ErrParams)
		}
	}
	return c, true, nil
}

// ReadPointerShape reads host.surface.cursor.
func ReadPointerShape(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	return str(m, "shape"), nil
}

// ReadClipboardWrite reads host.clipboard.write.
func ReadClipboardWrite(params []any) (string, error) {
	m, err := arg(params)
	if err != nil {
		return "", err
	}
	return str(m, "text"), nil
}
