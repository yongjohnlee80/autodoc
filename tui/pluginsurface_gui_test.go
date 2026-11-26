//go:build gui

package tui

import (
	"image"
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/gio/op/extimage"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// The window's caps offer a plugin memory frames and LINEAR DMA-BUFs: a DMA-BUF tuple of another
// modifier is one a registration would be refused for as a violation, so it isn't offered.
func TestASurfaceOffersMemoryFramesAndLinearDMABufs(t *testing.T) {
	caps := extimage.Caps{Tuples: []extimage.Tuple{
		{Kind: extimage.KindDMABuf, Format: extimage.FormatBGRA8, Path: extimage.PathZeroCopy, MaxSize: image.Pt(4096, 4096)},
		{Kind: extimage.KindDMABuf, Format: extimage.FormatBGRA8, Modifier: 0x0300000000606010, Path: extimage.PathZeroCopy},
		{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathCPU, MaxSize: image.Pt(8192, 8192)},
	}}
	kinds := surfaceKinds(caps)
	want := []plugin.SurfaceKind{
		{Kind: "dmabuf", Format: "bgra8", MaxW: 4096, MaxH: 4096},
		{Kind: "memory", Format: "rgba8", MaxW: 8192, MaxH: 8192},
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("kinds %+v, want %+v", kinds, want)
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
