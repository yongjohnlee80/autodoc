package procstat

import (
	"os"
	"runtime"
	"testing"
)

func TestReadThisProcess(t *testing.T) {
	s := Read()
	if s.PID != os.Getpid() {
		t.Fatalf("pid %d, want %d", s.PID, os.Getpid())
	}
	if s.CPUs != runtime.NumCPU() || s.Goroutines < 1 || s.GoMemory == 0 || s.GoHeap == 0 {
		t.Fatalf("runtime sizes: %+v", s)
	}
	if s.Started.IsZero() || s.Started != Read().Started {
		t.Fatalf("started moves: %v", s.Started)
	}
}

func TestCPUSecondsRise(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no getrusage here")
	}
	before := Read().CPUSeconds
	x := 0
	for i := 0; Read().CPUSeconds-before < 0.02 && i < 1<<30; i++ {
		x += i % 7
	}
	if after := Read().CPUSeconds; after <= before {
		t.Fatalf("cpu seconds %v then %v (x %d)", before, after, x)
	}
}

func TestResidentSizeOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS is Linux's")
	}
	s := Read()
	// bytes, not KiB or pages: a Go test binary is more than a MiB resident and far less than 64 GiB
	if s.RSS < 1<<20 || s.RSS > 64<<30 || s.PeakRSS < 1<<20 || s.PeakRSS > 64<<30 {
		t.Fatalf("rss %d peak %d: not sizes in bytes", s.RSS, s.PeakRSS)
	}
	// readOS holds the peak to at least the resident size it read (two readings, the kernel's peak
	// trailing): the contract, not an ordering of separate observations
	if s.PeakRSS < s.RSS {
		t.Fatalf("peak %d below the resident %d", s.PeakRSS, s.RSS)
	}
}
