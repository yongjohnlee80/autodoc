//go:build !gui

package tui

// A terminal shows a surface's frames as a picture where it can (pluginsurface_cells.go); elsewhere
// a surface plugin draws host.frame's cells (docs/decisions/plugins.md#surfaces-without-pixels).

func (pl *plugins) newSurfaceSink() (surfaceSink, any, bool) { return pl.newCellSurfaceSink() }

func (v *pluginView) surfaceNative() (any, bool) { return nil, false }
