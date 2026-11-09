//go:build gui

package tui

// guiBuild is whether this test binary is the GUI build's (-tags gui).
func guiBuild() bool { return true }
