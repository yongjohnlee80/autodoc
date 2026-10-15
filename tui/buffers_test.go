package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
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
	n := onLoop(r, func() int { return r.h.bufferOf(fileRef{"kb", "b.md"}).n }) // not bufferOn: onLoop inside onLoop waits on itself
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
	r.openByPicker(t, "b.md")          // its buffer is made and shown; the read is held
	r.s.WaitForText(t, "opening b.md") // the picker's Enter is a key: the open is under way before the switch
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
end }
autodoc.command{ name = "names", run = function()
  local s = ""
  for _, b in ipairs(autodoc.buffers()) do s = s .. tostring(b.path) .. "/" .. tostring(b.workspace) .. "/" .. tostring(b.active) .. " " end
  autodoc.notify(s .. tostring((autodoc.buffer(99))))
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
	if err := onLoop(r, func() error { return r.h.core.RunCommand("names") }); err != nil {
		t.Fatal(err)
	}
	r.waitNoticed(t, "a.md/kb/false b.md/kb/true nil")
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

// TestAnAgentActsOnABufferNotShown: with a buffer number, each editor verb acts on that buffer, shown
// or not, and the one shown is untouched; editor.run runs a Lua command on the buffer's own editor;
// editor.buffers lists them; an unknown number is refused, and so is one that is not a number.
func TestAnAgentActsOnABufferNotShown(t *testing.T) {
	sockDir, luaDir := t.TempDir(), t.TempDir()
	src := `autodoc.command{ name = "stampit", run = function(ed) ed:insert_at(1, 1, "R") end }`
	if err := os.WriteFile(filepath.Join(luaDir, "stampit.lua"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: sockDir, LuaDir: luaDir})
	r.waitListed(t, 3)
	r.openByPicker(t, "c.md")
	r.waitFile(t, "c.md")
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	a, c := bufferOn(r, "a.md"), bufferOn(r, "c.md")
	n, cn := onLoop(r, func() int { return a.n }), onLoop(r, func() int { return c.n })

	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, onLoop(r, r.h.editorSocketPath), msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string {
		if r.h.prefs.luaGrants == nil {
			r.h.prefs.luaGrants = map[string]map[string]bool{}
		}
		r.h.prefs.luaGrants["agent:zen"] = map[string]bool{"editor.read": true, "editor.write": true, "commands": true}
		return r.h.agents.editorToken("zen")
	})
	call := func(verb string, params ...any) (any, *golibrpc.Error) {
		t.Helper()
		res, err := cli.Call(ctx, verb, append([]any{token}, params...)...)
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return nil, re
		}
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		return res, nil
	}
	must := func(verb string, params ...any) map[string]any {
		t.Helper()
		res, re := call(verb, params...)
		if re != nil {
			t.Fatalf("%s %v: %v", verb, params, re)
		}
		m, _ := res.(map[string]any)
		return m
	}
	aText := func() string { return onLoop(r, func() string { return a.buf.Value() }) }
	dirty := func(b *openBuffer) bool { return onLoop(r, func() bool { return b.file.dirty }) }
	shownUntouched := func(after string) {
		t.Helper()
		if r.editorText() != "bbb\n" || r.file().dirty || r.file().path != "b.md" {
			t.Fatalf("after %s the buffer shown changed: %s %q, dirty %v", after, r.file().path, r.editorText(), r.file().dirty)
		}
	}

	res, _ := call("editor.buffers")
	rows, _ := res.([]any)
	if len(rows) != 3 {
		t.Fatalf("editor.buffers: %v", res)
	}
	first, _ := rows[1].(map[string]any)
	if fmt.Sprint(first["n"]) != strconv.Itoa(n) || first["path"] != "a.md" || first["workspace"] != "kb" ||
		first["active"] != false || first["modified"] != false || first["revision"] == nil {
		t.Fatalf("editor.buffers' a.md: %v", first)
	}
	if second, _ := rows[2].(map[string]any); second["active"] != true || second["path"] != "b.md" {
		t.Fatalf("editor.buffers' b.md: %v", second)
	}

	if doc := must("editor.document"); doc["path"] != "b.md" {
		t.Fatalf("editor.document without a buffer: %v; want the one shown", doc)
	}
	if doc := must("editor.document", n); doc["path"] != "a.md" || fmt.Sprint(doc["buffer"]) != strconv.Itoa(n) || doc["workspace"] != "kb" {
		t.Fatalf("editor.document %d: %v", n, doc)
	}
	read := must("editor.read", nil, nil, n)
	if read["text"] != "aaa\n" {
		t.Fatalf("editor.read of buffer %d: %v", n, read)
	}
	if hits, _ := call("editor.find", "aa", nil, n); len(hits.([]any)) != 1 {
		t.Fatalf("editor.find in buffer %d: %v", n, hits)
	}
	if hits, _ := call("editor.find", "aa"); len(hits.([]any)) != 0 {
		t.Fatalf("editor.find in the buffer shown: %v", hits)
	}

	must("editor.run", "stampit", n) // a clean buffer: the run alone makes it modified
	if aText() != "Raaa\n" || !dirty(a) {
		t.Fatalf("after editor.run on buffer %d: %q, dirty %v", n, aText(), dirty(a))
	}
	shownUntouched("editor.run")
	if _, re := call("editor.replace", 1, 1, 1, 2, "Q", read["revision"], n); re == nil || re.Code != codeEditorStale {
		t.Fatalf("a replace at the revision before the run: %v", re)
	}

	cText := func() string { return onLoop(r, func() string { return c.buf.Value() }) }
	must("editor.insert", 1, 1, "Z", nil, cn) // a clean buffer: the edit alone makes it modified
	if cText() != "Zccc\n" || !dirty(c) {
		t.Fatalf("after editor.insert: %q, dirty %v", cText(), dirty(c))
	}
	shownUntouched("editor.insert")
	must("editor.replace", 1, 1, 1, 2, "Q", nil, cn)
	if cText() != "Qccc\n" {
		t.Fatalf("after editor.replace: %q", cText())
	}
	must("editor.delete", 1, 1, 1, 2, nil, cn)
	if cText() != "ccc\n" {
		t.Fatalf("after editor.delete: %q", cText())
	}
	if _, re := call("editor.run", "no-such-command", n); re == nil || re.Code != codeEditorRefused {
		t.Fatalf("editor.run of an unknown command: %v", re)
	}

	if _, re := call("editor.read", nil, nil, 99); re == nil || re.Code != codeEditorRefused || !strings.Contains(re.Message, "no buffer 99") {
		t.Fatalf("an unknown buffer: %v", re)
	}
	if _, re := call("editor.insert", 1, 1, "x", nil, "a.md"); re == nil || !strings.Contains(re.Message, "the buffer") {
		t.Fatalf("a buffer that is not a number: %v", re)
	}
	res, _ = call("editor.buffers")
	if first, _ := res.([]any)[1].(map[string]any); first["modified"] != true {
		t.Fatalf("editor.buffers after the edits: %v", first)
	}
}

