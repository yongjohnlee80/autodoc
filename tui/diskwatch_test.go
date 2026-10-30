package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"
)

// diskwatch_test.go: open files follow the disk
// (docs/decisions/editor.md#open-files-follow-the-disk), on a daemon over real folders, changed by
// another program.

// onDisk is a TUI over a daemon whose workspace alpha is the folder root, a.md and b.md in it.
func onDisk(t *testing.T, opt Options) (*running, string) {
	t.Helper()
	root := fileDir(t, "a.md", "one\ntwo\nthree\n", "b.md", "bbb\n")
	d := startManaged(t, map[string]string{"alpha": root})
	opt.Workspace = "alpha"
	r := runTUI(t, NewSession(d.sock, nil), opt)
	r.s.WaitForText(t, "· alpha")
	r.prefsIn(t) // the cells set h.prefs (diskReload): the store's answer must be in before they do
	return r, root
}

// openOnDisk opens p and waits for it, clean.
func (r *running) openOnDisk(t *testing.T, p string) {
	t.Helper()
	r.h.p.Post(func() { r.h.openPath(p) })
	r.waitFile(t, p)
}

// rewrite writes text into p under root, as another program does.
func rewrite(t *testing.T, root, p, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, p), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// diskOf is p's buffer's disk state, read on the loop.
func diskOf(r *running, p string) diskState {
	return onLoop(r, func() diskState {
		if b := r.h.bufferOf(fileRef{"alpha", p}); b != nil {
			return b.file.disk
		}
		return diskSame
	})
}

// noWatch is a folder's driver without Watch: the poll fallback.
type noWatch struct{ vfs.FS }

// TestACleanFileFollowsTheDisk: another program rewrites an open, clean file: the buffer shows the
// new text, clean, the cursor still on its line; it is one undo step. The same with no native
// watcher, by polling.
func TestACleanFileFollowsTheDisk(t *testing.T) {
	for _, poll := range []bool{false, true} {
		t.Run(map[bool]string{false: "watch", true: "poll"}[poll], func(t *testing.T) {
			if poll {
				old := diskPollEvery
				diskPollEvery = 100 * time.Millisecond
				t.Cleanup(func() { diskPollEvery = old })
			}
			r, root := onDisk(t, Options{})
			if poll {
				onLoop(r, func() bool {
					r.h.diskFS = func(dir string) (vfs.FS, error) { fs, err := local.New(dir); return noWatch{fs}, err }
					return true
				})
			}
			r.openOnDisk(t, "a.md")
			wantMode := map[bool]string{false: diskWatching, true: diskPolling}[poll]
			if got := onLoop(r, func() string { return r.h.active.watch.mode }); got != wantMode {
				t.Fatalf("a.md's folder is followed by %q, want %q", got, wantMode)
			}
			onLoop(r, func() bool { r.h.core.SetLine(2, 0); return true }) // on "three" (rows from 0)
			rewrite(t, root, "a.md", "zero\none\ntwo\nthree\nfour\n")
			r.s.WaitFor(t, "the new text in the buffer", func(string) bool { return r.editorText() == "zero\none\ntwo\nthree\nfour\n" })
			if f, mod := r.file(), onLoop(r, r.h.core.Modified); f.dirty || mod || f.disk != diskSame {
				t.Fatalf("after the reload: dirty %v, modified %v, disk %v; want clean, as on disk", f.dirty, mod, f.disk)
			}
			if line := onLoop(r, func() int { return r.h.core.Cursor().Line }); line != 3 {
				t.Fatalf("the cursor is on line %d, want 4: still on \"three\"", line+1)
			}
			r.undo()
			r.wantText(t, "one undo", "one\ntwo\nthree\n")
		})
	}
}

// TestTheTUIsOwnSaveIsNoChange: saving a buffer reloads nothing and badges nothing, though its
// folder's event arrives while the save is out.
func TestTheTUIsOwnSaveIsNoChange(t *testing.T) {
	r, root := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	time.Sleep(diskWait + 500*time.Millisecond) // the event and its check, had they made a change
	if f := r.file(); f.disk != diskSame || f.dirty || !strings.HasPrefix(r.editorText(), "X") {
		t.Fatalf("after its own save: disk %v, dirty %v, text %q", f.disk, f.dirty, r.editorText())
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.md")); !strings.HasPrefix(string(b), "X") {
		t.Fatalf("on disk: %q", b)
	}
	if strings.Contains(r.s.String(), "changed on disk") {
		t.Fatal("its own save badged the file")
	}
}

