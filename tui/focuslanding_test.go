package tui

import (
	"sync/atomic"
	"testing"
)

// focusRig is a running host over a.md, b.md and c.md whose doc.read of the path held names waits
// for release, the Links drawer open with the keyboard over a.md.
type focusRig struct {
	*running
	held    atomic.Value // the path whose read is held; "" none
	waiting chan struct{}
	release chan struct{}
}

func newFocusRig(t *testing.T) *focusRig {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	sess := NewSession(d.sock, nil)
	f := &focusRig{waiting: make(chan struct{}, 1), release: make(chan struct{})}
	f.held.Store("")
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.read" && len(params) > 1 && params[1] == f.held.Load() {
			f.waiting <- struct{}{}
			<-f.release
		}
	})
	f.running = runTUI(t, sess, Options{})
	f.waitListed(t, 3)
	f.openByPicker(t, "a.md")
	f.waitFile(t, "a.md")
	f.openLinks(t)
	return f
}

func (f *focusRig) openLinks(t *testing.T) {
	t.Helper()
	f.leader(t, 'l')
	f.s.WaitFor(t, "the links' keyboard", func(string) bool { return f.panelIsOpen("links") && f.focusedOn("relationsList") })
}

// openFocus is a view's OpenRefFocus of p, the keyboard asked back to the Links drawer.
func (f *focusRig) openFocus(p string) {
	onLoop(f.running, func() bool { core{f.h}.OpenRefFocus("kb", p, relationsPanelSpec().ID); return true })
}

// openHeld is openFocus of p whose read is held, until it is.
func (f *focusRig) openHeld(p string) {
	f.held.Store(p)
	f.openFocus(p)
	<-f.waiting
}

// complete releases the held read, and waits for its document to fill its buffer.
func (f *focusRig) complete(t *testing.T, p, text string) {
	t.Helper()
	f.held.Store("")
	close(f.release)
	f.s.WaitFor(t, p+" in its buffer", func(string) bool {
		b := bufferOn(f.running, p)
		return b != nil && onLoop(f.running, func() bool { return b.buf.Value() == text })
	})
}

// A focus landing leaves the keyboard on its panel, a document read and one open already alike;
// an open without one gives it to the editor, as before.
func TestAFocusLandingKeepsThePanelsKeyboard(t *testing.T) {
	f := newFocusRig(t)
	f.openFocus("c.md") // read
	f.s.WaitFor(t, "c.md shown, the links' keyboard", func(string) bool {
		return f.shownPath() == "c.md" && f.editorText() == "ccc\n" && f.panelIsOpen("links") && f.focusedOn("relationsList")
	})
	f.openFocus("a.md") // open already
	f.s.WaitFor(t, "a.md shown, the links' keyboard", func(string) bool {
		return f.shownPath() == "a.md" && f.focusedOn("relationsList")
	})
	onLoop(f.running, func() bool { core{f.h}.OpenRef("kb", "c.md"); return true })
	f.s.WaitFor(t, "c.md shown, the editor's keyboard", func(string) bool { return f.shownPath() == "c.md" && f.focusedOn("editor") })
}

// A pending → cached B → A completes: while c.md's focus open is read, a.md, open already, is
// opened (at once); c.md's read then fills its buffer, and the keyboard stays in a.md's editor.
func TestAFocusLandingYieldsToACachedOpen(t *testing.T) {
	f := newFocusRig(t)
	f.openHeld("c.md")
	onLoop(f.running, func() bool { core{f.h}.OpenRef("kb", "a.md"); return true }) // cached: at once
	f.s.WaitFor(t, "a.md shown, the editor's keyboard", func(string) bool { return f.shownPath() == "a.md" && f.focusedOn("editor") })
	f.complete(t, "c.md", "ccc\n")
	if f.shownPath() != "a.md" || !f.focusedOn("editor") || !f.panelIsOpen("links") {
		t.Fatalf("c.md's late read took the page or the keyboard: shown %s, editor focused %v", f.shownPath(), f.focusedOn("editor"))
	}
}

// A switch of buffer, no navigation, while the focus open is read: its buffer is no longer the one
// shown, so the keyboard stays.
func TestAFocusLandingYieldsToABufferSwitch(t *testing.T) {
	f := newFocusRig(t)
	f.openHeld("c.md")
	onLoop(f.running, func() bool { f.h.activate(f.h.bufferOf(fileRef{"kb", "a.md"})); f.h.focusEditor(); return true })
	f.complete(t, "c.md", "ccc\n")
	if f.shownPath() != "a.md" || !f.focusedOn("editor") {
		t.Fatalf("shown %s, editor focused %v; want a.md, the editor's keyboard", f.shownPath(), f.focusedOn("editor"))
	}
}

// A navigation begun while the focus open is read, though it opened nothing (an HTML file's
// question, cancelled): the focus open is no longer the newest, so the keyboard stays.
func TestAFocusLandingYieldsToANewerNavigation(t *testing.T) {
	f := newFocusRig(t)
	f.openHeld("c.md")
	onLoop(f.running, func() bool { f.h.openIn("kb", "page.html"); return true })
	f.s.WaitForText(t, "Open page.html")
	onLoop(f.running, func() bool { f.h.openHTMLAs("cancel"); f.h.focusEditor(); return true })
	f.complete(t, "c.md", "ccc\n")
	if f.shownPath() != "c.md" || !f.focusedOn("editor") {
		t.Fatalf("shown %s, editor focused %v; want c.md, the editor's keyboard", f.shownPath(), f.focusedOn("editor"))
	}
}

// The panel closed, or closed and opened again, while the focus open is read: it is not the
// opening the open was asked from, so the keyboard stays.
func TestAFocusLandingYieldsToThePanelsClosing(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		f := newFocusRig(t)
		f.openHeld("c.md")
		onLoop(f.running, func() bool { f.h.closeDrawer(relationsPanelSpec().ID); return true })
		f.s.WaitFor(t, "the links closed", func(string) bool { return !f.panelIsOpen("links") })
		if reopen {
			f.openLinks(t)
		}
		onLoop(f.running, func() bool { f.h.focusEditor(); return true })
		f.complete(t, "c.md", "ccc\n")
		if !f.focusedOn("editor") {
			t.Fatalf("reopened %v: the late read moved the keyboard off the editor", reopen)
		}
	}
}
