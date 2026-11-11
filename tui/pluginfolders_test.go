package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// ownedLen is how many plugin folders a removal or an update owns.
func (f *folderFence) ownedLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.owned)
}

// waitingOn is how many removals and updates wait for dir's reads.
func (f *folderFence) waitingOn(dir string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waiting[dir]
}

// heldRead is a plugin folder's next read, held: armed, the next git a read of the folder runs
// waits there until released, then runs. While it is held it records whether the folder is still
// in place, and at its release, whether it still is: a removal or an update that did not wait for
// the read changed it under the read (on Windows, under git's working folder).
type heldRead struct {
	dir      string
	mu       sync.Mutex
	armed    bool
	started  chan struct{}
	release  chan struct{}
	present  bool // the folder was in place when the held read was released
	released bool
}

// holdReads arms nothing yet: the program's reads of dir run git as they would, until arm.
func holdReads(t *testing.T, dir string) *heldRead {
	t.Helper()
	h := &heldRead{dir: dir, started: make(chan struct{}), release: make(chan struct{})}
	tune(t).pluginGit = func(ctx context.Context, env procenv.Env, d string, args ...string) (string, error) {
		h.mu.Lock()
		hold := h.armed && d == h.dir
		h.armed = h.armed && !hold
		h.mu.Unlock()
		if hold {
			close(h.started)
			<-h.release
			_, err := os.Stat(d)
			h.mu.Lock()
			h.present = err == nil
			h.mu.Unlock()
		}
		return git(ctx, env, pluginCloneTimeout, d, args...)
	}
	t.Cleanup(h.free) // a read still held at the test's end is let go, so its program can quit
	return h
}

func (h *heldRead) arm() {
	h.mu.Lock()
	h.armed = true
	h.mu.Unlock()
}

func (h *heldRead) wasPresent() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.present
}

func (h *heldRead) free() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.released {
		h.released = true
		close(h.release)
	}
}

// waitStarted waits until the armed read is held.
func (h *heldRead) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-h.started:
	case <-time.After(20 * time.Second):
		t.Fatal("the folder's read never started")
	}
}

// heldManagerRead adds the test plugin echo from repo, then holds the manager's next read of its
// folder (its listing's git), started and held when it returns.
func heldManagerRead(t *testing.T, repo string) (*running, string, *heldRead) {
	t.Helper()
	dir := t.TempDir()
	h := holdReads(t, filepath.Join(dir, "echo"))
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	h.arm()
	r.h.p.Post(r.h.plugins.loadManaged) // the manager's listing reads the folder: held there
	h.waitStarted(t)
	return r, dir, h
}

