//go:build !windows

package tui

import (
	"os"
	"path/filepath"
)

// dirHeld reports whether a descriptor of this process names dir itself, where the OS lists them
// (Linux's /proc/self/fd); a descriptor on a folder inside dir doesn't count, as on Windows
// (dirheld_windows_test.go). Where nothing lists them, nothing is seen held.
func dirHeld(dir string) (bool, error) {
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return false, nil
	}
	for _, fd := range fds {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil && target == dir {
			return true, nil
		}
	}
	return false, nil
}
