package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/profiling"
)

// shortRuntime points the runtime directory somewhere short enough for a monitor's socket.
func shortRuntime(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "adp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

func noted(c *scriptedCore, part string) bool {
	return slices.ContainsFunc(c.notes, func(n string) bool { return strings.Contains(n, part) })
}

// Profile the window starts the window's monitor in its default folder and stops it: the item's
// check and the status line's badge follow, the start's notice names the folder and says the
// profiles are shared as a log is, and the stop leaves its samples.
func TestTheWindowsProfilingStartsAndStops(t *testing.T) {
	run := shortRuntime(t)
	c := newScripted("kb")
	prof := profiling.New(context.Background(), "gui")
	t.Cleanup(func() { _ = prof.Close() })
	f := newProfiling(c, prof, nil)
	if st := f.State(); st["App.profilingWindow"] != false || st["App.profilingMark"] != "" {
		t.Fatalf("before a start: %v", st)
	}
	f.toggleWindow()
	dir := prof.Dir()
	if !strings.HasPrefix(dir, run) || c.set["App.profilingWindow"] != true || c.set["App.profilingMark"] != profilingMark {
		t.Fatalf("after a start: dir %q, state %v", dir, c.set)
	}
	if !noted(c, dir) || !noted(c, "pprof labels") {
		t.Errorf("the start's notice: %v", c.notes)
	}
	f.toggleWindow()
	if prof.Dir() != "" || c.set["App.profilingWindow"] != false || c.set["App.profilingMark"] != "" {
		t.Fatalf("after a stop: dir %q, state %v", prof.Dir(), c.set)
	}
	if _, err := os.Stat(filepath.Join(dir, "samples.jsonl")); err != nil {
		t.Errorf("no samples after the stop: %v", err)
	}
	if len(c.failed) != 0 {
		t.Errorf("failed: %v", c.failed)
	}
}

// A window launched with --profile shows its monitor on, and a program with no profiler says so.
func TestTheWindowsProfilingFromLaunchAndWithout(t *testing.T) {
	shortRuntime(t)
	prof := profiling.New(context.Background(), "gui")
	t.Cleanup(func() { _ = prof.Close() })
	if _, err := prof.Start(""); err != nil {
		t.Fatal(err)
	}
	if st := newProfiling(newScripted("kb"), prof, nil).State(); st["App.profilingWindow"] != true || st["App.profilingMark"] != profilingMark {
		t.Errorf("launched with --profile: %v", st)
	}
	c := newScripted("kb")
	newProfiling(c, nil, nil).toggleWindow()
	if !noted(c, "no profiler") {
		t.Errorf("no profiler: %v", c.notes)
	}
}

// Profile the backend starts and stops the daemon's monitor through sys.profile, the connection
// learns whether it already runs, and a refusal is said and changes nothing.
func TestTheBackendsProfiling(t *testing.T) {
	c := newScripted("kb")
	on := false
	c.answers["sys.profile"] = func(params []any) (any, error) {
		switch params[0] {
		case "start":
			on = true
		case "stop":
			on = false
		}
		dir := ""
		if on {
			dir = "/run/user/1/autodoc-profile-serve-9"
		}
		return map[string]any{"on": on, "dir": dir}, nil
	}
	f := newProfiling(c, nil, nil)
	f.toggleBackend()
	if c.set["App.profilingBackend"] != true || c.set["App.profilingMark"] != profilingMark || !noted(c, "autodoc-profile-serve-9") {
		t.Fatalf("after a start: %v, notes %v", c.set, c.notes)
	}
	f.toggleBackend()
	if c.set["App.profilingBackend"] != false || c.set["App.profilingMark"] != "" {
		t.Fatalf("after a stop: %v", c.set)
	}
	if want := []string{"sys.profile[start ]", "sys.profile[stop ]"}; !slices.Equal(c.calls, want) {
		t.Errorf("calls %v; want %v", c.calls, want)
	}

	on = true // another client started it
	f.Connected()
	if c.set["App.profilingBackend"] != true {
		t.Errorf("a connection to a daemon already profiling: %v", c.set)
	}

	c.answers["sys.profile"] = func([]any) (any, error) { return nil, errors.New("unknown method") }
	f.toggleBackend()
	if len(c.failed) != 1 || c.set["App.profilingBackend"] != true {
		t.Errorf("a refused stop: failed %v, state %v", c.failed, c.set)
	}
}

// With neither monitor running, a capture and the folder say to start one first; the folder opens
// the last monitor's, after its stop too.
func TestCaptureAndFolderNeedAMonitor(t *testing.T) {
	shortRuntime(t)
	c := newScripted("kb")
	var opened []string
	prof := profiling.New(context.Background(), "gui")
	t.Cleanup(func() { _ = prof.Close() })
	f := newProfiling(c, prof, func(_ context.Context, p string) error { opened = append(opened, p); return nil })
	f.captureCPU()
	f.openFolder()
	if len(c.notes) != 2 || !noted(c, "Start profiling") || len(opened) != 0 {
		t.Fatalf("with none running: notes %v, opened %v", c.notes, opened)
	}
	f.toggleWindow()
	dir := prof.Dir()
	f.toggleWindow()
	f.openFolder()
	if !slices.Equal(opened, []string{dir}) {
		t.Errorf("opened %v; want the last monitor's %s", opened, dir)
	}
}
