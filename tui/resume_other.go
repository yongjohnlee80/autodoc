//go:build !unix

package tui

import "os"

// resumeSignal is none: there is no SIGCONT here, and no program stops itself on Ctrl+Z.
var resumeSignal os.Signal
