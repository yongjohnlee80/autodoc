package tui

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// restarted is the TUI on a daemon at a socket of its own, and a way to stop that daemon and start
// another there: the same one restarted (its workspaces' files kept), or another.
type restarted struct {
	sock  string
	d     *daemon
	r     *running
	reads atomic.Int32 // the TUI's doc.read calls
}

func onARestartableDaemon(t *testing.T, notes map[string][]string) *restarted {
	t.Helper()
	sock := filepath.Join(shortDir(t), "s.sock")
	x := &restarted{sock: sock, d: startDaemonWith(t, sock, notes, daemonOpts{version: "v-1"})}
	sess := NewSession(sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "doc.read" {
			x.reads.Add(1)
		}
	}
	x.r = runTUI(t, sess, Options{})
	x.r.s.WaitForText(t, "connected — autodoc v-1")
	return x
}

// restart stops the daemon and starts another serving notes, over the first one's files when
// keep is true; it waits until the TUI has entered a workspace on it.
func (x *restarted) restart(t *testing.T, notes map[string][]string, keep bool) {
	t.Helper()
	o := daemonOpts{version: "v-2"}
	if keep {
		o.roots = x.d.fs
	}
	x.d.stop()
	x.d = startDaemonWith(t, x.sock, notes, o)
	x.r.s.WaitFor(t, "the TUI on the next daemon", func(string) bool {
		return onLoop(x.r, func() bool {
			return x.r.h.connected && x.r.h.session.Version() == "v-2" && x.r.h.entered && len(x.r.h.filesAll) > 0
		})
	})
}

func (r *running) cursor() [2]int {
	return onLoop(r, func() [2]int { a, b := r.h.editor.Line(); return [2]int{a, b} })
}

// TestUnsavedEditsSurviveAReconnect: a file with unsaved edits stays open over a restart of its
// daemon, its text, its unsaved mark and its cursor as they were, and a save afterwards writes it
// over the version it was read at.
func TestUnsavedEditsSurviveAReconnect(t *testing.T) {
	notes := map[string][]string{"kb": {"a.md", "# A\n\nfirst\n\nsecond\n"}}
	x := onARestartableDaemon(t, notes)
	x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
	x.r.waitFile(t, "a.md")
	x.r.h.p.Post(func() { x.r.h.editor.SetLine(4, 0) })
	x.r.typeOnPage(t, "unsaved ")
	text, at := x.r.editorText(), x.r.cursor()
	reads := x.reads.Load()
	x.restart(t, notes, true)
	x.r.s.WaitFor(t, "the file checked against the disk", func(string) bool { return x.reads.Load() > reads })
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		// while the check answers, and after
		if f := x.r.file(); !f.open || f.path != "a.md" || !f.dirty || x.r.editorText() != text || x.r.cursor() != at {
			t.Fatalf("after the reconnect: open %v %q dirty %v, text %q at %v; want %q at %v", f.open, f.path, f.dirty, x.r.editorText(), x.r.cursor(), text, at)
		}
	}
	x.r.h.p.Post(x.r.h.save)
	x.r.waitNoticed(t, "saved a.md")
	if got := x.d.read(t, "kb", "a.md"); got != text {
		t.Fatalf("the save wrote %q, want %q", got, text)
	}
}

// TestAChangeOnDiskMeetsUnsavedEditsAtTheSave: a file with unsaved edits that changed on disk while
// the backend was away keeps the edits, says so, and its save asks before it overwrites the disk's
// version, as any conflicting save does.
func TestAChangeOnDiskMeetsUnsavedEditsAtTheSave(t *testing.T) {
	notes := map[string][]string{"kb": {"a.md", "# A\n\nfirst\n"}}
	x := onARestartableDaemon(t, notes)
	x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
	x.r.waitFile(t, "a.md")
	x.r.typeOnPage(t, "unsaved ")
	text := x.r.editorText()
	x.d.stop()
	x.d.write(t, "kb", "a.md", "# A\n\nchanged while the backend was away\n")
	x.d = startDaemonWith(t, x.sock, notes, daemonOpts{version: "v-2", roots: x.d.fs})
	x.r.waitNoticed(t, "a.md changed on disk while the backend was away: a save asks before it overwrites it")
	if f := x.r.file(); !f.open || !f.dirty || x.r.editorText() != text {
		t.Fatalf("the edits went: open %v dirty %v, %q", f.open, f.dirty, x.r.editorText())
	}
	x.r.h.p.Post(x.r.h.save)
	x.r.s.WaitFor(t, "the conflict question", func(sc string) bool { return strings.Contains(flat(sc), "a.md changed on disk since you opened it") })
	if got := x.d.read(t, "kb", "a.md"); got != "# A\n\nchanged while the backend was away\n" {
		t.Fatalf("the save overwrote the disk's version: %q", got)
	}
}

