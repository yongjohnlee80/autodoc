//go:build windows

package tui

// setStdioPollable has nothing to set: a Windows handle is not made non-blocking this way.
func setStdioPollable() {}
