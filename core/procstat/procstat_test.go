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
	if s.RSS == 0 || s.PeakRSS < s.RSS {
		t.Fatalf("rss %d peak %d", s.RSS, s.PeakRSS)
	}
}
