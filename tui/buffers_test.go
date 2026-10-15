package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// waitBuffer waits for p's buffer to be the one shown, modified or not (waitFile wants it clean).
func (r *running) waitBuffer(t *testing.T, p string) {
	t.Helper()
	r.s.WaitFor(t, p+" shown", func(string) bool { n := r.file(); return n.open && n.path == p })
}

// bufferOn is the open buffer of p in kb, read on the loop.
func bufferOn(r *running, p string) *openBuffer {
	return onLoop(r, func() *openBuffer { return r.h.bufferOf(fileRef{"kb", p}) })
}

// TestABufferIsShownAgainWithItsEdits: :b by name and by number shows a buffer, its unsaved edit
// and modified state as they were; switching is not an edit, so the clean buffer stays clean.
func TestABufferIsShownAgainWithItsEdits(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	run := func(line string) {
		t.Helper()
		if err := onLoop(r, func() error { return r.h.core.RunCommand(line) }); err != nil {
			t.Fatalf(":%s: %v", line, err)
		}
	}
	run("b a.md")
	r.waitBuffer(t, "a.md")
	if got := r.editorText(); !strings.Contains(got, "Xaaa") || !r.file().dirty {
		t.Fatalf(":b a.md: %q, dirty %v", got, r.file().dirty)
	}
	n := onLoop(r, func() int { return bufferOn(r, "b.md").n })
	run("b " + strconv.Itoa(n))
	r.waitFile(t, "b.md")
	if r.file().dirty || r.editorText() != "bbb\n" {
		t.Fatalf(":b %d: %q, dirty %v; switching made it an edit", n, r.editorText(), r.file().dirty)
	}
	if err := onLoop(r, func() error { return r.h.core.RunCommand("b nothing-like-it") }); err == nil {
		t.Error(":b with no matching buffer was accepted")
	}
}

// TestAQuitAsksAboutEachModifiedBuffer: quitting with two modified buffers asks about each in turn,
// shown as it is asked about; Stay on the second keeps the program running.
func TestAQuitAsksAboutEachModifiedBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.typeInEditor(t, "Y")
	r.keys(t, decltest.Ctrl('q'))
	r.s.WaitForText(t, "a.md has unsaved changes")
	r.keys(t, key('d')) // Discard a.md's
	r.s.WaitForText(t, "b.md has unsaved changes")
	r.keys(t, key('t')) // Stay
	select {
	case <-r.s.Quit():
		t.Fatal("quit after Stay on the second buffer")
	case <-time.After(300 * time.Millisecond):
	}
	if !strings.Contains(r.editorText(), "Ybbb") {
		t.Fatalf("the buffer asked about last is not shown: %q", r.editorText())
	}
	if got := d.read(t, "kb", "a.md"); got != "aaa\n" {
		t.Errorf("discard wrote a.md: %q", got)
	}
}

// TestALoadLandsInItsBufferAfterASwitch: a slow read of b.md lands in b.md's buffer though the user
// switched to a.md while it was read; a.md's text and page are untouched.
func TestALoadLandsInItsBufferAfterASwitch(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	sess := NewSession(d.sock, nil)
	release := make(chan struct{})
	var hold atomic.Bool
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.read" && hold.Load() && len(params) > 1 && params[1] == "b.md" {
			<-release
		}
	})
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	hold.Store(true)
	r.openByPicker(t, "b.md") // its buffer is made and shown; the read is held
	onLoop(r, func() bool { r.h.activate(r.h.bufferOf(fileRef{"kb", "a.md"})); return true })
	hold.Store(false)
	close(release)
	r.s.WaitFor(t, "b.md loaded into its buffer", func(string) bool {
		b := bufferOn(r, "b.md")
		return b != nil && onLoop(r, func() bool { return b.buf.Value() == "bbb\n" })
	})
	if r.file().path != "a.md" || r.editorText() != "aaa\n" {
		t.Fatalf("the load reached the page shown: %s %q", r.file().path, r.editorText())
	}
}

