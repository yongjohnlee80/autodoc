package tui

import (
	"context"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

func ctrlKey(k rune) tuicore.Event {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: k, Mods: tuicore.ModCtrl}
}

// waitAt waits for p open with the cursor at line, col.
func (r *running) waitAt(t *testing.T, p string, line, col int) {
	t.Helper()
	r.waitFile(t, p)
	r.s.WaitFor(t, "the cursor where it was left", func(string) bool { return r.cursor() == [2]int{line, col} })
}

// jumpIndex is the jump list's length and index.
func (r *running) jumpIndex() (int, int) {
	type li struct{ n, idx int }
	v := onLoop(r, func() li { es, idx := r.h.jumps.Entries(); return li{len(es), idx} })
	return v.n, v.idx
}

// TestCtrlOWalksTheJumpListAcrossDocuments: a link followed is a jump; in the document it opens, G
// is one too. Ctrl+O steps back through G, then to the link's document at the link; Ctrl+I goes
// forward again to the other document. SPC b goes back to the previous document in one step.
func TestCtrlOWalksTheJumpListAcrossDocuments(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {
		"a.md", "# A\n\nsee [[b]] and more\n",
		"b.md", "# B\n\none\ntwo\n",
	}}))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	col := strings.Index("see [[b]] and more", "b]]")
	r.putCursor(2, col)
	r.leader(t, 'j')
	r.waitAt(t, "b.md", 0, 0)
	r.keys(t, key('G'))
	r.s.WaitFor(t, "G", func(string) bool { return r.cursor()[0] > 0 })
	r.keys(t, ctrlKey('o')) // back through G, in b
	r.waitAt(t, "b.md", 0, 0)
	r.keys(t, ctrlKey('o')) // back to a, at the link
	r.waitAt(t, "a.md", 2, col)
	r.keys(t, ctrlKey('i')) // forward to b, where it was left
	r.waitAt(t, "b.md", 0, 0)

	r.leader(t, 'b') // back to the previous document in one step
	r.waitAt(t, "a.md", 2, col)
}

// TestCtrlOThatDoesntGoAheadKeepsTheList: back over unsaved changes asks first; Stay keeps the
// page and the jump list as they were, so Ctrl+O again asks again, and Discard then goes back.
func TestCtrlOThatDoesntGoAheadKeepsTheList(t *testing.T) {
	r := attached(t, startDaemon(t, linksKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.putCursor(2, strings.Index("see [[b]] and [the c](c.md), then nothing", "b]]"))
	r.leader(t, 'j')
	r.waitFile(t, "b.md")
	r.typeInEditor(t, "edit ")
	n, idx := r.jumpIndex()
	r.keys(t, ctrlKey('o'))
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("stay") })
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "Save them before") })
	if f := r.file(); f.path != "b.md" || !f.dirty {
		t.Fatalf("Stay left %s (dirty %v), want b.md unsaved", f.path, f.dirty)
	}
	if n2, idx2 := r.jumpIndex(); n2 != n || idx2 != idx {
		t.Fatalf("Stay moved the jump list: %d at %d, was %d at %d", n2, idx2, n, idx)
	}
	r.keys(t, ctrlKey('o'))
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("discard") })
	r.waitFile(t, "a.md")
}

