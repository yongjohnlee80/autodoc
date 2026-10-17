package procstat

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// readOS is Linux's: /proc/self/statm for the resident size now, getrusage for CPU and the peak.
// The two are separate readings, and the kernel's peak can trail the pages counted a moment
// before, so the peak is held to at least the resident size read: a peak is never below now.
func readOS(s *Stats) {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		s.RSS = residentPages(b) * uint64(os.Getpagesize())
	}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUSeconds = cpuSeconds(ru.Utime) + cpuSeconds(ru.Stime)
		s.PeakRSS = uint64(ru.Maxrss) * 1024 // Linux reports KiB
	}
	s.PeakRSS = max(s.PeakRSS, s.RSS)
}

// residentPages is statm's second field: the resident pages; 0 when it cannot be read.
func residentPages(statm []byte) uint64 {
	fields := bytes.Fields(statm)
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
