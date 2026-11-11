package tui

import (
	"errors"
	"sync/atomic"
	"testing"
)

// TestAFailedSaveSaysSoOnTheStatusLine: a save whose write fails says so on the status line, its
// reason with it, not only in a toast, and the note stays unsaved. Before, the status kept
// "saving a.md…" for good, as after every refused gated save, reading as a save still under way.
func TestAFailedSaveSaysSoOnTheStatusLine(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	var refuse atomic.Bool
	sess := NewSession(d.sock, nil)
	sess.SetCallError(func(method string) error {
		if method == "doc.write" && refuse.CompareAndSwap(true, false) {
			return errors.New("refused by the cell")
		}
		return nil
	})
	r := runTUI(t, sess, Options{})
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	refuse.Store(true)
	r.h.p.Post(func() {
		r.h.editor.SetValue("edited\n")
		r.h.edited()
		r.h.save()
	})
	r.waitStatus(t, "save a.md: refused by the cell")
	if !onLoop(r, func() bool { return r.h.file.dirty }) {
		t.Fatal("the note lost its unsaved marker though its save failed")
	}
}
