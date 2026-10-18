package procstat

import "syscall"

// readOS is Linux's: getrusage for CPU and the peak, in KiB. The resident size now is diag's.
func readOS(s *Stats) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUSeconds = cpuSeconds(ru.Utime) + cpuSeconds(ru.Stime)
		s.PeakRSS = uint64(ru.Maxrss) * 1024 // Linux reports KiB
	}
}
