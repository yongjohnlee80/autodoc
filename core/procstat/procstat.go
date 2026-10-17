// Package procstat reads what one process uses of its machine: the daemon's for sys.stats, the
// app's for autodoc.app.stats (ADR 1792256101 §4.1). It needs nothing beyond the standard library.
// A size the platform cannot say is 0: Darwin has no current resident size without Mach's
// task_info, so its RSS is 0 and its PeakRSS is getrusage's.
package procstat

import (
	"os"
	"runtime"
	"runtime/metrics"
	"time"
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

// the runtime/metrics samples Read takes, in this order
const (
	metricTotal      = "/memory/classes/total:bytes"
	metricHeap       = "/memory/classes/heap/objects:bytes"
	metricGoroutines = "/sched/goroutines:goroutines"
)

// Read is this process's Stats now.
func Read() Stats {
	s := Stats{PID: os.Getpid(), Started: started, CPUs: runtime.NumCPU()}
	readRuntime(&s)
	readOS(&s)
	return s
}

// readRuntime fills Go's own sizes. A metric this toolchain does not have reads as zero.
func readRuntime(s *Stats) {
	samples := []metrics.Sample{{Name: metricTotal}, {Name: metricHeap}, {Name: metricGoroutines}}
	metrics.Read(samples)
	s.GoMemory = uint64Of(samples[0].Value)
	s.GoHeap = uint64Of(samples[1].Value)
	s.Goroutines = int(uint64Of(samples[2].Value))
}

func uint64Of(v metrics.Value) uint64 {
	if v.Kind() == metrics.KindUint64 {
		return v.Uint64()
	}
	return 0
}