// TestAModifiedFileIsAskedAboutOnEnter: a file changed on disk while its buffer has unsaved edits
// is badged at once, asked about nothing while it is typed in; shown again, it asks keep / reload /
// overwrite, and Reload is one u from the edits.
func TestAModifiedFileIsAskedAboutOnEnter(t *testing.T) {
	r, root := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	rewrite(t, root, "a.md", "one\ntwo\nthree\nfour\n")
	r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	if strings.Contains(r.s.String(), "while it had unsaved changes") || !strings.HasPrefix(r.editorText(), "X") {
		t.Fatalf("the shown, modified buffer was asked about, or changed:\n%s", r.s.String())
	}
	if !strings.Contains(r.file().title(englishText), "changed on disk") {
		t.Fatalf("the title %q has no badge", r.file().title(englishText))
	}

	r.openOnDisk(t, "b.md")
	r.openByPicker(t, "a.md") // shown again
	r.s.WaitForText(t, "while it had unsaved changes")
	r.keys(t, key('r'))
	r.s.WaitFor(t, "the disk's text", func(string) bool { return r.editorText() == "one\ntwo\nthree\nfour\n" })
	if f := r.file(); f.dirty || f.disk != diskSame {
		t.Fatalf("after Reload: dirty %v, disk %v", f.dirty, f.disk)
	}
	r.undo()
	r.s.WaitFor(t, "the edit back", func(string) bool { return strings.HasPrefix(r.editorText(), "X") })
}

// TestAFileDeletedOnDiskKeepsItsBuffer: an open file deleted on disk keeps its buffer and text,
// badged; a save creates it again; a save after someone else made it again is refused.
func TestAFileDeletedOnDiskKeepsItsBuffer(t *testing.T) {
	r, root := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a.md badged deleted", func(string) bool { return diskOf(r, "a.md") == diskDeleted })
	if r.editorText() != "one\ntwo\nthree\n" {
		t.Fatalf("the deleted file's text went: %q", r.editorText())
	}
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	if b, _ := os.ReadFile(filepath.Join(root, "a.md")); string(b) != "Xone\ntwo\nthree\n" || diskOf(r, "a.md") != diskSame {
		t.Fatalf("the save did not create it again: %q, disk %v", b, diskOf(r, "a.md"))
	}

	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a.md badged deleted again", func(string) bool { return diskOf(r, "a.md") == diskDeleted })
	r.typeInEditor(t, "Y")
	r.waitTyped(t)
	rewrite(t, root, "a.md", "someone else's\n")
	r.s.WaitFor(t, "the file made again seen", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	onLoop(r, func() bool { r.h.active.file.disk = diskDeleted; return true }) // as if the save beat the event
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "changed on disk since you opened it")
	if b, _ := os.ReadFile(filepath.Join(root, "a.md")); string(b) != "someone else's\n" {
		t.Fatalf("the save wrote over the file made again: %q", b)
	}
}

// TestABackgroundBufferFollowsTheDisk: a clean buffer not shown reloads through its own editor;
// the one shown is untouched.
func TestABackgroundBufferFollowsTheDisk(t *testing.T) {
	r, root := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	r.openOnDisk(t, "b.md")
	rewrite(t, root, "a.md", "new a\n")
	a := bufferOn2(r, "a.md")
	r.s.WaitFor(t, "a.md reloaded behind", func(string) bool {
		return onLoop(r, func() bool { return a.buf.Value() == "new a\n" && !a.file.dirty })
	})
	if r.editorText() != "bbb\n" || r.file().path != "b.md" {
		t.Fatalf("the buffer shown changed: %s %q", r.file().path, r.editorText())
	}
}

// bufferOn2 is p's buffer in alpha.
func bufferOn2(r *running, p string) *openBuffer {
	return onLoop(r, func() *openBuffer { return r.h.bufferOf(fileRef{"alpha", p}) })
}

// TestAskFirstBadgesACleanFile: with tui.disk_reload = ask, a clean file changed on disk is badged
// and kept, as a modified one is.
func TestAskFirstBadgesACleanFile(t *testing.T) {
	r, root := onDisk(t, Options{})
	onLoop(r, func() bool { r.h.prefs.diskReload = diskReloadAsk; return true })
	r.openOnDisk(t, "a.md")
	rewrite(t, root, "a.md", "changed\n")
	r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
	if r.editorText() != "one\ntwo\nthree\n" {
		t.Fatalf("asked first, it reloaded: %q", r.editorText())
	}
}

