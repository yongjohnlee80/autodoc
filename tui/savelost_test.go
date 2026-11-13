package tui

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/autodoc/tui/agents"
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

// answeringNote is a window on a daemon serving kb's a.md, opened and edited, whose session
// answers the next doc.write with write and, when read is set, the doc.read after it with read,
// unsent: the daemon's answers a test cannot make it give.
func answeringNote(t *testing.T, write, read error) *running {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var wrote, armed atomic.Bool
	sess := NewSession(d.sock, nil)
	sess.SetCallError(func(method string) error {
		switch {
		case method == "doc.write" && armed.CompareAndSwap(true, false):
			wrote.Store(true)
			return write
		case method == "doc.read" && read != nil && wrote.CompareAndSwap(true, false):
			return read
		}
		return nil
	})
	r := runTUI(t, sess, Options{})
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited\n"); r.h.edited(); return true })
	armed.Store(true)
	return r
}

// TestASaveThatFindsItsNoteDeletedSaysTheNextSaveMakesIt: a save the daemon answers "not found" (the
// note deleted on disk) says on the status line that its text is kept and the next save creates it
// again, not only by the buffer's badge.
func TestASaveThatFindsItsNoteDeletedSaysTheNextSaveMakesIt(t *testing.T) {
	t.Parallel()
	r := answeringNote(t, &golibrpc.Error{Code: rpc.CodeNotFound, Message: "a.md is not there"}, nil)
	r.h.p.Post(r.h.save)
	r.waitStatus(t, "a.md was deleted on disk: its text is kept; saving creates it again")
}

// TestASaveWrittenButNotReadBackSaysSo: a write that landed (committed) whose read-back failed says
// so on the status line, the notice's words: saved, not read back, reload before saving again.
func TestASaveWrittenButNotReadBackSaysSo(t *testing.T) {
	t.Parallel()
	r := answeringNote(t, &golibrpc.Error{Code: rpc.CodeCommitted, Message: "written; a follow-up failed"}, errors.New("no read today"))
	r.h.p.Post(r.h.save)
	r.waitStatus(t, "saved a.md, but it could not be read back: no read today — reload before saving again")
}

// committedHeldRead is a window on a daemon serving kb's a.md, opened and edited, whose session
// answers the next doc.write Committed, unsent, and holds the read-back after it until the returned
// release: failing with read when it is set (held before it is sent, then refused unsent); else
// read from the daemon, which still holds "a\n", not what the save wrote, and held once answered,
// so a renewed connection does not close the call under it.
func committedHeldRead(t *testing.T, read error) (r *running, held <-chan struct{}, release func()) {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var wrote, armed, holdRead atomic.Bool
	in, out := make(chan struct{}, 1), make(chan struct{})
	release = sync.OnceFunc(func() { close(out) })
	hold := func() { in <- struct{}{}; <-out }
	sess := NewSession(d.sock, nil)
	sess.SetBeforeCall(func(method string, params []any) {
		if read != nil && method == "doc.read" && len(params) > 1 && params[1] == "a.md" && holdRead.CompareAndSwap(true, false) {
			hold()
		}
	})
	sess.SetCallError(func(method string) error {
		switch {
		case method == "doc.write" && armed.CompareAndSwap(true, false):
			wrote.Store(true)
			holdRead.Store(true) // the read-back after it is held
			return &golibrpc.Error{Code: rpc.CodeCommitted, Message: "written; a follow-up failed"}
		case method == "doc.read" && read != nil && wrote.CompareAndSwap(true, false):
			return read
		}
		return nil
	})
	sess.SetAfterCall(func(method string) {
		if read == nil && method == "doc.read" && holdRead.CompareAndSwap(true, false) {
			hold()
		}
	})
	r = runTUI(t, sess, Options{})
	t.Cleanup(release)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited\n"); r.h.edited(); return true })
	armed.Store(true)
	return r, in, release
}

// TestAStaleCommittedSaveNeverSaysSaveItAgain: a write that landed (Committed) and whose answer
// comes stale, the connection renewed while its read-back is out, is never told to be saved again
// (Committed is never sent twice): not read back, it says saved but not read back, reload; read
// back but differing, it says saved but the disk differs, reload.
func TestAStaleCommittedSaveNeverSaysSaveItAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		read error
		want string
	}{
		{"not read back", errors.New("no read today"), "saved a.md, but it could not be read back: no read today — reload before saving again"},
		{"read back differing", nil, "saved a.md, but the disk differs from what was written — reload before saving again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, held, release := committedHeldRead(t, tc.read)
			r.h.p.Post(r.h.save)
			select { // committed; its read-back held
			case <-held:
			case <-time.After(daemonWait):
				t.Fatal("the save's read-back was never held")
			}
			renew(t, r)
			release()
			r.waitStatus(t, tc.want)
			if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); strings.Contains(s, "save it again") {
				t.Fatalf("the status says %q: a committed write told to be sent again", s)
			}
		})
	}
}

