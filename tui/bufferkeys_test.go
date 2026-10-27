package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
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

// TestTheBufferCardAsksAboutEachUnsavedBufferInTurn: five files, all but c.md modified, c.md shown.
// SPC b r asks about d.md, then, once Discard closes it, about e.md, each shown as it is asked
// about; c.md is shown again. SPC b l asks about b.md, and Stay stops there: a.md is not asked
// about. Again from c.md, SPC b l asks about b.md, then a.md, and only c.md is left. Each
// question after the first waits for the next turn of the loop (closeBeside): the dialog the
// answer closes does not show again in the same turn.
func TestTheBufferCardAsksAboutEachUnsavedBufferInTurn(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n", "d.md", "ddd\n", "e.md", "eee\n"}})
	r := attached(t, d)
	r.waitListed(t, 5)
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
		if p != "c.md" {
			r.typeInEditor(t, "X")
			r.waitTyped(t)
		}
	}
	showC := func() {
		t.Helper()
		r.bufferKey(t, '3')
		r.s.WaitFor(t, "c.md shown", func(string) bool { return r.file().path == "c.md" })
	}
	// asked waits for the question about p, with p shown, and answers it
	asked := func(p string, answer rune) {
		t.Helper()
		r.s.WaitForText(t, p+" has unsaved changes")
		if got := r.file().path; got != p {
			t.Fatalf("asked about %s with %q shown", p, got)
		}
		r.keys(t, key(answer))
	}
	showC()
	r.bufferKey(t, 'r')
	asked("d.md", 'd') // Discard
	asked("e.md", 'd')
	r.s.WaitFor(t, "d.md and e.md closed, c.md shown again", func(string) bool {
		return slices.Equal(r.bufferPaths(), []string{"a.md", "b.md", "c.md"}) && r.file().path == "c.md"
	})

	r.bufferKey(t, 'l')
	asked("b.md", 't') // Stay
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "has unsaved changes") })
	// Stay posts nothing more: no question about a.md comes, and what is open now is what the run leaves
	if got := r.bufferPaths(); !slices.Equal(got, []string{"a.md", "b.md", "c.md"}) || r.file().path != "b.md" {
		t.Fatalf("after Stay on b.md: buffers %q, %q shown; want all three, b.md shown", got, r.file().path)
	}

	showC()
	r.bufferKey(t, 'l')
	asked("b.md", 'd')
	asked("a.md", 'd')
	r.s.WaitFor(t, "b.md and a.md closed, c.md shown again", func(string) bool {
		return slices.Equal(r.bufferPaths(), []string{"c.md"}) && r.file().path == "c.md"
	})
	for _, p := range []string{"a.md", "b.md", "d.md", "e.md"} {
		if got := d.read(t, "kb", p); got != string(p[0])+string(p[0])+string(p[0])+"\n" {
			t.Errorf("Discard wrote %s: %q", p, got)
		}
	}
}

