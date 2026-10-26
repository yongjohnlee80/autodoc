//go:build !linux && !darwin

package hostprobe

import "context"

// readGPU reads nothing here: the GPU is unknown.
func readGPU(context.Context) GPU { return GPU{} }

// readRAM reads nothing here: the RAM is unknown.
func readRAM() int64 { return 0 }
