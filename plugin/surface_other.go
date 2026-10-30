//go:build !linux

package plugin

import (
	"context"
	"errors"
	"image"
)

// Surface is Linux's first (docs/decisions/plugins.md#pixel-surfaces): elsewhere a plugin has none, and draws cells.
type Surface struct{}

// SurfaceBuffer is one buffer to draw into; there are none here.
type SurfaceBuffer struct {
	ID, Gen     int
	W, H, Pitch int
	Format      string
	Pix         []byte
}

var errNoSurface = errors.New("plugin: pixel surfaces are Linux's first")

func (*Surface) Info() SurfaceInfo                              { return SurfaceInfo{} }
func (*Surface) Done() <-chan SurfaceFrameDone                  { return nil }
func (*Surface) Buffer(context.Context) (*SurfaceBuffer, error) { return nil, errNoSurface }
func (*Surface) Present(*SurfaceBuffer, image.Rectangle, int64) (uint64, error) {
	return 0, errNoSurface
}
func (*Surface) setInfo(SurfaceInfo) {}
func openSurface() *Surface          { return nil }

func (*Surface) Close() {}
