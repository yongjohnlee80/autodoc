//go:build gui

package tui

import (
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/op/extimage"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// Until the SDK's DMA-BUF registration comes with its cells, a window offers its plugins memory
// frames only, whatever its caps import: a dmabuf tuple is not announced.
func TestASurfaceOffersMemoryFramesOnly(t *testing.T) {
	caps := extimage.Caps{Tuples: []extimage.Tuple{
		{Kind: extimage.KindDMABuf, Format: extimage.FormatBGRA8, Path: extimage.PathZeroCopy},
		{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathCPU, MaxSize: image.Pt(8192, 8192)},
	}}
	kinds := surfaceKinds(caps)
	if len(kinds) != 1 || kinds[0].Kind != "memory" || kinds[0].Format != "rgba8" || kinds[0].MaxW != 8192 {
		t.Errorf("kinds %+v, want the memory rgba8 tuple alone", kinds)
	}
}

// The surface's native view shapes the pointer as its plugin last asked; before it asks, the
// default.
func TestASurfaceViewShapesThePointerAsItsPluginAsks(t *testing.T) {
	run := &pluginRun{}
	v := surfaceView{v: &pluginView{run: run}}
	if s := v.PointerShape(); s != tuicore.PointerDefault {
		t.Errorf("before any host.surface.cursor: %v", s)
	}
	run.mu.Lock()
	run.pointer = pointerShapeOf("resize-ew")
	run.mu.Unlock()
	if s := v.PointerShape(); s != tuicore.PointerResizeEW {
		t.Errorf("after resize-ew: %v", s)
	}
	if s := (surfaceView{v: &pluginView{}}).PointerShape(); s != tuicore.PointerDefault {
		t.Errorf("a view with no run: %v", s)
	}
}
