package tui

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldWrite holds the doc.write of one note, once armed: before it is sent (before) or after it
// was answered, before the answer is handed back (after); refuse, when set, refuses it unsent.
type heldWrite struct {
	note    string
	before  atomic.Bool
	after   atomic.Bool
	refuse  atomic.Bool
	held    chan struct{}
	release chan struct{}
	once    sync.Once
	sent    atomic.Bool // the write went out (after its before hold)
}

func newHeldWrite(note string) *heldWrite {
	return &heldWrite{note: note, held: make(chan struct{}, 2), release: make(chan struct{})}
}

func (w *heldWrite) free() { w.once.Do(func() { close(w.release) }) }

func (w *heldWrite) hold() {
	w.held <- struct{}{}
	<-w.release
}

// session is a session to sock whose hooks hold the note's write.
func (w *heldWrite) session(sock string) *Session {
	sess := NewSession(sock, nil)
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.write" && len(params) > 1 && params[1] == w.note && w.before.CompareAndSwap(true, false) {
			w.hold()
		}
	})
	sess.SetCallError(func(method string) error {
		if method == "doc.write" && w.refuse.CompareAndSwap(true, false) {
			return errors.New("refused by the cell")
		}
		return nil
	})
	sess.SetAfterCall(func(method string) {
		if method == "doc.write" && w.after.CompareAndSwap(true, false) {
			w.sent.Store(true)
			w.hold()
		}
	})
	return sess
}

func (w *heldWrite) waitHeld(t *testing.T) {
	t.Helper()
	select {
	case <-w.held:
	case <-time.After(daemonWait):
		t.Fatal("the save's write was never held")
	}
}

// editedNote is a window on a daemon serving kb's a.md, opened and edited, its writes held by w.
func editedNote(t *testing.T, w *heldWrite) *running {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, w.session(d.sock), Options{})
	t.Cleanup(w.free) // registered last, so it runs first: before the window and the daemon stop
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited\n"); r.h.edited(); return true })
	return r
}

// renew renews the window's connection, as a reconnect does.
func renew(t *testing.T, r *running) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), daemonWait)
	defer cancel()
	if err := r.h.session.Connect(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestASaveLostToAReconnectSaysSo: a save whose write is answered after the connection was renewed,
// unwritten, says so and to save it again, and the note stays unsaved; before, the status kept
// "saving a.md…" for ever.
func TestASaveLostToAReconnectSaysSo(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	w.before.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t)
	renew(t, r)
	w.free() // the write goes out on the connection before, now closed: it is not written
	r.waitStatus(t, "save a.md: the connection to the backend was renewed meanwhile; save it again")
	if !onLoop(r, func() bool { return r.h.file.dirty }) {
		t.Fatal("the note lost its unsaved marker though its save was lost")
	}
}

// TestASaveWrittenBeforeAReconnectSaysSaved: a save whose write landed, its answer coming after the
// connection was renewed, says the note was saved, never to save it again.
func TestASaveWrittenBeforeAReconnectSaysSaved(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	w.after.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t) // written: its answer is held
	renew(t, r)
	w.free()
	r.waitStatus(t, "saved a.md")
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); s != "saved a.md" {
		t.Fatalf("the status says %q, want saved a.md", s)
	}
}

// TestASaveLostToAReadAgainSaysSo: a save whose buffer is read again from disk while its write is
// out, the write then refused, says the note was read again and to save it again.
func TestASaveLostToAReadAgainSaysSo(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	w.before.Store(true)
	w.refuse.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t)
	onLoop(r, func() bool { r.h.load("kb", "a.md", nil); return true }) // the buffer read again
	r.waitStatus(t, "opened a.md")
	w.free()
	r.waitStatus(t, "save a.md: it was read again from disk meanwhile; save it again")
}

// TestAFailedSaveSaysSoOnTheStatusLine: a save the daemon refuses says so on the status line, its
// reason with it, not only in a toast: before, the status kept "saving a.md…" for good, as after
// every refused gated save.
func TestAFailedSaveSaysSoOnTheStatusLine(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	w.refuse.Store(true)
	r.h.p.Post(r.h.save)
	r.waitStatus(t, "save a.md: refused by the cell")
	if !onLoop(r, func() bool { return r.h.file.dirty }) {
		t.Fatal("the note lost its unsaved marker though its save failed")
	}
}
