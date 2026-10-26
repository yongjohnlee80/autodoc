package hostprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/search/embed"
)

// fakeSys makes a sysfs and procfs tree under a temp root: one drm card per vendor id (with VRAM
// for each one given), and meminfo; it points sysRoot at it, and smi at answer (absent when "").
func fakeSys(t *testing.T, cards []string, vram map[string]string, meminfo, answer string) {
	t.Helper()
	root := t.TempDir()
	for i, v := range cards {
		dev := filepath.Join(root, "sys/class/drm", "card"+string(rune('0'+i)), "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dev, "vendor"), []byte(v+"\n"), 0o644)
		if b, ok := vram[v]; ok {
			os.WriteFile(filepath.Join(dev, "mem_info_vram_total"), []byte(b+"\n"), 0o644)
		}
	}
	if meminfo != "" {
		os.MkdirAll(filepath.Join(root, "proc"), 0o755)
		os.WriteFile(filepath.Join(root, "proc/meminfo"), []byte(meminfo), 0o644)
	}
	wasRoot, wasSmi := sysRoot, smi
	sysRoot = root
	smi = func(context.Context, ...string) (string, bool) { return answer, answer != "" }
	t.Cleanup(func() { sysRoot, smi = wasRoot, wasSmi })
}

const meminfo = "MemTotal:       65536000 kB\nMemFree:        1000 kB\n"

// The GPU and RAM as sysfs, procfs and nvidia-smi give them, a discrete GPU over an Intel one,
// and unknown (0, "") where nothing can be read; the budget counts only a GPU Ollama runs on.
func TestTheProbeReadsTheGPUAndTheRAM(t *testing.T) {
	ram := int64(65536000) << 10
	for _, c := range []struct {
		name    string
		cards   []string
		vram    map[string]string
		smi     string
		meminfo string
		gpu     GPU
		budget  embed.Budget
	}{
		{"NVIDIA with nvidia-smi", []string{"0x10de"}, nil, "NVIDIA GeForce RTX 5090, 32607, 12.0\n", meminfo,
			GPU{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 5090", VRAM: 32607 << 20, Compute: "12.0"},
			embed.Budget{VRAM: 32607 << 20, RAM: ram}},
		{"NVIDIA without nvidia-smi: VRAM unknown", []string{"0x10de"}, nil, "", meminfo,
			GPU{Vendor: VendorNVIDIA}, embed.Budget{RAM: ram}},
		{"AMD from sysfs", []string{"0x1002"}, map[string]string{"0x1002": "17163091968"}, "", meminfo,
			GPU{Vendor: VendorAMD, VRAM: 17163091968}, embed.Budget{VRAM: 17163091968, RAM: ram}},
		{"an Intel GPU and an AMD one: the AMD", []string{"0x8086", "0x1002"}, map[string]string{"0x1002": "8589934592"}, "", meminfo,
			GPU{Vendor: VendorAMD, VRAM: 8589934592}, embed.Budget{VRAM: 8589934592, RAM: ram}},
		{"an AMD GPU and then an Intel one: still the AMD", []string{"0x1002", "0x8086"}, map[string]string{"0x1002": "8589934592"}, "", meminfo,
			GPU{Vendor: VendorAMD, VRAM: 8589934592}, embed.Budget{VRAM: 8589934592, RAM: ram}},
		{"Intel alone: no VRAM in the budget", []string{"0x8086"}, nil, "", meminfo,
			GPU{Vendor: VendorIntel}, embed.Budget{RAM: ram}},
		{"no GPU, no meminfo: all unknown", nil, nil, "", "", GPU{}, embed.Budget{}},
		{"nvidia-smi answering nonsense", []string{"0x10de"}, nil, "garbage", meminfo,
			GPU{Vendor: VendorNVIDIA}, embed.Budget{RAM: ram}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeSys(t, c.cards, c.vram, c.meminfo, c.smi)
			h := Probe(context.Background(), t.TempDir(), "", nil)
			if h.GPU != c.gpu {
				t.Errorf("GPU %+v, want %+v", h.GPU, c.gpu)
			}
			if b := h.Budget(); b != c.budget {
				t.Errorf("budget %+v, want %+v", b, c.budget)
			}
			if h.Free <= 0 {
				t.Errorf("free space %d under a temp dir", h.Free)
			}
		})
	}
}

// Ollama's version is what /api/version answers, and "" when nothing answers, or not as Ollama.
func TestTheProbeAsksOllamaItsVersion(t *testing.T) {
	fakeSys(t, nil, nil, meminfo, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			w.Write([]byte(`{"version":"0.40.11"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if v := Probe(context.Background(), t.TempDir(), srv.URL, nil).Ollama; v != "0.40.11" {
		t.Errorf("version %q", v)
	}
	notOllama := httptest.NewServer(http.NotFoundHandler())
	defer notOllama.Close()
	if v := Probe(context.Background(), t.TempDir(), notOllama.URL, nil).Ollama; v != "" {
		t.Errorf("a server that is not Ollama: %q", v)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if v := Probe(context.Background(), t.TempDir(), gone.URL, nil).Ollama; v != "" {
		t.Errorf("nothing listening: %q", v)
	}
}
