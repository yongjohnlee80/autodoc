// Package procstat reads what one process uses of its machine: the daemon's for sys.stats, the
// app's for autodoc.app.stats (docs/decisions/editor.md#process-stats). It needs nothing beyond the
// standard library. A size the platform cannot say is 0: Darwin has no current resident size
// without Mach's task_info, so its RSS is 0 and its PeakRSS is getrusage's.
package procstat

import (
	"os"
	"runtime"
	"time"

	"github.com/yongjohnlee80/golib/diag"
)

// Stats is one process's use, read now.
type Stats struct {
	PID        int
	Started    time.Time // when this process loaded the package: its start, near enough
	RSS        uint64    // resident bytes now (Linux; 0 elsewhere)
	PeakRSS    uint64    // the most resident bytes so far, getrusage's maxrss in bytes
	CPUSeconds float64   // user + system CPU used so far: cumulative, never a rate
	GoMemory   uint64    // what the Go runtime has mapped
	GoHeap     uint64    // live heap objects
	Goroutines int
	CPUs       int
}

var started = time.Now()

// Read is this process's Stats now.
func Read() Stats {
	s := Stats{PID: os.Getpid(), Started: started, CPUs: runtime.NumCPU()}
	readRuntime(&s)
	readOS(&s)
	s.PeakRSS = max(s.PeakRSS, s.RSS) // two readings: a peak is never below now
	return s
}

// readRuntime fills Go's own sizes and the resident size from golib/diag's reader: one reading of
// the runtime's memory, and of /proc/self/statm on Linux, for :usage and for a --profile monitor.
func readRuntime(s *Stats) {
	d := diag.Read()
	s.GoMemory = d.GoTotal
	s.GoHeap = d.HeapObjs
	s.Goroutines = int(d.Goroutines)
	s.RSS = d.RSS
}
