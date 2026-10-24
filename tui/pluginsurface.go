package tui

import (
	"math"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// A PLUGIN'S PIXEL SURFACE, AS THE REST OF THE HOST SEES IT (ADR 1792797354): a run whose manifest
// declares [surface] at protocol 10 gets a side channel (pluginsurface_linux.go), and, in a window
// that draws streams, a native view in its own cells (pluginsurface_gui.go). Elsewhere it draws
// host.frame's cells, as every plugin does.

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
