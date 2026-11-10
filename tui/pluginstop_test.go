package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// exitedBy fails t unless run's process is gone now: what follows (a removal, a quit) came after it.
func exitedBy(t *testing.T, run *pluginRun, what string) {
	t.Helper()
	select {
	case <-run.exited:
	default:
		t.Fatalf("%s while %s still ran", what, run.e.m.Name)
	}
}

// TestARemovalWaitsForThePluginToExit: Remove… of an open plugin that ignores plugin.close and
// SIGTERM takes its folder away only once its process is gone, killed past its deadlines: on
// Windows a removal would stop at the first file in use.
func TestARemovalWaitsForThePluginToExit(t *testing.T) {
	t.Parallel()
	shorten(t, 30*time.Second, time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "deaf", "deaf", 30, 6)
	r, _ := pluginTUI(t, dir)
	run := r.openPlugin("deaf")
	if run == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitShown(t, "open 30x6")
	r.remove(t, dir, "deaf")
	exitedBy(t, run, "its folder was removed")
	if _, err := os.Stat(filepath.Join(dir, "deaf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's directory is still there: %v", err)
	}
}

// TestTheQuitWaitsForAPluginStillClosing: a plugin stopped before the quit (it did not answer in
// time), still within its close's deadlines, is waited for as AutoDoc quits, as an open one is:
// nothing of it runs after, and its folder can go.
func TestTheQuitWaitsForAPluginStillClosing(t *testing.T) {
	t.Parallel()
	shorten(t, time.Second, 2*time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "silent", "silent", 30, 6)
	r, _ := pluginTUI(t, dir)
	run := r.openPlugin("silent")
	if run == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitNotice(t, "silent did not answer in 1s")
	onLoop(r, func() bool { r.h.plugins.stopPlugins(); return true })
	exitedBy(t, run, "the quit was done")
}

// TestAnExitWaitIsBounded: a run whose shutdown never ends holds a removal or an update only past
// its close's deadlines and the margin, then says which plugin is still there; one already shut
// down holds nothing.
func TestAnExitWaitIsBounded(t *testing.T) {
	t.Parallel()
	done, stuck := &pluginRun{shutDone: make(chan struct{})}, &pluginRun{shutDone: make(chan struct{})}
	close(done.shutDone)
	stuck.e.m.Name, stuck.grace, stuck.termGrace = "stuck", 10*time.Millisecond, 10*time.Millisecond
	if err := awaitShutdown([]*pluginRun{done}); err != nil {
		t.Fatalf("a run shut down: %v", err)
	}
	start := time.Now()
	err := awaitShutdown([]*pluginRun{done, stuck})
	took := time.Since(start)
	if err == nil || err.Error() != "stuck did not exit within 2.02s: its folder was left as it is" {
		t.Fatalf("a run that never shuts down: %v", err)
	}
	if took < 2*time.Second || took > 10*time.Second {
		t.Fatalf("the wait took %s, want its bound, 2.02s", took)
	}
}

// TestARunThatEndedAsTheDeadlineCameEnded: a shutdown done when the deadline has fired too is no
// timeout, however select picks between the two (each try is a fresh pick).
func TestARunThatEndedAsTheDeadlineCameEnded(t *testing.T) {
	t.Parallel()
	done := &pluginRun{shutDone: make(chan struct{})}
	close(done.shutDone)
	for range 200 {
		fired := make(chan time.Time, 1)
		fired <- time.Now()
		if err := awaitShutdownBy([]*pluginRun{done}, fired, 0); err != nil {
			t.Fatalf("a run shut down, the deadline fired too: %v", err)
		}
	}
}
