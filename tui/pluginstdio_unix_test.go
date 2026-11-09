//go:build linux || darwin

package tui

import "syscall"

// setStdioPollable makes stdin and stdout non-blocking, as plugin.Serve does.
func setStdioPollable() {
	for _, fd := range []int{0, 1} {
		_ = syscall.SetNonblock(fd, true)
	}
}
