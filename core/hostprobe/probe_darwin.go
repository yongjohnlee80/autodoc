package hostprobe

import (
	"context"
	"runtime"

	"golang.org/x/sys/unix"
)

// readGPU is Apple silicon's GPU, which shares the RAM (unified memory): no VRAM of its own. An
// Intel Mac's GPU is not read.
func readGPU(context.Context) GPU {
	if runtime.GOARCH == "arm64" {
		return GPU{Vendor: VendorApple}
	}
	return GPU{}
}

// readRAM is hw.memsize, in bytes; 0 when it can't be read.
func readRAM() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n)
}
