package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lostCall holds the first call of method whose second param is p, once armed, until released;
// it says when it holds one.
type lostCall struct {
	armed   atomic.Bool
	held    chan struct{}
	release chan struct{}
	once    sync.Once
}

func newLostCall() *lostCall {
	return &lostCall{held: make(chan struct{}, 1), release: make(chan struct{})}
}

func (c *lostCall) free() { c.once.Do(func() { close(c.release) }) }

// seam is the session's before-call hook that holds method's call of p, its second param (a
// workspace's path, after the workspace).
func (c *lostCall) seam(method, p string) func(string, []any) { return c.seamAt(method, 1, p) }

// seamAt is seam for a call whose param i is p (file.locate's absolute path is its first).
func (c *lostCall) seamAt(method string, i int, p string) func(string, []any) {
	return func(m string, params []any) {
		if m == method && len(params) > i && params[i] == p && c.armed.CompareAndSwap(true, false) {
			c.held <- struct{}{}
			<-c.release
		}
	}
}

// hold sets c's hook on sess for method's call of param i, p.
func (c *lostCall) hold(sess *Session, method string, i int, p string) {
	sess.SetBeforeCall(c.seamAt(method, i, p))
}

// waitHeld waits for the call to be held, within daemonWait.
func (c *lostCall) waitHeld(t *testing.T) {
	t.Helper()
	select {
	case <-c.held:
	case <-time.After(daemonWait):
		t.Fatal("the call to hold was never made")
	}
}

// TestAnOpenLostToAReconnectSaysSo: a note's read answered after the connection was renewed is
// dropped (the answer is the old connection's), and the status line, which said "opening a.md…",
// says the open was lost and to open it again, rather than seeming to open still.
func TestAnOpenLostToAReconnectSaysSo(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	c := newLostCall()
	sess := NewSession(d.sock, nil)
	c.hold(sess, "doc.read", 1, "a.md")
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free) // registered last, so it runs first: before the window and the daemon stop
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("b.md") })
	r.waitFile(t, "b.md") // a.md then gets a buffer of its own, beside b.md's
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	c.waitHeld(t)
	r.waitStatus(t, "opening a.md")
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 2 {
		t.Fatalf("%d buffers while a.md is read, want b.md's and the one made for a.md", n)
	}
	renew(t, r)
	c.free()
	r.waitStatus(t, "open a.md: the connection to the backend was renewed meanwhile; open it again")
	if f := r.file(); f.path != "b.md" || !f.open {
		t.Fatalf("the page shows %q (open %v), want b.md again: the buffer made for a.md stays, empty", f.path, f.open)
	}
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers, want b.md's alone: the one made for a.md never loaded", n)
	}
}

// renew renews the session's connection, as a daemon restart does.
func renew(t *testing.T, r *running) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), daemonWait)
	defer cancel()
	if err := r.h.session.Connect(ctx); err != nil {
		t.Fatal(err)
	}
}

// staleReadSaysNothing runs a.md's read held over a renewed connection while meanwhile does what
// makes the read no longer the buffer's own, then checks the read's answer says nothing: that
// change says its own.
func staleReadSaysNothing(t *testing.T, meanwhile func(r *running)) *running {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n", "c.md", "c\n"}})
	c := newLostCall()
	sess := NewSession(d.sock, nil)
	c.hold(sess, "doc.read", 1, "a.md")
	dropped := make(chan struct{}, 1)
	tune(t).readDropped = func(p string) { // on the loop, once the drop has said what it says
		if p == "a.md" {
			select { // never blocks the loop
			case dropped <- struct{}{}:
			default:
			}
		}
	}
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("b.md") })
	r.waitFile(t, "b.md")
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	c.waitHeld(t)
	renew(t, r)
	meanwhile(r)
	c.free()
	select {
	case <-dropped: // the read's answer came to the loop and was dropped there, said or not
	case <-time.After(daemonWait):
		t.Fatal("a.md's read never answered")
	}
	if st := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); strings.Contains(st, "open a.md:") {
		t.Fatalf("the status line says %q: a read no longer the buffer's own spoke for it", st)
	}
	return r
}

