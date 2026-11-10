//go:build linux || windows

package hostprobe

import (
	"context"
	"testing"
)

// stubSmi points smi at answer (absent when "") for t.
func stubSmi(t *testing.T, answer string) {
	t.Helper()
	was := smi
	smi = func(context.Context, ...string) (string, bool) { return answer, answer != "" }
	t.Cleanup(func() { smi = was })
}

// nvidia-smi's answer gives the first GPU's name, VRAM and compute capability; no nvidia-smi, or an
// answer in another shape, gives none.
func TestNvidiaSmiIsReadForTheFirstGPU(t *testing.T) {
	for _, c := range []struct {
		name, answer string
		ok           bool
		want         GPU
	}{
		{"two GPUs", "NVIDIA GeForce RTX 5090, 32607, 12.0\nNVIDIA T4, 15360, 7.5\n", true,
			GPU{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 5090", VRAM: 32607 << 20, Compute: "12.0"}},
		{"no nvidia-smi", "", false, GPU{}},
		{"another shape", "garbage", false, GPU{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			stubSmi(t, c.answer)
			g, ok := nvidiaGPU(context.Background())
			if ok != c.ok || g != c.want {
				t.Fatalf("nvidiaGPU = %+v, %v; want %+v, %v", g, ok, c.want, c.ok)
			}
		})
	}
}
