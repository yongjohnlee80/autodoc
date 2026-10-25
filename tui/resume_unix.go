//go:build unix

package tui

import (
	"os"
	"syscall"
)

// resumeSignal is what Resume sends a tab's program: SIGCONT, which a stopped program runs again on.
var resumeSignal os.Signal = syscall.SIGCONT