// TestASaveLandsInItsBufferAfterASwitch: a save of a.md answered after the user switched to b.md
// marks a.md's buffer saved, and leaves b.md's alone.
func TestASaveLandsInItsBufferAfterASwitch(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	sess := NewSession(d.sock, nil)
	release := make(chan struct{})
	var hold atomic.Bool
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.write" && hold.Load() {
			<-release
		}
	})
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 2)
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	hold.Store(true)
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "saving a.md")
	onLoop(r, func() bool { r.h.activate(r.h.bufferOf(fileRef{"kb", "b.md"})); return true })
	hold.Store(false)
	close(release)
	r.s.WaitFor(t, "a.md saved in its buffer", func(string) bool {
		return onLoop(r, func() bool {
			b := r.h.bufferOf(fileRef{"kb", "a.md"})
			return b != nil && !b.file.dirty && !b.buf.Modified()
		})
	})
	if r.file().path != "b.md" || r.file().dirty || r.editorText() != "bbb\n" {
		t.Fatalf("the save reached the buffer shown: %s dirty %v %q", r.file().path, r.file().dirty, r.editorText())
	}
	if got := d.read(t, "kb", "a.md"); got != "Xaaa\n" {
		t.Errorf("a.md on disk: %q", got)
	}
}

// TestAScriptEditsABufferNotShown: autodoc.buffer(n) edits a buffer no widget shows, by the editor
// functions a command uses; the buffer becomes modified, and the one shown is untouched.
func TestAScriptEditsABufferNotShown(t *testing.T) {
	dir := t.TempDir()
	src := `autodoc.command{ name = "stamp", run = function(ed, args)
  local b = autodoc.buffer(tonumber(args[1]))
  b:insert_at(1, 1, "Z")
end }`
	if err := os.WriteFile(filepath.Join(dir, "stamp.lua"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	a := bufferOn(r, "a.md")
	n := onLoop(r, func() int { return a.n })
	if err := onLoop(r, func() error { return r.h.core.RunCommand("stamp " + strconv.Itoa(n)) }); err != nil {
		t.Fatal(err)
	}
	got, dirty := onLoop(r, func() string { return a.buf.Value() }), onLoop(r, func() bool { return a.file.dirty })
	if got != "Zaaa\n" || !dirty {
		t.Fatalf("buffer %d after the script: %q, dirty %v", n, got, dirty)
	}
	if r.editorText() != "bbb\n" || r.file().dirty {
		t.Fatalf("the buffer shown changed: %q, dirty %v", r.editorText(), r.file().dirty)
	}
}

// TestAForcedExitKeepsEveryModifiedBuffer: keepRecovery writes a copy of each modified buffer, a
// file's and the draft's, and none of a clean one.
func TestAForcedExitKeepsEveryModifiedBuffer(t *testing.T) {
	dir := t.TempDir()
	h := &Host{recoveryDir: dir}
	mk := func(n int, ws, p, text string, dirty bool) *openBuffer {
		b := &openBuffer{n: n, buf: widget.NewBuffer(widget.BufferText(text))}
		b.file = openedFile{ws: ws, path: p, open: p != "", dirty: dirty}
		return b
	}
	h.buffers = []*openBuffer{mk(1, "kb", "a.md", "A", true), mk(2, "kb", "b.md", "B", false), mk(3, "", "", "draft", true)}
	h.active, h.file = h.buffers[0], &h.buffers[0].file
	if err := h.keepRecovery(); err != nil {
		t.Fatal(err)
	}
	kept := readRecovery(dir)
	if len(kept) != 2 {
		t.Fatalf("kept %d copies, want 2 (a.md and the draft)", len(kept))
	}
	texts := map[string]string{}
	for _, k := range kept {
		texts[k.Path] = k.Text
	}
	if texts["a.md"] != "A" || texts[""] != "draft" {
		t.Fatalf("kept %v", texts)
	}
}
