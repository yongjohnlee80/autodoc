//go:build linux || windows

package hostprobe

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// smi runs nvidia-smi with args and answers its output, or not ok when it isn't on PATH or fails
// within probeTimeout (a variable, so a test can stand in for it). exec.LookPath finds it as the
// OS does: nvidia-smi.exe through PATHEXT on Windows.
var smi = func(ctx context.Context, args ...string) (string, bool) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	return string(out), err == nil
}

// nvidiaGPU is the first NVIDIA GPU as nvidia-smi reports it: its name, VRAM and compute
// capability. Not ok when nvidia-smi isn't there, fails, or answers in a shape it doesn't read.
func nvidiaGPU(ctx context.Context) (GPU, bool) {
	out, ok := smi(ctx, "--query-gpu=name,memory.total,compute_cap", "--format=csv,noheader,nounits")
	if !ok {
		return GPU{}, false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n") // the first GPU
	f := strings.Split(first, ",")
	if len(f) != 3 {
		return GPU{}, false
	}
	g := GPU{Vendor: VendorNVIDIA, Name: strings.TrimSpace(f[0]), Compute: strings.TrimSpace(f[2])}
	if mib, err := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64); err == nil {
		g.VRAM = mib << 20
	}
	return g, true
}
