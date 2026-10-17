package procstat

import "syscall"

// readOS is Darwin's: getrusage for CPU and the peak. The resident size now needs Mach's
// task_info (cgo), so RSS stays 0.
func readOS(s *Stats) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUSeconds = cpuSeconds(ru.Utime) + cpuSeconds(ru.Stime)
		s.PeakRSS = uint64(ru.Maxrss) // Darwin reports bytes
	}
}
