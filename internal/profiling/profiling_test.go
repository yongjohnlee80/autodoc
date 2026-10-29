package profiling_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/profiling"
)

// shortDir is a directory short enough for a monitor's socket (t.TempDir's names a long test).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "adp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// A profiler starts, stops and starts again in one process; a start while one runs keeps it (one
// monitor per process); each stop leaves its samples written; Close at exit stops a running one.
func TestAProfilerStartsStopsAndStartsAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := profiling.New(ctx, "gui")
	if p.Dir() != "" || p.Stop() != nil {
		t.Fatal("a new profiler is not off")
	}
	first := filepath.Join(shortDir(t), "a")
	if dir, err := p.Start(first); err != nil || dir != first {
		t.Fatalf("start: %q, %v", dir, err)
	}
	if dir, err := p.Start(filepath.Join(shortDir(t), "b")); err != nil || dir != first {
		t.Errorf("a second start: %q, %v; want the running one, %s", dir, err, first)
	}
	if err := p.Stop(); err != nil || p.Dir() != "" {
		t.Fatalf("stop: %v, dir %q", err, p.Dir())
	}
	if _, err := os.Stat(filepath.Join(first, "samples.jsonl")); err != nil {
		t.Errorf("no samples after the stop: %v", err)
	}
	second := filepath.Join(shortDir(t), "c")
	if dir, err := p.Start(second); err != nil || dir != second {
		t.Fatalf("a start after a stop: %q, %v", dir, err)
	}
	if err := p.Close(); err != nil || p.Dir() != "" {
		t.Errorf("close: %v, dir %q", err, p.Dir())
	}
	if _, err := os.Stat(filepath.Join(second, profiling.SocketName)); !os.IsNotExist(err) {
		t.Errorf("the socket outlived the monitor: %v", err)
	}
}

// Two profilers on one directory: the second is refused (diag's lock), and the first runs on.
func TestASecondMonitorOnADirectoryIsRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := filepath.Join(shortDir(t), "d")
	a, b := profiling.New(ctx, "gui"), profiling.New(ctx, "serve")
	if _, err := a.Start(dir); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := b.Start(dir); err == nil || b.Dir() != "" {
		t.Errorf("a second monitor on %s: %v, dir %q", dir, err, b.Dir())
	}
	if a.Dir() != dir {
		t.Errorf("the first monitor stopped: %q", a.Dir())
	}
}

// A directory whose socket path is over the unix socket limit is refused before anything is made,
// saying so; the default directory is in the runtime directory, named for the mode and the process.
func TestTheMonitorsDirectory(t *testing.T) {
	long := filepath.Join(shortDir(t), strings.Repeat("x", 120))
	if _, err := profiling.New(context.Background(), "gui").Start(long); !errors.Is(err, profiling.ErrSocketTooLong) {
		t.Errorf("a long directory: %v, want ErrSocketTooLong", err)
	}
	if _, err := os.Stat(long); !os.IsNotExist(err) {
		t.Errorf("the refused directory was made: %v", err)
	}
	run := shortDir(t)
	t.Setenv("XDG_RUNTIME_DIR", run)
	dir, err := profiling.DefaultDir("gui")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(run, "autodoc-profile-gui-"); !strings.HasPrefix(dir, want) || !strings.HasSuffix(dir, "-"+strconv.Itoa(os.Getpid())) {
		t.Errorf("default directory %s; want %s<pid>", dir, want)
	}
	p := profiling.New(context.Background(), "gui")
	got, err := p.Start("")
	if err != nil || got != dir {
		t.Fatalf("a start with no directory: %q, %v; want %s", got, err, dir)
	}
	defer p.Close()
}

// Fetch writes a profile from the running monitor's socket into its directory, and nothing is left
// behind when no monitor serves it; a CPU or goroutine profile holds labels, a heap profile not.
func TestFetchWritesAProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := profiling.New(ctx, "gui")
	dir, err := p.Start(filepath.Join(shortDir(t), "f"))
	if err != nil {
		t.Fatal(err)
	}
	path, err := profiling.Fetch(ctx, profiling.Goroutine, dir, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 || filepath.Dir(path) != dir {
		t.Errorf("fetched %s: %v", path, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := profiling.Fetch(ctx, profiling.Heap, dir, time.Unix(60, 0)); err == nil {
		t.Error("a fetch with no monitor serving succeeded")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "fetched-heap-*"))
	if len(left) != 0 {
		t.Errorf("a failed fetch left %v", left)
	}
	if !profiling.HoldsLabels(profiling.CPU) || !profiling.HoldsLabels(profiling.Goroutine) || profiling.HoldsLabels(profiling.Heap) {
		t.Error("HoldsLabels: want cpu and goroutine alone")
	}
}

// Close is for good: a Start after it, as a start queued before the process ended may run, is
// ErrClosed and leaves no monitor, socket or directory; Stop still toggles before it.
func TestCloseIsTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := profiling.New(ctx, "gui")
	first := filepath.Join(shortDir(t), "a")
	if _, err := p.Start(first); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(first); err != nil {
		t.Fatalf("a start after a stop: %v", err)
	}
	if err := p.Close(); err != nil || p.Dir() != "" {
		t.Fatalf("close: %v, dir %q", err, p.Dir())
	}
	late := filepath.Join(shortDir(t), "late")
	if dir, err := p.Start(late); !errors.Is(err, profiling.ErrClosed) || dir != "" || p.Dir() != "" {
		t.Errorf("a start after close: %q, %v, dir %q; want ErrClosed and nothing", dir, err, p.Dir())
	}
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Errorf("a start after close made its directory: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("a second close: %v", err)
	}
}
