// Package hostprobe reads what this machine offers an embedding model, without a network or a
// provider (docs/decisions/search.md#host-probe): its GPU (vendor, VRAM, compute capability), its
// RAM, the free space under the data directory, and whether Ollama answers. Nothing here fails:
// what can't be read is reported as unknown (0, ""), and the setup recommends by what is known.
package hostprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/golib/search/embed"
)

// Host is what this machine offers an embedding model, read now.
type Host struct {
	GPU    GPU
	RAM    int64  // bytes of memory; on macOS unified memory, the GPU's too; 0 unknown
	Free   int64  // bytes free under the data directory (store.FreeSpace); 0 unknown
	Ollama string // the version the Ollama at the probed URL reports; "" when nothing answers
}

// GPU is the machine's GPU as far as it can be read; Vendor "" for none found.
type GPU struct {
	Vendor  string // VendorNVIDIA, VendorAMD, VendorIntel or VendorApple
	Name    string // its name where the reading gives one (nvidia-smi); "" otherwise
	VRAM    int64  // bytes; AMD from sysfs, NVIDIA from nvidia-smi when it is on PATH; 0 unknown
	Compute string // NVIDIA's compute capability ("12.0"), from nvidia-smi; "" otherwise
}

// The GPU vendors the probe names.
const (
	VendorNVIDIA = "nvidia"
	VendorAMD    = "amd"
	VendorIntel  = "intel"
	VendorApple  = "apple"
)

// probeTimeout bounds each outside reading: nvidia-smi, and Ollama's /api/version.
const probeTimeout = 2 * time.Second

// Probe reads the host. dataDir is where the store lives (its free space); ollamaURL is the
// Ollama to ask for its version, with client (nil: http.DefaultClient).
func Probe(ctx context.Context, dataDir, ollamaURL string, client *http.Client) Host {
	h := Host{GPU: readGPU(ctx), RAM: readRAM()}
	if free, err := store.FreeSpace(dataDir); err == nil {
		h.Free = free
	}
	h.Ollama = ollamaVersion(ctx, ollamaURL, client)
	return h
}

// Budget is h as an embedding model's budget: the VRAM of a GPU Ollama runs models on (NVIDIA's,
// AMD's), and the RAM. On macOS the memory is unified, so it is all RAM; an Intel GPU is not one
// Ollama uses.
func (h Host) Budget() embed.Budget {
	b := embed.Budget{RAM: h.RAM}
	if h.GPU.Vendor == VendorNVIDIA || h.GPU.Vendor == VendorAMD {
		b.VRAM = h.GPU.VRAM
	}
	return b
}

// ollamaVersion is what /api/version answers at base, "" for no answer.
func ollamaVersion(ctx context.Context, base string, client *http.Client) string {
	if base == "" {
		return ""
	}
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/version", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&v) != nil {
		return ""
	}
	return v.Version
}