// TestTheBuffersPickerAndB: the Buffers picker lists the open buffers and previews one's unsaved
// text; Enter shows it. :b with no name opens the picker; a part of a name shows the one buffer it
// matches, and one matching two, or an unknown number, is refused; Tab lists the names. :bd takes no
// name, and closes the shown buffer, a clean one unasked.
func TestTheBuffersPickerAndB(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "notes.md", "nnn\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.s.WaitFor(t, "a.md modified", func(string) bool { return onLoop(r, func() bool { return bufferOnLoop(r, "a.md").file.dirty }) })

	onLoop(r, func() bool { r.h.openBuffers(); return true })
	r.s.WaitForText(t, "switch buffer")
	r.keys(t, decltest.Type("a.md")...)
	r.s.WaitForText(t, "Xaaa") // the preview: the buffer's text as it is, unsaved edit and all
	r.keys(t, enter())
	r.waitBuffer(t, "a.md")
	if !strings.Contains(r.editorText(), "Xaaa") {
		t.Fatalf("the picker's Enter: %q", r.editorText())
	}

	run := func(line string) error {
		t.Helper()
		return onLoop(r, func() error { return r.h.core.RunCommand(line) })
	}
	if err := run("b"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "switch buffer")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "switch buffer") })

	r.openByPicker(t, "notes.md")
	r.waitFile(t, "notes.md")
	if err := run("b md"); err == nil || !strings.Contains(err.Error(), "3 buffers match") {
		t.Fatalf(":b md: %v", err)
	}
	if err := run("b 99"); err == nil || !strings.Contains(err.Error(), "no buffer 99") {
		t.Fatalf(":b 99: %v", err)
	}
	if err := run("b b."); err != nil {
		t.Fatal(err)
	}
	r.waitBuffer(t, "b.md") // a switch says nothing: "opened" is an open's
	names := onLoop(r, func() []string { return r.h.bufferEntries()[0].Complete(widget.CompleteContext{}) })
	if strings.Join(names, ",") != "a.md,b.md,notes.md" {
		t.Fatalf(":b's names: %v", names)
	}

	if err := run("bd notes.md"); err == nil {
		t.Fatal(":bd with a name was accepted")
	}
	if err := run("bd"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "b.md closed", func(string) bool { return onLoop(r, func() bool { return bufferOnLoop(r, "b.md") == nil }) })
	if strings.Contains(r.s.String(), "unsaved file") {
		t.Fatal(":bd asked about a clean buffer")
	}
	if p := r.file().path; p != "a.md" && p != "notes.md" {
		t.Fatalf("after :bd: %q shown", p)
	}
}