// waitHeldByTheRead waits until the operation on folder waits for the held read, or did not: it
// changed the folder (gone, or swapped out to .previous), or failed, which on Windows it may when a
// read's git has only just exited. Either is the cell's defect, which fails at once.
func waitHeldByTheRead(t *testing.T, r *running, folder, op string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for r.h.plugins.folders.waitingOn(folder) == 0 {
		if failed := onLoop(r, func() string {
			for _, n := range r.h.noticeLog.Items {
				if strings.Contains(n.Text, "not removed") || strings.Contains(n.Text, "not installed") {
					return n.Text
				}
			}
			return ""
		}); failed != "" {
			t.Fatalf("the %s did not wait for the read of its folder, and failed: %q", op, failed)
		}
		if _, err := os.Stat(folder); err != nil {
			t.Fatalf("the %s changed the plugin's folder while a read of it ran: %v", op, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(folder), previousDir, filepath.Base(folder))); err == nil {
			t.Fatalf("the %s swapped the plugin's folder out while a read of it ran", op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s never waited for the read of its folder", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestARemovalWaitsForAReadOfItsFolder: a plugin removed while the manager's listing reads its
// folder (git, run in it: on Windows its working folder, which can't be removed while it runs) is
// removed once the read has ended, never under it. The read ends as it would have, and the removal
// says so once.
func TestARemovalWaitsForAReadOfItsFolder(t *testing.T) {
	t.Parallel()
	r, dir, h := heldManagerRead(t, pluginRepo(t, "Echo", "true"))
	folder := filepath.Join(dir, "echo")
	onLoop(r, func() bool { r.h.plugins.removePluginConfirmed(folder); return true })
	waitHeldByTheRead(t, r, folder, "removal")
	h.free()
	r.waitNotice(t, "removed echo")
	if !h.wasPresent() {
		t.Fatal("the read's folder was gone when it was released")
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's folder is still there: %v", err)
	}
	if n := r.noticesSaying("removed echo"); n != 1 {
		t.Fatalf("the removal said so %d times", n)
	}
	if n := r.noticesSaying("not removed"); n != 0 {
		t.Fatalf("the removal also failed %d times", n)
	}
	if n := onLoop(r, func() int { return r.h.plugins.folders.ownedLen() }); n != 0 {
		t.Fatalf("%d folders still owned after the removal ended", n)
	}
}

// TestAnUpdateWaitsForAReadOfItsFolder: an update whose swap comes while the manager's listing reads
// the plugin's folder swaps it once the read has ended, never under it.
func TestAnUpdateWaitsForAReadOfItsFolder(t *testing.T) {
	t.Parallel()
	repo := pluginRepo(t, "Echo", "true")
	dir := t.TempDir()
	folder := filepath.Join(dir, "echo")
	h := holdReads(t, folder)
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	commitPlugin(t, repo, "Echo two", "true")
	r.h.p.Post(func() { _ = r.h.plugins.updatePlugin("echo") })
	r.question(t) // the fetch's own reads ran: the update asks
	h.arm()
	r.h.p.Post(r.h.plugins.loadManaged) // the manager's listing, no dialog over the question
	h.waitStarted(t)
	r.keys(t, key('y'))
	waitHeldByTheRead(t, r, folder, "update")
	h.free()
	r.waitNotice(t, "updated echo at")
	if !h.wasPresent() {
		t.Fatal("the read's folder was gone when it was released")
	}
	if n := r.noticesSaying("not installed"); n != 0 {
		t.Fatalf("the update also failed %d times", n)
	}
	if n := onLoop(r, func() int { return r.h.plugins.folders.ownedLen() }); n != 0 {
		t.Fatalf("%d folders still owned after the update ended", n)
	}
}

// TestARemovalGivesUpOnAReadThatDoesNotEnd: a read of the folder that outlasts the removal's bound
// fails the removal, saying a read of the folder did not end, the folder left as it is, rather than
// waiting for ever. The folder is released: once the read has ended, the plugin can be removed.
func TestARemovalGivesUpOnAReadThatDoesNotEnd(t *testing.T) {
	t.Parallel()
	tune(t).folderReadWait = 200 * time.Millisecond
	r, dir, h := heldManagerRead(t, pluginRepo(t, "Echo", "true"))
	folder := filepath.Join(dir, "echo")
	onLoop(r, func() bool { r.h.plugins.removePluginConfirmed(folder); return true })
	r.waitNotice(t, "a read of its folder did not end within 200ms: its folder was left as it is")
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("the folder a read still held was changed: %v", err)
	}
	if n := onLoop(r, func() int { return r.h.plugins.folders.ownedLen() }); n != 0 {
		t.Fatalf("%d folders still owned after the removal failed", n)
	}
	h.free()
	select { // the listing's reads end (its second git runs unheld), then nothing holds the folder
	case <-r.h.plugins.folders.readsEnded(folder):
	case <-time.After(20 * time.Second):
		t.Fatal("the released read never ended")
	}
	onLoop(r, func() bool { r.h.plugins.removePluginConfirmed(folder); return true })
	r.waitNotice(t, "removed echo")
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's folder is still there: %v", err)
	}
}

// TestAPluginsLuaPackageLeavesItsFolderToUpdateAndRemove: a plugin with a Lua package, its module
// read, is updated and removed in the app, on every OS: the update's new package runs, and the
// removal takes the plugin's folder, lua/ with it. On Windows a folder still held could be neither
// replaced nor removed: by the package's reads (each opens the folder and closes it again), or by
// the manager's reads of it, its listing's git (the fence waits for them).
func TestAPluginsLuaPackageLeavesItsFolderToUpdateAndRemove(t *testing.T) {
	t.Parallel()
	repo := pluginRepo(t, "Echo", "true")
	commitLua := func(label string) {
		t.Helper()
		writeFiles(t, repo, map[string]string{
			"lua/package.toml": "name = \"echo\"\n",
			"lua/mod.lua":      `return "lua-` + label + `"`,
			"lua/init.lua":     `autodoc.command{ name = "echolua", run = function(ed) ed:insert(require("mod")) end }`,
		})
		runGit(t, repo, "add", "lua")
		runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "lua "+label)
	}
	commitLua("one")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	runs := func(want string) {
		t.Helper()
		r.s.WaitFor(t, "the package's command saying "+want, func(string) bool {
			r.page("", 1, 1)
			return r.luaRun("echolua") == nil && r.editorText() == want
		})
	}
	runs("lua-one")

	r.h.p.Post(r.h.plugins.managePlugins)
	echo := func() int {
		return onLoop(r, func() int {
			for i, m := range r.h.plugins.managedPlugins.All() {
				if m.e.m.Name == "echo" {
					return i
				}
			}
			return -1
		})
	}
	r.s.WaitFor(t, "echo managed", func(string) bool { return echo() >= 0 })
	commitLua("two")
	i := echo()
	r.h.p.Post(func() { r.h.plugins.startUpdatePlugin(i) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "updated echo at")
	runs("lua-two")

	i = echo()
	r.h.p.Post(func() { r.h.plugins.startRemovePlugin(i) })
	r.s.WaitForText(t, "remove the plugin?")
	r.keys(t, key('y'))
	r.waitNotice(t, "removed echo")
	if _, err := os.Stat(filepath.Join(dir, "echo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plugin's folder is still there: %v", err)
	}
}

// TestAReadNeverStartsInAnOwnedFolder: a read of a folder a removal or an update owns runs nothing
// and says what owns it; once the folder is released, it runs. A removal waits for a read already
// running, and for nothing when none is.
func TestAReadNeverStartsInAnOwnedFolder(t *testing.T) {
	t.Parallel()
	var f folderFence
	dir := filepath.Join(t.TempDir(), "echo")
	if ok, _ := f.own(dir, "removed"); !ok {
		t.Fatal("a free folder was refused")
	}
	if ok, other := f.own(dir, "updated"); ok || other != "removed" {
		t.Fatalf("an owned folder was owned again: %v %q", ok, other)
	}
	ran := false
	if what, ok := f.read(dir, func() { ran = true }); ok || ran || what != "removed" {
		t.Fatalf("a read ran in an owned folder: %v %v %q", ok, ran, what)
	}
	f.disown(dir)
	if _, ok := f.read(dir, func() { ran = true }); !ok || !ran {
		t.Fatal("a read did not run in a released folder")
	}
	if err := f.awaitReads(context.Background(), dir, time.Hour); err != nil {
		t.Fatalf("a removal waited for a read when none ran: %v", err)
	}
	in, out := make(chan struct{}), make(chan struct{})
	go f.read(dir, func() { close(in); <-out })
	<-in
	got := make(chan error, 1)
	go func() { got <- f.awaitReads(context.Background(), dir, time.Hour) }()
	select {
	case err := <-got:
		t.Fatalf("a removal did not wait for the read running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(out)
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the read ended, the removal failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the read ended, the removal still waited")
	}
}