// TestAnOpenOvertakenInItsBufferSaysNothing: a.md's read held over a renewed connection, c.md is
// opened into the same buffer; a.md's answer, no longer the buffer's, says nothing (c.md's open says
// its own), and c.md stays shown.
func TestAnOpenOvertakenInItsBufferSaysNothing(t *testing.T) {
	t.Parallel()
	r := staleReadSaysNothing(t, func(r *running) {
		r.h.p.Post(func() { r.h.openPath("c.md") }) // the buffer made for a.md, still empty, takes c.md
		r.waitFile(t, "c.md")
	})
	if f := r.file(); f.path != "c.md" || !f.open {
		t.Fatalf("the page shows %q, want c.md", f.path)
	}
}

// TestAnOpenWhoseBufferClosedSaysNothing: a.md's read held over a renewed connection, its buffer is
// closed; the answer says nothing (the close was the user's), and b.md stays the one buffer.
func TestAnOpenWhoseBufferClosedSaysNothing(t *testing.T) {
	t.Parallel()
	r := staleReadSaysNothing(t, func(r *running) {
		onLoop(r, func() bool { r.h.closeBuffer(r.h.active); return true })
		r.waitWithin(t, "b.md shown again", daemonWait, func() bool { f := r.file(); return f.open && f.path == "b.md" })
	})
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers, want b.md's alone", n)
	}
}

// TestANewFileLostToAWorkspaceSwitchSaysSo: File › New file answered after another workspace was
// entered opens nothing (the answer is the workspace left's), but the file is made: the status
// line says it wasn't opened and to open it, rather than nothing at all.
func TestANewFileLostToAWorkspaceSwitchSaysSo(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}, "kb2": {"b.md", "b\n"}})
	c := newLostCall()
	sess := NewSession(d.sock, nil)
	c.hold(sess, "doc.write", 1, "n.md")
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free)
	r.ready(t)
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.createEmpty("n.md") })
	c.waitHeld(t)
	onLoop(r, func() bool { r.h.enter("kb2"); return true })
	c.free()
	r.waitStatus(t, "open n.md: another workspace was entered meanwhile; open it again")
	if f := r.file(); f.open && f.path == "n.md" {
		t.Fatal("the new file was opened in the workspace entered since")
	}
}

// outsideFile is a note at an absolute path outside every workspace, and kb2, a second workspace
// to enter, for File › Open file… and File › New file… cells.
func outsideFile(t *testing.T) (d *daemon, dir string) {
	t.Helper()
	d = startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}, "kb2": {"b.md", "b\n"}})
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d, dir
}

// TestAnOutsideOpenLostToAWorkspaceSwitchSaysSo: File › Open file… answered after another
// workspace was entered opens nothing (where the file is was asked of the workspace left), and the
// status line says why, rather than nothing at all.
func TestAnOutsideOpenLostToAWorkspaceSwitchSaysSo(t *testing.T) {
	t.Parallel()
	d, dir := outsideFile(t)
	abs := filepath.Join(dir, "x.md")
	c := newLostCall()
	sess := NewSession(d.sock, nil)
	c.hold(sess, "file.locate", 0, abs)
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free)
	r.ready(t)
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.openAbsolute(abs) })
	c.waitHeld(t)
	onLoop(r, func() bool { r.h.enter("kb2"); return true })
	c.free()
	r.waitStatus(t, "open "+abs+": another workspace was entered meanwhile; open it again")
	if f := r.file(); f.open && f.path == abs {
		t.Fatal("the file was opened in the workspace entered since")
	}
}

// TestAnOutsideNewFileLostToAWorkspaceSwitchSaysSo: File › New file… (any path) answered after
// another workspace was entered opens nothing, but the file is made: the status line says it wasn't
// opened and to open it.
func TestAnOutsideNewFileLostToAWorkspaceSwitchSaysSo(t *testing.T) {
	t.Parallel()
	d, dir := outsideFile(t)
	abs := filepath.Join(dir, "n.md")
	c := newLostCall()
	sess := NewSession(d.sock, nil)
	c.hold(sess, "file.locate", 0, abs) // the create asks where the path is before it writes
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free)
	r.ready(t)
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.createAbsolute(abs) })
	c.waitHeld(t)
	onLoop(r, func() bool { r.h.enter("kb2"); return true })
	c.free()
	r.waitStatus(t, "open "+abs+": another workspace was entered meanwhile; open it again")
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("the new file was not made: %v", err)
	}
	if f := r.file(); f.open && f.path == abs {
		t.Fatal("the new file was opened in the workspace entered since")
	}
}