// TestACleanFileSurvivesAReconnectAndIsReadAgain: a file without unsaved edits stays open over a
// restart, and is read again, the cursor where it was, when it changed on disk meanwhile.
func TestACleanFileSurvivesAReconnectAndIsReadAgain(t *testing.T) {
	notes := map[string][]string{"kb": {"a.md", "# A\n\nfirst\n\nsecond\n"}}
	x := onARestartableDaemon(t, notes)
	x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
	x.r.waitFile(t, "a.md")
	x.r.h.p.Post(func() { x.r.h.editor.SetLine(2, 0) })
	x.r.s.WaitFor(t, "the cursor", func(string) bool { return x.r.cursor() == [2]int{2, 0} })
	x.d.stop()
	x.d.write(t, "kb", "a.md", "# A\n\nfirst, changed while the backend was away\n\nsecond\n")
	x.d = startDaemonWith(t, x.sock, notes, daemonOpts{version: "v-2", roots: x.d.fs})
	x.r.waitNoticed(t, "read a.md again: it changed on disk while the backend was away")
	if f := x.r.file(); !f.open || f.dirty || !strings.Contains(x.r.editorText(), "changed while the backend was away") || x.r.cursor() != [2]int{2, 0} {
		t.Fatalf("after the reconnect: open %v dirty %v, %q at %v", f.open, f.dirty, x.r.editorText(), x.r.cursor())
	}
}

// TestAReconnectAsksBeforeItDropsEdits: a file with unsaved edits gone from the next daemon's
// workspace is closed only through the unsaved question, whose save writes it anew; when the
// workspace itself is not served, the unsaved text stays on the page as the untitled draft.
func TestAReconnectAsksBeforeItDropsEdits(t *testing.T) {
	t.Run("the file is gone", func(t *testing.T) {
		x := onARestartableDaemon(t, map[string][]string{"kb": {"a.md", "# A\n", "b.md", "# B\n"}})
		x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
		x.r.waitFile(t, "a.md")
		x.r.typeOnPage(t, "unsaved ")
		text := x.r.editorText()
		if err := x.d.fs["kb"].Remove(context.Background(), "a.md"); err != nil {
			t.Fatal(err)
		}
		x.restart(t, map[string][]string{"kb": {"b.md", ""}}, true)
		x.r.s.WaitFor(t, "the unsaved question", func(sc string) bool {
			return strings.Contains(flat(sc), "a.md has unsaved changes. Save them before you close it: it is gone from the workspace?")
		})
		if x.r.editorText() != text || !x.r.file().dirty {
			t.Fatal("the edits went before the question was answered")
		}
		x.r.h.p.Post(func() { x.r.h.unsaved("save") })
		x.r.waitNoticed(t, "saved a.md")
		if got := x.d.read(t, "kb", "a.md"); got != text {
			t.Fatalf("the save wrote %q, want %q", got, text)
		}
	})
	t.Run("the workspace is gone", func(t *testing.T) {
		x := onARestartableDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
		x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
		x.r.waitFile(t, "a.md")
		x.r.typeOnPage(t, "unsaved ")
		text := x.r.editorText()
		x.restart(t, map[string][]string{"notes": {"n.md", "# N\n"}}, false)
		x.r.waitNoticed(t, "the unsaved text is kept as an untitled draft")
		if f := x.r.file(); f.open || !f.dirty || x.r.editorText() != text || onLoop(x.r, func() string { return x.r.h.ws }) != "notes" {
			t.Fatalf("after the reconnect: open %v dirty %v, %q in %q", f.open, f.dirty, x.r.editorText(), onLoop(x.r, func() string { return x.r.h.ws }))
		}
	})
}

