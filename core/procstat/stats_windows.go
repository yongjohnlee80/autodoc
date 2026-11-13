package procstat

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// getProcessMemoryInfo is psapi's GetProcessMemoryInfo, which x/sys/windows doesn't wrap.
var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS: its sizes are SIZE_T, a pointer wide.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// readOS is Windows': GetProcessTimes for CPU, and GetProcessMemoryInfo's working set, the pages
// resident now and at most, for the resident sizes. A call that fails leaves its sizes 0.
func readOS(s *Stats) {
	self := windows.CurrentProcess()
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(self, &created, &exited, &kernel, &user) == nil {
		s.CPUSeconds = filetimeSeconds(kernel) + filetimeSeconds(user)
	}
	m := processMemoryCounters{}
	m.cb = uint32(unsafe.Sizeof(m))
	if ok, _, _ := getProcessMemoryInfo.Call(uintptr(self), uintptr(unsafe.Pointer(&m)), uintptr(m.cb)); ok != 0 {
		s.RSS = uint64(m.workingSetSize)
		s.PeakRSS = uint64(m.peakWorkingSetSize)
	}
}

// filetimeSeconds is a FILETIME that holds a duration (GetProcessTimes' kernel and user times), in
// 100-nanosecond intervals, as seconds.
func filetimeSeconds(f windows.Filetime) float64 {
	return float64(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) / 1e7
}