// TestBuffersInOneFolderShareAWatch: two buffers in a folder share its watch; closing both ends it.
func TestBuffersInOneFolderShareAWatch(t *testing.T) {
	r, _ := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	r.openOnDisk(t, "b.md")
	if n, refs := onLoop(r, func() int { return len(r.h.watches) }), onLoop(r, func() int { return r.h.active.watch.refs }); n != 1 || refs != 2 {
		t.Fatalf("%d watches, %d buffers in it; want one, shared by two", n, refs)
	}
	onLoop(r, func() bool {
		for _, b := range append([]*openBuffer(nil), r.h.buffers...) {
			r.h.dropBuffer(b)
		}
		return true
	})
	if n := onLoop(r, func() int { return len(r.h.watches) }); n != 0 {
		t.Fatalf("%d watches left after closing every buffer", n)
	}
}

// TestAFolderThatGoesAndComesBack: the open file's folder deleted ends its watch, and its buffer is
// badged deleted; made again, the watch starts again, and a change in it is picked up. The same
// with no native watcher: a poll's overflow (its listing failed) starts its run again.
func TestAFolderThatGoesAndComesBack(t *testing.T) {
	for _, poll := range []bool{false, true} {
		t.Run(map[bool]string{false: "watch", true: "poll"}[poll], func(t *testing.T) {
			if poll {
				old := diskPollEvery
				diskPollEvery = 100 * time.Millisecond
				t.Cleanup(func() { diskPollEvery = old })
			}
			folderGoesAndComesBack(t, poll)
		})
	}
}

