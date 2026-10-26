package tui

import (
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// bufferKey is SPC b, then k on the buffer card: the card open, k pressed, the card closed.
func (r *running) bufferKey(t *testing.T, k rune) {
	t.Helper()
	r.leader(t, 'b')
	r.s.WaitForText(t, "SPC b — buffers")
	r.keys(t, key(k))
	r.s.WaitFor(t, "the buffer card closed", func(sc string) bool { return !strings.Contains(sc, "SPC b — buffers") })
}

// bufferPaths are the open buffers' paths in the tabs' order, a draft's "".
func (r *running) bufferPaths() []string {
	return onLoop(r, func() []string {
		out := make([]string, len(r.h.buffers))
		for i, b := range r.h.buffers {
			out[i] = b.file.path
		}
		return out
	})
}

// TestTheBuffersHaveKeysAndMenuEntries: SPC b s opens the Buffers picker, SPC b d closes the buffer
// shown; Go › Buffers… and File › Close buffer are on the menus.
func TestTheBuffersHaveKeysAndMenuEntries(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.bufferKey(t, 's')
	r.s.WaitForText(t, "switch buffer")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "switch buffer") })

	r.bufferKey(t, 'd')
	r.s.WaitFor(t, "b.md closed", func(string) bool { return onLoop(r, func() bool { return bufferOnLoop(r, "b.md") == nil }) })
	if r.file().path != "a.md" {
		t.Fatalf("after SPC b d: %q shown, want a.md", r.file().path)
	}

	r.leader(t, 'm')   // the menu bar, focused
	r.keys(t, enter()) // File
	r.s.WaitForText(t, "Close buffer")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyRight}) // Go
	r.s.WaitForText(t, "Buffers…")
}

// TestSPCbIsTheBufferCard (v0.3.2): SPC b opens the buffer card, Backspace going back to SPC's; SPC B
// and SPC C are off SPC's card. The card lists 1 to 9 and 0, d s, r l and b, and F1 names each
// catalog entry by its key on it: buffer.show_1 to buffer.show_10, buffer.close, buffer.pick,
// buffer.close_right, buffer.close_left and file.back.
func TestSPCbIsTheBufferCard(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	fs := h.features()
	reg, err := newRegistry(fs...)
	if err != nil {
		t.Fatal(err)
	}
	k := keyCatalog(fs)
	if err := k.check(mainIDs, reg.catalog); err != nil {
		t.Fatal(err)
	}
	for _, key := range k.on(leader) {
		if key.Seq == "Shift+B" || key.Seq == "Shift+C" {
			t.Errorf("SPC %s is still on SPC's card: %s", key.Show, key.Does)
		}
		if key.Seq == "b" && key.Opens != bufferCard {
			t.Errorf("SPC b opens %q, runs %q: want the buffer card", key.Opens, key.Run)
		}
	}
	c, ok := k.card(bufferCard)
	if !ok || c.Back != leader {
		t.Fatalf("the buffer card %+v: want one, Backspace going back to SPC's", c)
	}
	text := k.cardState()[textSource(bufferCard)].(string)
	for _, want := range []string{"1  show the 1st buffer", "2  show the 2nd buffer", "3  show the 3rd buffer",
		"9  show the 9th buffer", "0  show the 10th buffer", "d  close the buffer", "s  switch buffer",
		"r  close the buffers to the right", "l  close the buffers to the left", "b  back"} {
		if !strings.Contains(text, want) {
			t.Errorf("the buffer card lacks %q:\n%s", want, text)
		}
	}
	for id, want := range map[string]string{
		"buffer.show_1": "SPC b 1", "buffer.show_9": "SPC b 9", "buffer.show_10": "SPC b 0",
		"buffer.close": "SPC b d", "buffer.pick": "SPC b s", "file.back": "SPC b b",
		"buffer.close_right": "SPC b r", "buffer.close_left": "SPC b l",
	} {
		e := reg.catalog.lookup(id)
		if e == nil {
			t.Errorf("no entry %s", id)
			continue
		}
		if got := k.keysOf(e); !slices.Equal(got, []string{want}) {
			t.Errorf("F1 names %s's keys %q, want %q", id, got, want)
		}
	}
}