// TestAJumpWhoseListMovedLandsNowhere: a step proposed, then the list moved before its document
// was shown (here, while the unsaved question is asked): the document opens, but the step is not
// committed, so the cursor stays at the document's start and the list where the move left it.
func TestAJumpWhoseListMovedLandsNowhere(t *testing.T) {
	r := attached(t, startDaemon(t, linksKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.putCursor(2, strings.Index("see [[b]] and [the c](c.md), then nothing", "b]]"))
	r.leader(t, 'j')
	r.waitFile(t, "b.md")
	r.typeInEditor(t, "edit ")
	r.keys(t, ctrlKey('o'))
	r.s.WaitForText(t, "Save them before")
	onLoop(r, func() bool { r.h.jumps.Push(widget.JumpPos{Doc: "elsewhere", Line: 1}); return true })
	n, idx := r.jumpIndex()
	r.h.p.Post(func() { r.h.unsaved("discard") })
	r.waitFile(t, "a.md")
	if at := r.cursor(); at != [2]int{0, 0} {
		t.Fatalf("a step whose list moved landed at %v, want the document's start", at)
	}
	if n2, idx2 := r.jumpIndex(); n2 != n || idx2 != idx {
		t.Fatalf("a refused step moved the list: %d at %d, was %d at %d", n2, idx2, n, idx)
	}
}

// TestCtrlOToADocumentGoneDropsIt: a document removed since it was left is dropped from the list,
// and the user told; the page stays.
func TestCtrlOToADocumentGoneDropsIt(t *testing.T) {
	d := startDaemon(t, linksKB)
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.putCursor(2, strings.Index("see [[b]] and [the c](c.md), then nothing", "b]]"))
	r.leader(t, 'j')
	r.waitFile(t, "b.md")
	if err := d.fs["kb"].Remove(context.Background(), "a.md"); err != nil {
		t.Fatal(err)
	}
	r.keys(t, ctrlKey('o'))
	r.s.WaitFor(t, "the notice", func(string) bool { return r.noticed("a.md is gone: it left the jump list") })
	if f := r.file(); f.path != "b.md" {
		t.Fatalf("after a gone document the page is %s, want b.md", f.path)
	}
	if more := onLoop(r, func() bool { _, ok := r.h.backToDocument(); return ok }); more {
		t.Fatal("the gone document is still on the list")
	}
}

// TestCtrlOOpensNoDocument: Ctrl+O no longer opens the picker; SPC o does.
func TestCtrlOOpensNoDocument(t *testing.T) {
	r := attached(t, startDaemon(t, linksKB))
	r.waitListed(t, 3)
	r.keys(t, ctrlKey('o'), key('l')) // l after it: the page handled both
	r.s.WaitFor(t, "the keys handled", func(string) bool { return true })
	if strings.Contains(r.s.String(), "open a document") {
		t.Fatal("Ctrl+O opened the picker")
	}
	r.leader(t, 'o')
	r.s.WaitForText(t, "open a document")
}

// landingKB is a.md, and b.md whose section "two" starts at byte 10 (line 4). An HTML file's
// question is asked before any read, so the one the cells open needn't exist.
var landingKB = map[string][]string{"kb": {
	"a.md", "# A\n",
	"b.md", "# B\n\none\n\ntwo\n",
}}

// A search hit's open that doesn't go ahead takes its section with it: the next open lands at the
// document's own start, not at the hit's section. Stay at the unsaved question drops it.
func TestASearchHitRefusedByStayDropsItsSection(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "edit ")
	// keys and a post are not ordered: every key typed, Esc included, before the hit's open
	r.s.WaitFor(t, "the typing done", func(string) bool {
		return onLoop(r, func() bool {
			return strings.Contains(r.h.core.Value(), "edit ") && r.h.core.Mode() == widget.ModeNormal
		})
	})
	onLoop(r, func() bool { r.h.openHit(hit{path: "b.md", byteStart: 10}); return true })
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("stay") })
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "Save them before") })
	onLoop(r, func() bool { r.h.openPath("b.md"); return true })
	r.s.WaitForText(t, "Save them before")
	r.h.p.Post(func() { r.h.unsaved("discard") })
	r.waitAt(t, "b.md", 0, 0)
}

// An HTML file's Cancel drops the hit's section: the next open lands at its own start.
func TestASearchHitRefusedByAnHTMLCancelDropsItsSection(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.waitListed(t, 2)
	onLoop(r, func() bool { r.h.openHit(hit{path: "c.html", byteStart: 10}); return true })
	r.s.WaitFor(t, "the HTML question", func(string) bool { return onLoop(r, func() bool { return r.h.htmlAsk != nil }) })
	onLoop(r, func() bool { r.h.openHTMLAs("cancel"); return true })
	onLoop(r, func() bool { r.h.openPath("b.md"); return true })
	r.waitAt(t, "b.md", 0, 0)
}

// A read that fails drops the hit's section: the next open lands at its own start.
func TestASearchHitWhoseReadFailsDropsItsSection(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.waitListed(t, 2)
	onLoop(r, func() bool { r.h.openHit(hit{path: "gone.md", byteStart: 10}); return true })
	r.s.WaitFor(t, "the failure told", func(string) bool { return r.noticed("gone.md") })
	onLoop(r, func() bool { r.h.openPath("b.md"); return true })
	r.waitAt(t, "b.md", 0, 0)
}

// A search hit lands at its section, in another document and in the one shown already.
func TestASearchHitLandsAtItsSection(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.openHit(hit{path: "b.md", byteStart: 10}); return true })
	r.waitAt(t, "b.md", 4, 0)
	r.putCursor(0, 0)
	onLoop(r, func() bool { r.h.openHit(hit{path: "b.md", byteStart: 10}); return true })
	r.waitAt(t, "b.md", 4, 0)
}