// bufferOnLoop is p's open buffer in kb, for a function already on the loop.
func bufferOnLoop(r *running, p string) *openBuffer { return r.h.bufferOf(fileRef{"kb", p}) }

// TestCreatingAFileOpensItInABufferOfItsOwn: App.createFile with no draft being named makes an empty
// file and opens it in a new buffer, the modified one left keeping its text; a name that exists is
// asked again, with why.
func TestCreatingAFileOpensItInABufferOfItsOwn(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "a.md modified", func(string) bool { return r.file().dirty })
	onLoop(r, func() bool { r.h.createFile("c"); return true })
	r.waitFile(t, "c.md")
	if r.editorText() != "" || d.read(t, "kb", "c.md") != "" {
		t.Fatalf("c.md: shown %q, on disk %q", r.editorText(), d.read(t, "kb", "c.md"))
	}
	if a := bufferOn(r, "a.md"); a == nil || !onLoop(r, func() bool { return a.file.dirty && a.buf.Value() == "Xaaa\n" }) {
		t.Fatal("a.md's buffer lost its unsaved text")
	}
	onLoop(r, func() bool { r.h.createFile("a"); return true })
	r.s.WaitForText(t, "a.md exists: pick another name")
}

// TestADraftClosedWhileItIsNamedIsSavedStill: a draft named and closed (its edits discarded) while
// its write is in flight is on disk once the write lands, and said so; no buffer opens for it.
func TestADraftClosedWhileItIsNamedIsSavedStill(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	sess := NewSession(d.sock, nil)
	held, release := make(chan struct{}, 1), make(chan struct{})
	var hold atomic.Bool
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.write" && hold.Load() {
			held <- struct{}{}
			<-release
		}
	})
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 1)
	r.typeInEditor(t, "draft text")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	hold.Store(true)
	r.keys(t, decltest.Type("kept")...)
	r.keys(t, enter())
	<-held
	onLoop(r, func() bool { r.h.closeShownBuffer(); return true })
	r.s.WaitForText(t, "unsaved file")
	r.keys(t, key('d'))
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "unsaved file") })
	hold.Store(false)
	close(release)
	r.waitNoticed(t, "saved the draft as kept.md")
	if got := d.read(t, "kb", "kept.md"); got != "draft text" {
		t.Fatalf("kept.md: %q", got)
	}
	if onLoop(r, func() bool { return bufferOnLoop(r, "kept.md") != nil }) {
		t.Fatal("a buffer opened for the draft closed meanwhile")
	}
}

