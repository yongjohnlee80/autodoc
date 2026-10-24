//go:build gui

package tui

import (
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/op/extimage"
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
