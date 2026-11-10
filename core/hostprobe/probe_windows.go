package hostprobe

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

// globalMemoryStatusEx is kernel32's GlobalMemoryStatusEx, which x/sys/windows doesn't wrap.
var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// readGPU is an NVIDIA GPU as nvidia-smi reports it, which an NVIDIA driver puts on PATH. Any other
// GPU is unknown here: naming an AMD or Intel one, and its VRAM, would take DXGI.
func readGPU(ctx context.Context) GPU {
	g, _ := nvidiaGPU(ctx)
	return g
}

// readRAM is GlobalMemoryStatusEx's total physical memory, in bytes; 0 when it can't be read.
func readRAM() int64 {
	m := memoryStatusEx{}
	m.length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return 0
	}
	return int64(m.totalPhys)
}