func folderGoesAndComesBack(t *testing.T, poll bool) {
	r, root := onDisk(t, Options{})
	if poll {
		onLoop(r, func() bool {
			r.h.diskFS = func(dir string) (vfs.FS, error) { fs, err := local.New(dir); return noWatch{fs}, err }
			return true
		})
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	rewrite(t, root, "sub/c.md", "c\n")
	r.s.WaitFor(t, "sub/c.md listed", func(string) bool {
		return onLoop(r, func() bool { return slices.Contains(r.h.files.Paths, "sub/c.md") })
	})
	r.openOnDisk(t, "sub/c.md")
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "sub/c.md badged deleted", func(string) bool { return diskOf(r, "sub/c.md") == diskDeleted })
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	rewrite(t, root, "sub/c.md", "c again\n")
	deadline := time.Now().Add(8 * time.Second) // the watch starts again after its backoff
	for r.editorText() != "c again\n" || diskOf(r, "sub/c.md") != diskSame {
		if time.Now().After(deadline) {
			state := onLoop(r, func() string {
				var out []string
				for dir, w := range r.h.watches {
					out = append(out, fmt.Sprintf("%s: mode %q gen %d refs %d err %v", dir, w.mode, w.gen, w.refs, w.err))
				}
				return strings.Join(out, "; ")
			})
			t.Fatalf("sub/c.md not back: text %q, disk %v; watches: %s", r.editorText(), diskOf(r, "sub/c.md"), state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestACheckOverlappingASaveIsStale: a check whose read is out while a save starts and lands is
// stale when it answers: it changes nothing, and one fresh check runs.
func TestACheckOverlappingASaveIsStale(t *testing.T) {
	root := fileDir(t, "a.md", "one\n")
	d := startManaged(t, map[string]string{"alpha": root})
	sess := NewSession(d.sock, nil)
	held, release := make(chan struct{}, 1), make(chan struct{})
	var hold atomic.Bool
	var reads atomic.Int32
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.read" {
			reads.Add(1)
			if hold.CompareAndSwap(true, false) {
				held <- struct{}{}
				<-release
			}
		}
	})
	r := runTUI(t, sess, Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	unwatched(r)
	r.openOnDisk(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	hold.Store(true)
	onLoop(r, func() bool { r.h.diskSoon(r.h.active); return true })
	<-held // the check's read is out
	before := reads.Load()
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saved a.md")
	r.typeInEditor(t, "Y") // modified again: a stale read taking the save for an outside change would badge it
	r.s.WaitFor(t, "Y typed", func(string) bool {
		return onLoop(r, func() bool { return strings.HasPrefix(r.h.core.Value(), "Y") && r.h.core.Mode() == widget.ModeNormal })
	})
	close(release)
	r.s.WaitFor(t, "one fresh check", func(string) bool { return reads.Load() > before })
	time.Sleep(diskWait + 300*time.Millisecond)
	if f := r.file(); f.disk != diskSame || !strings.HasPrefix(r.editorText(), "YXone") {
		t.Fatalf("after the stale check: disk %v, text %q; want its own save not taken for a change", f.disk, r.editorText())
	}
}

// TestACheckWaitsForASave: a check asked for while the buffer's save is out reads nothing until the
// save answers, then runs once.
func TestACheckWaitsForASave(t *testing.T) {
	root := fileDir(t, "a.md", "one\n")
	d := startManaged(t, map[string]string{"alpha": root})
	sess := NewSession(d.sock, nil)
	held, release := make(chan struct{}, 1), make(chan struct{})
	var hold atomic.Bool
	var reads atomic.Int32
	sess.SetBeforeCall(func(method string, params []any) {
		switch method {
		case "doc.read":
			reads.Add(1)
		case "doc.write":
			if hold.CompareAndSwap(true, false) {
				held <- struct{}{}
				<-release
			}
		}
	})
	r := runTUI(t, sess, Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	unwatched(r)
	r.openOnDisk(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	hold.Store(true)
	r.keys(t, decltest.Ctrl('s'))
	<-held // the save is out
	before := reads.Load()
	onLoop(r, func() bool { r.h.diskSoon(r.h.active); return true })
	time.Sleep(diskWait + 400*time.Millisecond)
	if n := reads.Load() - before; n != 0 {
		t.Fatalf("%d reads while the save was out; want the check held", n)
	}
	close(release)
	r.s.WaitForText(t, "saved a.md")
	r.s.WaitFor(t, "the held check run", func(string) bool { return reads.Load()-before == 1 })
	time.Sleep(diskWait + 300*time.Millisecond)
	if n := reads.Load() - before; n != 1 {
		t.Fatalf("%d checks after the save; want the held one, once", n)
	}
}

// unwatched makes r's folders unwatchable: no event asks for a check, only the cell does.
func unwatched(r *running) {
	onLoop(r, func() bool {
		r.h.diskFS = func(string) (vfs.FS, error) { return nil, errors.New("not watched in this cell") }
		return true
	})
}

// TestAWatchLeavesNoHandleBehind: a folder's watch started and ended over and over leaves no file
// descriptor behind: each run closes its driver's handle on the folder.
func TestAWatchLeavesNoHandleBehind(t *testing.T) {
	if _, err := os.ReadDir("/proc/self/fd"); err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	r, _ := onDisk(t, Options{})
	r.openOnDisk(t, "a.md")
	fds := func() int { e, _ := os.ReadDir("/proc/self/fd"); return len(e) }
	cycle := func() {
		onLoop(r, func() bool { r.h.unwatchFile(r.h.active); r.h.watchFile(r.h.active); return true })
	}
	for range 5 { // warm: the runtime's own descriptors settle
		cycle()
	}
	time.Sleep(200 * time.Millisecond)
	before := fds()
	for range 20 {
		cycle()
	}
	deadline := time.Now().Add(3 * time.Second)
	for fds()-before > 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if grown := fds() - before; grown > 3 {
		t.Fatalf("%d more file descriptors after 20 watches started and ended", grown)
	}
}

// TestAFileOutsideEveryWorkspaceFollowsTheDisk (the verify-first of
// docs/decisions/plugins.md#opening-files-by-path-and-self-update: a remote note's
// cache file is outside every workspace): clean, it reloads from disk; with unsaved edits, it is
// badged and keeps them, never reloaded under the user.
func TestAFileOutsideEveryWorkspaceFollowsTheDisk(t *testing.T) {
	r, _ := onDisk(t, Options{})
	out := t.TempDir()
	abs := filepath.Join(out, "note.sql")
	if err := os.WriteFile(abs, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	r.waitOpen(t, "", abs)
	outside := func() diskState {
		return onLoop(r, func() diskState {
			if b := r.h.bufferOf(fileRef{"", abs}); b != nil {
				return b.file.disk
			}
			return diskSame
		})
	}
	rewrite(t, out, "note.sql", "SELECT 2;\n")
	r.s.WaitFor(t, "the clean buffer reloaded", func(string) bool { return r.editorText() == "SELECT 2;\n" })

	r.typeInEditor(t, "X")
	r.waitTyped(t)
	rewrite(t, out, "note.sql", "SELECT 3;\n")
	r.s.WaitFor(t, "the edited buffer badged", func(string) bool { return outside() == diskChanged })
	if got := r.editorText(); got == "SELECT 3;\n" || !strings.Contains(got, "X") {
		t.Fatalf("the edited buffer was reloaded: %q", got) // the X goes where the reload left the cursor
	}
}
