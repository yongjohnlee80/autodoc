package hostprobe

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysRoot is where /sys and /proc are read from: "/", or a fake tree in a test.
var sysRoot = "/"

// pciVendors are the PCI vendor ids sysfs names a display device's vendor by.
var pciVendors = map[string]string{"0x10de": VendorNVIDIA, "0x1002": VendorAMD, "0x8086": VendorIntel}

// readGPU is the machine's GPU from /sys/class/drm: a discrete one (NVIDIA, AMD) over an Intel one.
// AMD's VRAM is sysfs's mem_info_vram_total; NVIDIA's VRAM, name and compute capability are
// nvidia-smi's, when it is on PATH.
func readGPU(ctx context.Context) GPU {
	cards, _ := filepath.Glob(filepath.Join(sysRoot, "sys/class/drm/card*/device/vendor"))
	var g GPU
	for _, v := range cards {
		b, err := os.ReadFile(v)
		if err != nil {
			continue
		}
		vendor := pciVendors[strings.TrimSpace(string(b))]
		if vendor == "" || (g.Vendor != "" && vendor == VendorIntel) || g.Vendor == VendorNVIDIA || g.Vendor == VendorAMD {
			continue // no display vendor we know, or a discrete one is already found
		}
		g = GPU{Vendor: vendor}
		if vendor == VendorAMD {
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(v), "mem_info_vram_total")); err == nil {
				g.VRAM, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			}
		}
	}
	if g.Vendor == VendorNVIDIA {
		if n, ok := nvidiaGPU(ctx); ok {
			g = n
		}
	}
	return g
}

// readRAM is /proc/meminfo's MemTotal, in bytes; 0 when it can't be read.
func readRAM() int64 {
	f, err := os.Open(filepath.Join(sysRoot, "proc/meminfo"))
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			kib, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0
			}
			return kib << 10
		}
	}
	return 0
}
