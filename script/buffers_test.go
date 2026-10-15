package script

import (
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// fakeBuffers is a BufferSource over editors kept by number; edited records Edited's calls.
type fakeBuffers struct {
	infos  []BufferInfo
	eds    map[int]*widget.EditorCore
	edited []int
}

func (f *fakeBuffers) Buffers() []BufferInfo { return f.infos }
func (f *fakeBuffers) Editor(n int) (*widget.EditorCore, bool) {
	ed, ok := f.eds[n]
	return ed, ok
}
func (f *fakeBuffers) Edited(n int) { f.edited = append(f.edited, n) }

// buffersEngine is an engine given two buffers: 1, a.md in kb, and 2, the draft; buffer 1's editor
// runs the shown editor's commands, as the host's headless editors do.
func buffersEngine(t *testing.T) (*Engine, *widget.EditorCore, *fakeBuffers, *[]string) {
	t.Helper()
	core := widget.NewEditorCore(widget.CoreInitialText("shown"))
	src := &fakeBuffers{
		infos: []BufferInfo{{N: 1, Path: "a.md", Workspace: "kb", Kind: "markdown", Modified: true}, {N: 2, Kind: "markdown", Active: true}},
		eds: map[int]*widget.EditorCore{
			1: widget.NewEditorCore(widget.CoreInitialText("aaa"), widget.CoreCommands(core.Commands())),
			2: core,
		},
	}
	var said []string
	e := New(core, WithNotify(func(s string) { said = append(said, s) }), WithTimeout(300*time.Millisecond), WithBuffers(src))
	t.Cleanup(e.Close)
	return e, core, src, &said
}

// buffers() lists each buffer with what it has, a draft without path or workspace; buffer(n) edits
// buffer n by the editor methods, as one undo step, and tells the host; a read tells it nothing; an
// unknown number is nil and why, and a handle whose buffer closed raises.
func TestBuffersListsAndEditsABufferByNumber(t *testing.T) {
	e, core, src, said := buffersEngine(t)
	if err := e.Load("mine.lua", []byte(`
editor.command{ name = "list", run = function()
  for _, b in ipairs(editor.buffers()) do
    editor.notify(b.n .. " " .. tostring(b.path) .. " " .. tostring(b.workspace) .. " " .. b.kind .. " " .. tostring(b.modified) .. " " .. tostring(b.active))
  end
end }
editor.command{ name = "stamp", run = function()
  local b = editor.buffer(1)
  b:insert_at(1, 1, "Z")
  b:insert_at(1, 1, "Y")
  editor.notify(b:line(1))
end }
editor.command{ name = "nobody", run = function() local b, why = editor.buffer(9); editor.notify(tostring(b) .. "|" .. why) end }
editor.command{ name = "keep", run = function() kept = editor.buffer(1) end }
editor.command{ name = "late", run = function() local ok, err = pcall(function() return kept:value() end); editor.notify(tostring(ok) .. "|" .. tostring(err)) end }
`)); err != nil {
		t.Fatal(err)
	}
	run := func(line string) {
		t.Helper()
		if err := core.RunCommand(line); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
	}
	run("list")
	if got := strings.Join(*said, ","); got != "1 a.md kb markdown true false,2 nil nil markdown false true" {
		t.Fatalf("buffers(): %q", got)
	}
	*said = nil
	run("stamp")
	one := src.eds[1]
	if one.Value() != "YZaaa" || strings.Join(*said, ",") != "YZaaa" || core.Value() != "shown" {
		t.Fatalf("buffer 1 %q, said %q, the shown %q", one.Value(), *said, core.Value())
	}
	if len(src.edited) != 2 || src.edited[0] != 1 || src.edited[1] != 1 {
		t.Fatalf("Edited calls: %v; want 1 per edit, none for the read", src.edited)
	}
	one.Undo()
	if one.Value() != "Zaaa" {
		t.Fatalf("one undo: %q; each call is its own step", one.Value())
	}
	*said = nil
	run("nobody")
	if got := strings.Join(*said, ","); got != "nil|no buffer 9" {
		t.Fatalf("buffer(9): %q", got)
	}
	run("keep")
	delete(src.eds, 1)
	*said = nil
	run("late")
	if got := strings.Join(*said, ","); !strings.HasPrefix(got, "false|") || !strings.Contains(got, "no buffer 1") {
		t.Fatalf("a closed buffer's handle: %q", got)
	}
}

// A gated script's handle keeps its gate: its run needs commands, as its ed's does; granted, it
// runs the command on that buffer's editor.
func TestAGatedBufferHandleRunNeedsCommands(t *testing.T) {
	e, core, src, said := buffersEngine(t)
	var on *widget.EditorCore
	if err := core.Commands().Set(widget.Command{Name: "boom", Run: func(x widget.CommandContext) error { on = x.Core; return nil }}); err != nil {
		t.Fatal(err)
	}
	g := &fakeGrants{granted: map[string]map[string]bool{}, requested: map[string][]string{}}
	if err := e.LoadGated("pkg", "pkg.lua", []byte(`
editor.command{ name = "go", run = function() local a, b = editor.buffer(1):run("boom"); editor.notify(tostring(a) .. "|" .. tostring(b)) end }`), g); err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand("go"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*said, ","); got != "nil|needs commands: not granted" || on != nil {
		t.Fatalf("ungranted: %q, boom ran on %p", got, on)
	}
	g.granted["pkg"] = map[string]bool{"commands": true}
	*said = nil
	if err := core.RunCommand("go"); err != nil {
		t.Fatal(err)
	}
	if on != src.eds[1] {
		t.Fatalf("granted: boom ran on %p, not buffer 1's editor %p; said %q", on, src.eds[1], *said)
	}
}
