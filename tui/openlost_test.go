package tui

import (
	"context"
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

// seam is the session's before-call hook that holds method's call of p.
func (c *lostCall) seam(method, p string) func(string, []any) {
	return func(m string, params []any) {
		if m == method && len(params) > 1 && params[1] == p && c.armed.CompareAndSwap(true, false) {
			c.held <- struct{}{}
			<-c.release
		}
	}
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
	sess.SetBeforeCall(c.seam("doc.read", "a.md"))
	r := runTUI(t, sess, Options{})
	t.Cleanup(c.free) // registered last, so it runs first: before the window and the daemon stop
	r.ready(t)
	c.armed.Store(true)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	c.waitHeld(t)
	r.waitStatus(t, "opening a.md")
	ctx, cancel := context.WithTimeout(context.Background(), daemonWait)
	defer cancel()
	if err := r.h.session.Connect(ctx); err != nil { // the connection renewed while the read is out
		t.Fatal(err)
	}
	c.free()
	r.waitStatus(t, "open a.md: the connection to the backend was renewed meanwhile; open it again")
	if f := r.file(); f.open && f.path == "a.md" {
		t.Fatal("the read answered on the old connection opened a.md")
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
	sess.SetBeforeCall(c.seam("doc.write", "n.md"))
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
