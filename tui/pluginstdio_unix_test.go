//go:build linux || darwin

package tui

import (
	"os"
	"syscall"
)

// setStdioPollable makes stdin and stdout non-blocking, as plugin.Serve does.
func setStdioPollable() {
	for _, fd := range []int{0, 1} {
		_ = syscall.SetNonblock(fd, true)
	}
}

// testStdioFiles are the test plugin's stdin and stdout as plugin.Serve makes them on Unix:
// descriptors 0 and 1, non-blocking, so the link's deadlines hold.
func testStdioFiles() (in, out *os.File) {
	setStdioPollable()
	return os.NewFile(0, "stdin"), os.NewFile(1, "stdout")
}
