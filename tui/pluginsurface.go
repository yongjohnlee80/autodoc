package tui

import (
	"math"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// A PLUGIN'S PIXEL SURFACE, AS THE REST OF THE HOST SEES IT
// (docs/decisions/plugins.md#pixel-surfaces): a run whose manifest declares [surface] at protocol
// 10 gets a side channel (pluginsurface_linux.go), and, in a window that draws streams, a native
// view in its own cells (pluginsurface_gui.go); in a terminal that shows pictures, its frames as a
// picture over its cells (pluginsurface_cells.go). Elsewhere it draws host.frame's cells, as every
// plugin does.

// surfaceMaxPixels is a frame's side in device pixels: protocol 10's, below the window's MaxSize.
const surfaceMaxPixels = 4096

// surfaceCellPixels is a cell's size in a surface's device pixels: the backend's, where it reads
// them (a window's); else the scale a terminal's pictures are drawn at (widget.CellPixelsW and
// CellPixelsH, as a diagram preview's), so a terminal's surface has a size and its pointer a place.
func surfaceCellPixels(ctx *tuicore.Context) tuicore.CellPixels {
	if cp := ctx.CellPixels(); cp.W > 0 && cp.H > 0 {
		return cp
	}
	return tuicore.CellPixels{W: widget.CellPixelsW, H: widget.CellPixelsH}
}

// pointerShapes are host.surface.cursor's names, as the SDK's SetPointerShape lists them.
var pointerShapes = map[string]tuicore.PointerShape{
	"default":     tuicore.PointerDefault,
	"text":        tuicore.PointerText,
	"hand":        tuicore.PointerHand,
	"crosshair":   tuicore.PointerCrosshair,
	"move":        tuicore.PointerMove,
	"resize-ew":   tuicore.PointerResizeEW,
	"resize-ns":   tuicore.PointerResizeNS,
	"not-allowed": tuicore.PointerNotAllowed,
}

// pointerShapeOf is a host.surface.cursor's shape: the default for a name it doesn't know, which a
// later protocol's plugin may send.
func pointerShapeOf(name string) tuicore.PointerShape { return pointerShapes[name] }

// pixelSurface is a run's surface owner: the loop tells it the geometry its view was painted at,
// and closes it with the run.
type pixelSurface interface {
	Resize(g surfaceGeometry)
	Close()
}

// surfaceGeometry is what a generation is made of: the size in dp, the device scale, and the frame
// kinds the window draws (its caps).
type surfaceGeometry struct {
	W, H, Scale float32
	Kinds       []plugin.SurfaceKind
}

func (g surfaceGeometry) pixels() (int, int) {
	return int(math.Ceil(float64(g.W * g.Scale))), int(math.Ceil(float64(g.H * g.Scale)))
}

func (g surfaceGeometry) same(o surfaceGeometry) bool {
	if g.W != o.W || g.H != o.H || g.Scale != o.Scale || len(g.Kinds) != len(o.Kinds) {
		return false
	}
	for i := range g.Kinds {
		if g.Kinds[i] != o.Kinds[i] {
			return false
		}
	}
	return true
}

// surfaceSink is where admitted frames go.
type surfaceSink interface {
	Push(f *extimage.Frame) surfaceReceipt
	Close()
}

// surfaceReceipt is a pushed frame's receipt: Done closes once its outcome is known.
type surfaceReceipt interface {
	Done() <-chan struct{}
	Result() (outcome string, when time.Time)
}
