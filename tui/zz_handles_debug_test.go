package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// DEBUG (scratch, never merged): every process's handles on dir (handle.exe matches by name), and
// the TUI's watched folders.
func debugHandles(t *testing.T, when, dir string, h *Host) {
	if runtime.GOOS != "windows" || os.Getenv("HANDLE_EXE") == "" {
		return
	}
	tail := filepath.Join(filepath.Base(filepath.Dir(dir)), filepath.Base(dir))
	out, err := exec.Command(os.Getenv("HANDLE_EXE"), "-accepteula", "-nobanner", "-a", tail).CombinedOutput()
	var ws []string
	if h != nil {
		for d, w := range h.watches {
			ws = append(ws, d+" mode="+w.mode)
		}
		sort.Strings(ws)
	}
	t.Logf("DEBUG %s: HANDLES (every process) on %s (%v):\n%s\nWATCHES %v\nEND", when, tail, err, out, ws)
}