// heldReads is a TUI whose next doc.read, once armed, is read and then held before its answer is
// handed back, until released.
type heldReads struct {
	d       *daemon
	r       *running
	armed   atomic.Bool
	held    chan struct{}
	release func()
	gate    chan struct{}
	done    chan struct{} // the held answer was handed back
}

func withHeldReads(t *testing.T, notes map[string][]string) *heldReads {
	t.Helper()
	x := &heldReads{d: startDaemonWith(t, "", notes, daemonOpts{}), held: make(chan struct{}), gate: make(chan struct{}), done: make(chan struct{})}
	x.release = sync.OnceFunc(func() { close(x.gate) })
	t.Cleanup(x.release)
	sess := NewSession(x.d.sock, nil)
	sess.afterCall = func(method string) {
		if method == "doc.read" && x.armed.CompareAndSwap(true, false) {
			close(x.held)
			<-x.gate
			defer close(x.done)
		}
	}
	x.r = runTUI(t, sess, Options{})
	x.r.s.WaitFor(t, "the files listed", func(string) bool { return len(x.r.listed()) > 0 })
	return x
}

// steady checks for a while, after the held answer was handed back, that the page holds want, saved,
// as the disk does.
func (x *heldReads) steady(t *testing.T, want string) {
	t.Helper()
	<-x.done
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if got, f := x.r.editorText(), x.r.file(); got != want || f.dirty || !f.open {
			t.Fatalf("the page after the older read: %q (dirty %v, open %v), want %q saved; disk %q", got, f.dirty, f.open, want, x.d.read(t, "kb", "a.md"))
		}
	}
	if got := x.d.read(t, "kb", "a.md"); got != want {
		t.Fatalf("the disk holds %q, the page %q", got, want)
	}
}

// TestAnOlderReadNeverUndoesASave: a reconnect's check of the open file, read before a save lands
// and answered after it, is older than the page: the page keeps the saved text, saved, as the disk
// has it. So does reading the file again (File › Reload) when a save lands meanwhile.
func TestAnOlderReadNeverUndoesASave(t *testing.T) {
	for _, c := range []struct {
		name string
		read func(h *Host)
	}{
		{"the reconnect's check", func(h *Host) { h.recheckFile() }},
		{"a reload", func(h *Host) { h.reload() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := withHeldReads(t, map[string][]string{"kb": {"a.md", "# Original\n"}})
			x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
			x.r.waitFile(t, "a.md")
			x.armed.Store(true)
			x.r.h.p.Post(func() { c.read(x.r.h) })
			<-x.held
			x.r.typeOnPage(t, "saved edit ")
			want := x.r.editorText()
			x.r.h.p.Post(x.r.h.save)
			x.r.waitNoticed(t, "saved a.md")
			x.release()
			x.steady(t, want)
		})
	}
}

// TestACheckAnsweredBeforeASaveLeavesItAlone: the same check answered before the save, the disk
// unchanged, keeps the unsaved edit, and the save then writes it.
func TestACheckAnsweredBeforeASaveLeavesItAlone(t *testing.T) {
	x := withHeldReads(t, map[string][]string{"kb": {"a.md", "# Original\n"}})
	x.r.h.p.Post(func() { x.r.h.openPath("a.md") })
	x.r.waitFile(t, "a.md")
	x.r.typeOnPage(t, "saved edit ")
	want := x.r.editorText()
	x.armed.Store(true)
	x.r.h.p.Post(x.r.h.recheckFile)
	<-x.held
	x.release()
	<-x.done
	for end := time.Now().Add(200 * time.Millisecond); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if f := x.r.file(); x.r.editorText() != want || !f.dirty {
			t.Fatalf("the check changed the page: %q, dirty %v", x.r.editorText(), f.dirty)
		}
	}
	x.r.h.p.Post(x.r.h.save)
	x.r.waitNoticed(t, "saved a.md")
	x.steady(t, want)
}