// TestTheBufferEntriesRunAsTheirKeys: the buffer card's catalog entries do what their keys do when
// run by id, as ':', autodoc.run and a plugin run them: buffer.show_2 shows the 2nd buffer,
// buffer.close_right and buffer.close_left close the buffers beside it, and buffer.show_3, past
// the last, shows none.
func TestTheBufferEntriesRunAsTheirKeys(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	run := func(id string) {
		t.Helper()
		if err := onLoop(r, func() error { return r.h.cat.run(id, nil) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("buffer.show_2")
	r.s.WaitFor(t, "buffer.show_2: b.md", func(string) bool { return r.file().path == "b.md" })
	run("buffer.close_right")
	r.s.WaitFor(t, "buffer.close_right: c.md closed", func(string) bool { return slices.Equal(r.bufferPaths(), []string{"a.md", "b.md"}) })
	run("buffer.close_left")
	r.s.WaitFor(t, "buffer.close_left: a.md closed", func(string) bool { return slices.Equal(r.bufferPaths(), []string{"b.md"}) })
	run("buffer.show_3")
	if got := r.file().path; got != "b.md" || !slices.Equal(r.bufferPaths(), []string{"b.md"}) {
		t.Errorf("after buffer.show_3 of one buffer: %q shown, buffers %q", got, r.bufferPaths())
	}
}

// bufferCardHost is a host with its catalog and ten buffers whose files are named by name(n),
// the 4th modified: no program, as bufferCardText reads only the buffers and the catalog.
func bufferCardHost(name func(n int) string) *Host {
	h := newHost(NewSession("unused", nil), Options{})
	h.features()
	h.buffers = nil
	for n := 1; n <= 10; n++ {
		b := h.newBuffer()
		b.file.open, b.file.ws, b.file.path = true, "kb", name(n)
		b.file.dirty = n == 4
	}
	return h
}

// TestTheBufferCardNamesEachSlotsBuffer (v0.3.2, Johno: "a file name attached to the numbers and
// the ones not attached not shown"): SPC b's card has a row per open buffer, its slot key and its
// tab's label, [+] on a modified one, 0 the 10th, and none for a slot with no buffer; a base name
// two buffers share is told apart as the tabs tell it. Ten names too long for the card are cut in
// the middle, each row within the 80 × 20 screen's card, its file's name kept. The other keys
// follow; F1 lists the card as declared.
func TestTheBufferCardNamesEachSlotsBuffer(t *testing.T) {
	h := bufferCardHost(func(n int) string { return fmt.Sprintf("n%d.md", n) })
	text := h.bufferCardText()
	for n := 1; n <= 10; n++ {
		want := fmt.Sprintf("%d  n%d.md", n%10, n)
		if n == 4 {
			want += " [+]"
		}
		if !strings.Contains(text, want+"\n") {
			t.Errorf("the card lacks the row %q:\n%s", want, text)
		}
	}
	for _, want := range []string{"d  close the buffer", "s  switch buffer", "r  close the buffers to the right", "l  close the buffers to the left", "b  back"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "show the") {
		t.Errorf("the card still lists the declared slot rows:\n%s", text)
	}
	if help := helpText(h.keys, nil, nil); !strings.Contains(help, "1  show the 1st buffer") {
		t.Error("F1 no longer lists the card's keys as declared")
	}

	h.buffers = h.buffers[:3] // three buffers: rows 1 to 3 only
	text = h.bufferCardText()
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "1  n1.md" || lines[1] != "2  n2.md" || lines[2] != "3  n3.md" {
		t.Fatalf("three buffers' card:\n%s", text)
	}
	for _, l := range lines[3:] {
		if l != "" && l[0] >= '0' && l[0] <= '9' {
			t.Errorf("a row for a slot with no buffer: %q", l)
		}
	}

	h = bufferCardHost(func(n int) string { return fmt.Sprintf("dir%d/notes.md", n) }) // one base name: told apart by path
	if text := h.bufferCardText(); !strings.Contains(text, "1  dir1/notes.md\n") || !strings.Contains(text, "0  dir10/notes.md\n") {
		t.Errorf("a shared base name is not told apart as the tabs tell it:\n%s", text)
	}

	h = bufferCardHost(func(n int) string { return fmt.Sprintf("%s-%d.md", strings.Repeat("long-name-", 12), n) })
	for i, l := range strings.Split(h.bufferCardText(), "\n")[:10] {
		if w := tuicore.StringWidth(l); w > cardBodyWidth {
			t.Errorf("row %d is %d cells, wider than the card's %d: %q", i+1, w, cardBodyWidth, l)
		}
		if !strings.Contains(l, "…") || !strings.Contains(l, fmt.Sprintf("-%d.md", i+1)) {
			t.Errorf("row %d is not cut in the middle with its file's name kept: %q", i+1, l)
		}
	}
}

// TestTheBufferCardFollowsTheBuffers: the card names what is open as it changes. Open while it
// shows, it drops a buffer closed meanwhile; opened again, it names a buffer opened since.
func TestTheBufferCardFollowsTheBuffers(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.leader(t, 'b')
	r.s.WaitFor(t, "the card naming a.md and b.md", func(sc string) bool {
		return strings.Contains(sc, "SPC b — buffers") && strings.Contains(sc, "1  a.md") && strings.Contains(sc, "2  b.md")
	})
	onLoop(r, func() bool { r.h.closeBuffer(bufferOnLoop(r, "a.md")); return true }) // not shown, unmodified: closed unasked
	r.s.WaitFor(t, "the open card naming b.md first, a.md gone", func(sc string) bool {
		return strings.Contains(sc, "SPC b — buffers") && strings.Contains(sc, "1  b.md") && !strings.Contains(sc, "2  b.md") // a notice can still name a.md
	})
	r.keys(t, esc())
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "SPC b — buffers") })
	r.openByPicker(t, "c.md")
	r.waitFile(t, "c.md")
	r.leader(t, 'b')
	r.s.WaitFor(t, "the card naming c.md second", func(sc string) bool {
		return strings.Contains(sc, "SPC b — buffers") && strings.Contains(sc, "1  b.md") && strings.Contains(sc, "2  c.md")
	})
}

