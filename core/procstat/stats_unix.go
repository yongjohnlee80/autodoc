//go:build linux || darwin

package procstat

import (
	"syscall"
	"time"
)

// cpuSeconds is a getrusage time as seconds.
func cpuSeconds(tv syscall.Timeval) float64 {
	return time.Duration(tv.Nano()).Seconds()
}
