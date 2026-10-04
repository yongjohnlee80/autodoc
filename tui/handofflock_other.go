//go:build !unix

package tui

// lockHandoff is a no-op where there is no flock: the TUI's daemon runs on unix.
func lockHandoff(string, bool) (func(), error) { return func() {}, nil }
