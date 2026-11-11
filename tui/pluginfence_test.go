package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAnExpiredDeadlineHoldsEveryRunAfter: once the deadline has fired, a run after the first is
// asked whether it is done, never waited for. The deadline's channel holds one value, which the
// first run's select may consume (its shutdown done too, select picks either), and a wait on the
// drained channel would never end. Each try is a fresh pick.
func TestAnExpiredDeadlineHoldsEveryRunAfter(t *testing.T) {
	t.Parallel()
	done, stuck := &pluginRun{shutDone: make(chan struct{})}, &pluginRun{shutDone: make(chan struct{})}
	close(done.shutDone)
	stuck.e.m.Name = "stuck"
	for range 64 {
		fired := make(chan time.Time, 1)
		fired <- time.Now()
		got := make(chan error, 1)
		go func() { got <- awaitShutdownBy([]*pluginRun{done, stuck}, fired, 0) }()
		select {
		case err := <-got:
			if err == nil || err.Error() != "stuck did not exit within 0s: its folder was left as it is" {
				t.Fatalf("a deadline fired, the second run never shut down: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the first run's select took the deadline, and the second run was waited for, for ever")
		}
	}
}

// TestNoRunStartsFromAFolderBeingRemoved: a plugin opened again while its removal waits for the old
// run's exit is refused, saying why: it would run from a folder about to be deleted. The removal
// then ends as it would have, and the folder is free again after.
func TestNoRunStartsFromAFolderBeingRemoved(t *testing.T) {
	t.Parallel()
	shorten(t, 30*time.Second, 2*time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "deaf", "deaf", 30, 6)
	r, _ := pluginTUI(t, dir)
	old := r.openPlugin("deaf")
	if old == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitShown(t, "open 30x6")
	again := onLoop(r, func() *pluginRun {
		r.h.plugins.removePluginConfirmed(filepath.Join(dir, "deaf"))
		r.h.plugins.openPlugin("deaf") // within the old run's grace: its removal owns the folder
		return r.h.plugins.running["deaf"]
	})
	if again != nil {
		t.Fatal("the plugin started again from the folder its removal is deleting")
	}
	r.waitNotice(t, "its folder is being removed")
	r.waitNotice(t, "removed deaf")
	exitedBy(t, old, "its folder was removed")
	if _, err := os.Stat(filepath.Join(dir, "deaf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's directory is still there: %v", err)
	}
	if n := onLoop(r, func() int { return len(r.h.plugins.owned) }); n != 0 {
		t.Fatalf("%d folders still owned after the removal ended", n)
	}
}

// TestASecondRemovalOfAFolderIsRefused: a removal asked again while the first still waits does
// nothing and says so: the folder is owned by one operation at a time, so the first's end never
// releases a fence a second one still counts on, nor the second's the first's.
func TestASecondRemovalOfAFolderIsRefused(t *testing.T) {
	t.Parallel()
	shorten(t, 30*time.Second, 2*time.Second, time.Second)
	dir := t.TempDir()
	installTestPlugin(t, dir, "deaf", "deaf", 30, 6)
	r, _ := pluginTUI(t, dir)
	old := r.openPlugin("deaf")
	if old == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitShown(t, "open 30x6")
	onLoop(r, func() bool {
		r.h.plugins.removePluginConfirmed(filepath.Join(dir, "deaf"))
		r.h.plugins.removePluginConfirmed(filepath.Join(dir, "deaf"))
		return true
	})
	r.waitNotice(t, "its folder is being removed")
	r.waitNotice(t, "removed deaf")
	exitedBy(t, old, "its folder was removed")
	if n := onLoop(r, func() int { return len(r.h.plugins.owned) }); n != 0 {
		t.Fatalf("%d folders still owned after the removal ended", n)
	}
}

// TestNoRunStartsFromAFolderBeingUpdated: a plugin opened again while its update waits for the old
// run's exit, before the swap, is refused, saying why: it would run from the folder the swap moves
// away. The update then lands, and the folder is free again after.
func TestNoRunStartsFromAFolderBeingUpdated(t *testing.T) {
	t.Parallel()
	shorten(t, 30*time.Second, 3*time.Second, time.Second)
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	commitPluginAs(t, repo, "echo", "deaf", "Echo", "true")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	old := r.openPlugin("echo")
	if old == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitShown(t, "open 30x6")
	commitPluginAs(t, repo, "echo", "deaf", "Echo two", "true")
	r.h.p.Post(func() { _ = r.h.plugins.updatePlugin("echo") })
	r.question(t)
	r.keys(t, key('y'))
	// the build is done and the old run is stopping, held by its grace: the swap waits on it
	deadline := time.Now().Add(30 * time.Second)
	for !onLoop(r, func() bool { return old.closing }) {
		if time.Now().After(deadline) {
			t.Fatal("the update never stopped the old run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	again := onLoop(r, func() *pluginRun {
		r.h.plugins.openPlugin("echo")
		return r.h.plugins.running["echo"]
	})
	if again != nil {
		t.Fatal("the plugin started again from the folder its update is moving")
	}
	r.waitNotice(t, "its folder is being updated")
	r.waitNotice(t, "updated echo at")
	exitedBy(t, old, "its folder was swapped")
	if n := onLoop(r, func() int { return len(r.h.plugins.owned) }); n != 0 {
		t.Fatalf("%d folders still owned after the update ended", n)
	}
}

// TestAFailedRemovalReleasesItsFolder: a removal that fails (here a run that never exits within its
// bound) releases the folder it owned: the plugin starts again after, as it would have before.
func TestAFailedRemovalReleasesItsFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installTestPlugin(t, dir, "echo", "echo", 30, 6)
	r, _ := pluginTUI(t, dir)
	stuck := &pluginRun{shutDone: make(chan struct{}), grace: 10 * time.Millisecond, termGrace: 10 * time.Millisecond}
	stuck.e.dir, stuck.e.m.Name = filepath.Join(dir, "echo"), "echo"
	defer close(stuck.shutDone) // before the quit, which waits for every stopping run
	onLoop(r, func() bool {
		if r.h.plugins.stopping == nil {
			r.h.plugins.stopping = map[*pluginRun]struct{}{}
		}
		r.h.plugins.stopping[stuck] = struct{}{} // a run of it still in its close, that never ends
		r.h.plugins.removePluginConfirmed(filepath.Join(dir, "echo"))
		return true
	})
	r.waitNotice(t, "echo did not exit within")
	if n := onLoop(r, func() int { return len(r.h.plugins.owned) }); n != 0 {
		t.Fatalf("%d folders still owned after the removal failed", n)
	}
	if again := r.openPlugin("echo"); again == nil {
		t.Fatal("the plugin did not start again after its removal failed")
	}
	r.waitShown(t, "open 30x6")
	onLoop(r, func() bool { delete(r.h.plugins.stopping, stuck); return true })
}

// TestAFailedUpdateReleasesItsFolder: an update whose swap fails (here the older folder can't be
// removed) releases the folder it owned: the plugin, left as it was, starts again after.
func TestAFailedUpdateReleasesItsFolder(t *testing.T) {
	t.Parallel()
	tune(t).installFS = installFS{rename: os.Rename, removeAll: func(string) error { return errors.New("busy") }}
	repo := pluginRepo(t, "Echo", "true")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	commitPlugin(t, repo, "Echo two", "true")
	r.h.p.Post(func() { _ = r.h.plugins.updatePlugin("echo") })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "the plugin was left as it was")
	if n := onLoop(r, func() int { return len(r.h.plugins.owned) }); n != 0 {
		t.Fatalf("%d folders still owned after the update failed", n)
	}
	if again := r.openPlugin("echo"); again == nil {
		t.Fatal("the plugin did not start again after its update failed")
	}
	r.waitShown(t, "open 30x6")
}
