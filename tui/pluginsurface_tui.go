//go:build !gui

package tui

// A terminal draws no pixels: a surface plugin draws host.frame's cells (ADR 1792797354 §4.9).

func (pl *plugins) newSurfaceSink() (surfaceSink, any, bool) { return nil, nil, false }

func (v *pluginView) surfaceNative() (any, bool) { return nil, false }