// TestAnEditResolvesTheLabelsOnlyWhenItsMarkComesOn: the first edit of a clean buffer puts [+] on
// its tab and its row on SPC b's card, one pass over the buffers' labels (bufferTabLabels); the
// edits after it change neither and resolve nothing.
func TestAnEditResolvesTheLabelsOnlyWhenItsMarkComesOn(t *testing.T) {
	// the hook is set before the loop starts and reset after it stops: cleanups run last first, so
	// one registered before the host's runs after its shutdown
	passes := countLabelPasses(t)
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	before := passes()
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	if first := passes() - before; first == 0 {
		t.Fatal("[+] came on with no pass over the labels: the tabs and the card do not show it")
	}
	r.leader(t, 'b')
	r.s.WaitFor(t, "the card marking b.md modified", func(sc string) bool { return strings.Contains(sc, "2  b.md [+]") })
	r.keys(t, esc())
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "SPC b — buffers") })
	before = passes()
	r.typeInEditor(t, "more typing")
	r.s.WaitFor(t, "the typing in", func(string) bool { return strings.Contains(r.editorText(), "more typing") })
	if again := passes() - before; again != 0 {
		t.Errorf("11 more edits of a modified buffer made %d passes over the labels, want none", again)
	}
}

// TestTheBufferLabelsTakeOnePassAChange: SPC b's card resolves its labels once whatever the number
// of buffers (one width, one pass), and the cost of a pass grows with the buffers, not with their
// square: eight times the buffers take well under the 64 times a pass over every pair would.
func TestTheBufferLabelsTakeOnePassAChange(t *testing.T) {
	h := bufferCardHostN(1600)
	passes := countLabelPasses(t)
	h.bufferCardText()
	if got := passes(); got != 1 {
		t.Errorf("the card made %d passes over 1600 buffers' labels, want 1", got)
	}
	cost := func(n int) float64 {
		h := bufferCardHostN(n)
		best := 0.0
		for range 3 { // the best of three: a pause of the collector inflates one, never deflates
			ns := float64(testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					h.bufferCardText()
				}
			}).NsPerOp())
			if best == 0 || ns < best {
				best = ns
			}
		}
		return best
	}
	small, large := cost(200), cost(1600)
	if ratio := large / small; ratio > 24 {
		t.Errorf("1600 buffers' card costs %.0f times 200's (%.0f ns against %.0f): a pass grows with the square of the buffers", ratio, large, small)
	}
}

// countLabelPasses counts bufferTabLabels' passes from now until the test ends (labelsResolved).
// A cell with a running host calls it before the host starts, so the loop never reads the hook
// while the test writes it.
func countLabelPasses(t *testing.T) func() int64 {
	var n atomic.Int64
	labelsResolved = func() { n.Add(1) }
	t.Cleanup(func() { labelsResolved = nil })
	return n.Load
}

// bufferCardHostN is a host with its catalog and n buffers, files of distinct short names.
func bufferCardHostN(n int) *Host {
	h := newHost(NewSession("unused", nil), Options{})
	h.features()
	h.buffers = nil
	for i := range n {
		b := h.newBuffer()
		b.file.open, b.file.ws, b.file.path = true, "kb", fmt.Sprintf("notes/n%d.md", i)
	}
	return h
}

// BenchmarkBufferCardText is SPC b's card's text, which syncBuffers sets on every change of the buffer
// list, by the number of buffers open.
func BenchmarkBufferCardText(b *testing.B) {
	for _, n := range []int{10, 100, 300, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			h := bufferCardHostN(n)
			b.ReportAllocs()
			for b.Loop() {
				h.bufferCardText()
			}
		})
	}
}

// TestARestoreIntoTheShownBufferMarksItsRow: kept text restored into the buffer shown makes it
// modified, and SPC b's card (as the tabs) marks it so. The restore's edit reaches edited within
// its Replace, the buffer still clean, which syncs the buffers once [+] comes on; were it to come
// after the restore's setDirty, it would sync nothing, and the mark would never show.
func TestARestoreIntoTheShownBufferMarksItsRow(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	onLoop(r, func() bool {
		r.h.applyRecovered(r.h.active, recovered{WS: "kb", Path: "b.md", Text: "kept b\n"})
		return true
	})
	r.s.WaitFor(t, "the kept text in, modified", func(string) bool { return r.editorText() == "kept b\n" && r.file().dirty })
	r.leader(t, 'b')
	r.s.WaitFor(t, "the card marking b.md modified", func(sc string) bool { return strings.Contains(sc, "2  b.md [+]") })
}
