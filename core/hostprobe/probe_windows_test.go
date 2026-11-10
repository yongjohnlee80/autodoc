package hostprobe

import (
	"context"
	"testing"
)

// On Windows the RAM is the machine's physical memory, in bytes, and the GPU is nvidia-smi's or
// unknown.
func TestWindowsReadsTheRAMAndAnNvidiaGPU(t *testing.T) {
	if ram := readRAM(); ram < 256<<20 || ram > 16<<40 {
		t.Fatalf("RAM %d: not a machine's memory in bytes", ram)
	}
	stubSmi(t, "NVIDIA RTX A2000, 6138, 8.6\n")
	if g := readGPU(context.Background()); g.Vendor != VendorNVIDIA || g.VRAM != 6138<<20 || g.Compute != "8.6" {
		t.Fatalf("with nvidia-smi: %+v", g)
	}
	stubSmi(t, "")
	if g := readGPU(context.Background()); g != (GPU{}) {
		t.Fatalf("without nvidia-smi the GPU is %+v, want unknown", g)
	}
}