// TestTheBufferCardShowsTheNthBuffer: SPC b 1 to 9 show the 1st to the 9th buffer in the tabs'
// order, and 0 the 10th; a place past the last buffer shows none (run on the loop: a key that does
// nothing has no effect to wait on).
func TestTheBufferCardShowsTheNthBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.bufferKey(t, '1')
	r.s.WaitFor(t, "SPC b 1: a.md", func(string) bool { return r.file().path == "a.md" })
	r.bufferKey(t, '3')
	r.s.WaitFor(t, "SPC b 3: c.md", func(string) bool { return r.file().path == "c.md" })
	r.bufferKey(t, '2')
	r.s.WaitFor(t, "SPC b 2: b.md", func(string) bool { return r.file().path == "b.md" })
	if moved := onLoop(r, func() bool { was := r.h.active; r.h.showBufferAt(4); return r.h.active != was }); moved {
		t.Fatal("the 4th of three buffers showed one")
	}
	// ten buffers: seven drafts after the three files, the last shown
	tenth := onLoop(r, func() *openBuffer {
		for range 7 {
			r.h.newEmptyBuffer()
		}
		return r.h.buffers[9]
	})
	r.bufferKey(t, '1')
	r.s.WaitFor(t, "SPC b 1: a.md again", func(string) bool { return r.file().path == "a.md" })
	r.bufferKey(t, '0')
	r.s.WaitFor(t, "SPC b 0: the 10th buffer", func(string) bool { return onLoop(r, func() bool { return r.h.active == tenth }) })
}

// TestTheBufferCardClosesToTheRightAndLeft: five files, b.md and d.md modified, c.md shown. SPC b r
// closes d.md, asked about and shown first (Discard), then e.md, and c.md is shown again. SPC b l
// asks about b.md first, the nearest; Stay stops there, b.md shown and a.md left open.
func TestTheBufferCardClosesToTheRightAndLeft(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n", "d.md", "ddd\n", "e.md", "eee\n"}})
	r := attached(t, d)
	r.waitListed(t, 5)
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
		if p == "b.md" || p == "d.md" {
			r.typeInEditor(t, "X")
			r.waitTyped(t)
		}
	}
	r.bufferKey(t, '3')
	r.s.WaitFor(t, "c.md shown", func(string) bool { return r.file().path == "c.md" })

	r.bufferKey(t, 'r')
	r.s.WaitForText(t, "d.md has unsaved changes")
	if got := r.file().path; got != "d.md" {
		t.Fatalf("asked about d.md with %q shown", got)
	}
	r.keys(t, key('d')) // Discard
	r.s.WaitFor(t, "d.md and e.md closed, c.md shown again", func(string) bool {
		return slices.Equal(r.bufferPaths(), []string{"a.md", "b.md", "c.md"}) && r.file().path == "c.md"
	})
	if got := d.read(t, "kb", "d.md"); got != "ddd\n" {
		t.Errorf("Discard wrote d.md: %q", got)
	}

	r.bufferKey(t, 'l')
	r.s.WaitForText(t, "b.md has unsaved changes")
	r.keys(t, key('t')) // Stay
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "has unsaved changes") })
	// Stay posts nothing more: what is open now is what the run leaves
	if got := r.bufferPaths(); !slices.Equal(got, []string{"a.md", "b.md", "c.md"}) {
		t.Errorf("after Stay on b.md the buffers are %q: a.md, beyond it, was closed", got)
	}
	if got := r.file().path; got != "b.md" {
		t.Errorf("after Stay %q is shown, want b.md, the buffer asked about", got)
	}
}