// TestASaveWhoseBufferClosedSaysNothing: a save whose buffer is closed while its write is out says
// nothing of it when the write answers: there is no buffer left to say it of, and the status line
// is the close's.
func TestASaveWhoseBufferClosedSaysNothing(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	w.before.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t)
	b := onLoop(r, func() *openBuffer { return r.h.active })
	onLoop(r, func() bool { r.h.dropBuffer(b); return true }) // closed, its edit discarded
	w.free()
	r.waitWithin(t, "the save settled", daemonWait, func() bool { return !onLoop(r, func() bool { return b.saving }) })
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); strings.Contains(s, "save a.md") || strings.Contains(s, "saved a.md") {
		t.Fatalf("the status says %q: a save whose buffer was closed spoke for it", s)
	}
}

// TestALandedSaveOvertakenByAReadAgainKeepsTheNewerStatus: a save that landed whole, its answer
// arriving after the note was read again, leaves the read's "opened a.md" on the status line, a
// newer word than "saving a.md…" was, and says it was saved by the notice alone.
func TestALandedSaveOvertakenByAReadAgainKeepsTheNewerStatus(t *testing.T) {
	t.Parallel()
	w := newHeldWrite("a.md")
	r := editedNote(t, w)
	b := onLoop(r, func() *openBuffer { return r.h.active })
	w.after.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t) // written: its answer is held
	r.h.p.Post(func() { r.h.load(r.h.file.ws, r.h.file.path, nil) })
	r.waitStatus(t, "opened a.md")
	w.free()
	r.waitWithin(t, "the save settled", daemonWait, func() bool { return !onLoop(r, func() bool { return b.saving }) })
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); s != "opened a.md" {
		t.Fatalf("the status says %q, want the read again's opened a.md kept", s)
	}
	if !r.noticed("saved a.md") {
		t.Fatal("no notice says the save landed")
	}
}

// TestALandedSaveBehindALaterSaveLeavesItsStatus: save A of a.md is written, its answer held; the
// note is read again (A goes stale), edited, and saved again (B, its write held before it is sent,
// "saving a.md…" on the status line). A's late answer, landed, leaves B's "saving a.md…" where it
// is, the same words as A's were, and says A was saved by the notice alone.
func TestALandedSaveBehindALaterSaveLeavesItsStatus(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var writes atomic.Int32
	aHeld, aGo := make(chan struct{}, 1), make(chan struct{})
	bHeld, bGo := make(chan struct{}, 1), make(chan struct{})
	freeA, freeB := sync.OnceFunc(func() { close(aGo) }), sync.OnceFunc(func() { close(bGo) })
	var armed atomic.Bool
	sess := NewSession(d.sock, nil)
	sess.SetAfterCall(func(method string) {
		if method == "doc.write" && armed.Load() && writes.Load() == 1 {
			aHeld <- struct{}{} // A written: its answer held
			<-aGo
		}
	})
	sess.SetBeforeCall(func(method string, params []any) {
		if method != "doc.write" || !armed.Load() {
			return
		}
		if writes.Add(1) == 2 {
			bHeld <- struct{}{} // B's write held before it is sent
			<-bGo
		}
	})
	r := runTUI(t, sess, Options{})
	t.Cleanup(func() { freeA(); freeB() })
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited\n"); r.h.edited(); return true })
	armed.Store(true)
	r.h.p.Post(r.h.save)
	held := func(ch chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(daemonWait):
			t.Fatalf("%s was never held", what)
		}
	}
	held(aHeld, "save A's answer")
	r.h.p.Post(func() { r.h.load(r.h.file.ws, r.h.file.path, nil) }) // A goes stale
	r.waitStatus(t, "opened a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited again\n"); r.h.edited(); return true })
	r.h.p.Post(r.h.save)
	held(bHeld, "save B's write")
	saving := onLoop(r, func() string { return r.h.trf("autodoc.notice.files.saving", "a.md") })
	r.waitStatus(t, saving) // B's
	freeA()
	r.waitWithin(t, "A's notice", daemonWait, func() bool { return r.noticed("saved a.md") })
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); s != saving {
		t.Fatalf("the status says %q after A's late answer, want B's %q kept", s, saving)
	}
	freeB()
}

// TestALandedSaveBehindAProfileStatusLeavesIt: an agent profile made the default (its "SPC g
// opens" on the status line) after a save went stale; the save's late answer, landed, leaves the
// profile's words there and says it was saved by the notice alone.
func TestALandedSaveBehindAProfileStatusLeavesIt(t *testing.T) {
	t.Parallel()
	profiles := agents.AgentProfilesJSON([]agents.AgentProfile{{Name: "claude", Command: "claude", Codename: "claude"}})
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{agents.PrefAgentProfiles: profiles}})
	w := newHeldWrite("a.md")
	r := runTUI(t, w.session(d.sock), Options{})
	t.Cleanup(w.free)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.editor.SetValue("edited\n"); r.h.edited(); return true })
	w.after.Store(true)
	r.h.p.Post(r.h.save)
	w.waitHeld(t) // written: its answer is held
	renew(t, r)   // the save goes stale
	theirs := onLoop(r, func() string { return r.h.trf("autodoc.text.agentprofiles.spc_g_opens", "claude") })
	r.h.p.Post(func() { r.h.agents.MakeAgentDefault(0) })
	r.waitStatus(t, theirs)
	w.free()
	r.waitWithin(t, "the save's notice", daemonWait, func() bool { return r.noticed("saved a.md") })
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); s != theirs {
		t.Fatalf("the status says %q after the save's late answer, want the profile's %q kept", s, theirs)
	}
}