// TestARestoreLandsInItsOwnBuffer: a draft's kept text restored while a file is open becomes a new
// draft buffer, shown, the file's buffer keeping its text; a file's kept text restored into its
// buffer, clean and not shown, marks that buffer modified, the one shown untouched.
func TestARestoreLandsInItsOwnBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "a.md modified", func(string) bool { return r.file().dirty })
	onLoop(r, func() bool { r.h.restoreAsDraft(recovered{Text: "kept draft\n"}, ""); return true })
	if f := r.file(); f.open || !f.dirty || r.editorText() != "kept draft\n" {
		t.Fatalf("the draft restored: %+v %q", f, r.editorText())
	}
	if a := bufferOn(r, "a.md"); a == nil || !onLoop(r, func() bool { return a.file.dirty && a.buf.Value() == "Xaaa\n" }) {
		t.Fatal("a.md's buffer lost its text to the restored draft")
	}

	r.openByPicker(t, "c.md")
	r.waitFile(t, "c.md")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	onLoop(r, func() bool {
		r.h.applyRecovered(bufferOnLoop(r, "c.md"), recovered{WS: "kb", Path: "c.md", Text: "kept c\n"})
		return true
	})
	c := bufferOn(r, "c.md")
	if !onLoop(r, func() bool { return c.file.dirty && c.buf.Value() == "kept c\n" && c.restored != nil }) {
		t.Fatal("the kept text did not land in c.md's buffer, modified")
	}
	if r.editorText() != "bbb\n" || r.file().dirty {
		t.Fatalf("the buffer shown changed: %q", r.editorText())
	}
}

// TestDeletingTheWorkspaceInUseAsksAboutItsBuffers: deleting the workspace in use asks about each
// of its modified buffers; Stay keeps the workspace; Discard deletes it, and its buffers close.
func TestDeletingTheWorkspaceInUseAsksAboutItsBuffers(t *testing.T) {
	d := startManaged(t, map[string]string{
		"alpha": fileDir(t, "a.md", "aaa\n", "b.md", "bbb\n"),
		"beta":  fileDir(t, "c.md", "ccc\n"),
	})
	r := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	r.h.p.Post(func() { r.h.openPath("b.md") })
	r.waitFile(t, "b.md")
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "b.md modified, typing done", func(string) bool {
		return onLoop(r, func() bool { return r.h.file.dirty && r.h.core.Mode() == widget.ModeNormal })
	})
	r.h.p.Post(func() { r.h.removeWorkspaceConfirmed("alpha") })
	r.s.WaitForText(t, "b.md has unsaved changes")
	r.keys(t, key('t')) // Stay
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "unsaved changes") })
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "alpha" {
		t.Fatalf("Stay, and the workspace in use is %q", ws)
	}
	r.h.p.Post(func() { r.h.removeWorkspaceConfirmed("alpha") })
	r.s.WaitForText(t, "b.md has unsaved changes")
	r.keys(t, key('d')) // Discard
	r.waitNoticed(t, "deleted workspace alpha")
	// a.md clean and b.md discarded: both close, none kept as a draft; the one buffer left is beta's
	// empty draft
	r.s.WaitFor(t, "alpha's buffers closed", func(string) bool {
		return onLoop(r, func() bool {
			return len(r.h.buffers) == 1 && !r.h.buffers[0].file.open && r.h.buffers[0].buf.Value() == ""
		})
	})
}

// TestAForcedExitThatCannotKeepSaysWhichBuffers: with no recovery directory nothing is kept and
// nothing fails; a directory that cannot be written fails each modified buffer, by name, all of them.
func TestAForcedExitThatCannotKeepSaysWhichBuffers(t *testing.T) {
	mk := func(n int, p, text string) *openBuffer {
		b := &openBuffer{n: n, buf: widget.NewBuffer(widget.BufferText(text))}
		b.file = openedFile{ws: "kb", path: p, open: p != "", dirty: true}
		return b
	}
	h := &Host{buffers: []*openBuffer{mk(1, "a.md", "A"), mk(2, "", "draft")}}
	h.active, h.file = h.buffers[0], &h.buffers[0].file
	if err := h.keepRecovery(); err != nil {
		t.Fatalf("no recovery directory: %v", err)
	}
	notDir := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.recoveryDir = notDir
	err := h.keepRecovery()
	if err == nil || !strings.Contains(err.Error(), "unsaved text of a.md") || !strings.Contains(err.Error(), "unsaved text of "+untitled) {
		t.Fatalf("an unwritable recovery directory: %v", err)
	}
}
