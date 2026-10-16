package tui

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// BUFFER.NEW — the catalog's buffer.new [name] [text]: a new buffer, shown, an untitled draft
// holding the text, named by its hint until a save names it. Nothing is written.

// newBufferRun runs buffer.new with args through the catalog, as a key, autodoc.run or a plugin's
// host.call does.
func (r *running) newBufferRun(args ...string) error {
	return onLoop(r, func() error { return r.h.cat.run("buffer.new", args) })
}

// syntaxShown is the editor's syntax, as the page's highlighter has it.
func (r *running) syntaxShown() string {
	return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.syntaxDefinition"); return v.Raw })
}

// renderedSeen records what the host gives the editor's Rendered view from now on: the last value.
func (r *running) renderedSeen() func() bool {
	var last bool
	onLoop(r, func() bool { r.h.renderedSet = func(on bool) { last = on }; return true })
	return func() bool { return onLoop(r, func() bool { return last }) }
}

// TestBufferNewOpensANamedDraft: buffer.new with a name and text adds a buffer, shown: an untitled
// draft holding the text, modified (the host's mark and the editor's own), the cursor at its start,
// its tab and its name the hint. The buffer shown before keeps its unsaved text. A .sql hint reads
// as SQL, no Markdown table keys and no Rendered view; with no hint the draft is Markdown, with both.
func TestBufferNewOpensANamedDraft(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	a := bufferOn(r, "a.md")
	before := onLoop(r, func() int { return len(r.h.buffers) })
	rendered := r.renderedSeen()

	const sql = "SELECT *\nFROM public.users\nLIMIT 100;\n"
	if err := r.newBufferRun("public.users.sql", sql); err != nil {
		t.Fatalf("buffer.new: %v", err)
	}
	type shown struct {
		b                 *openBuffer
		n                 int
		open, dirty, mod  bool
		name, tab, label  string
		text              string
		row, col          int
		tables            bool
		aText             string
		aDirty, aIsActive bool
	}
	look := func() shown {
		return onLoop(r, func() shown {
			b := r.h.active
			row, col := r.h.core.Line()
			return shown{b: b, n: len(r.h.buffers), open: b.file.open, dirty: b.file.dirty, mod: r.h.core.Modified(),
				name: b.file.name(), tab: r.h.bufferTabLabel(b, bufferTabWidth-2), label: bufferLabel(b), text: r.h.core.Value(),
				row: row, col: col, tables: r.h.core.TableEditing(),
				aText: a.buf.Value(), aDirty: a.file.dirty, aIsActive: b == a}
		})
	}
	s := look()
	switch {
	case s.aIsActive || s.n != before+1:
		t.Fatalf("buffer.new: %d buffers (want %d), the shown one a.md's: %v", s.n, before+1, s.aIsActive)
	case s.open || !s.dirty || !s.mod:
		t.Fatalf("the draft: open %v, modified %v (the editor's %v); want an unsaved draft", s.open, s.dirty, s.mod)
	case s.text != sql || s.row != 0 || s.col != 0:
		t.Fatalf("the draft holds %q, the cursor at %d:%d; want the text, the cursor at its start", s.text, s.row, s.col)
	case s.name != "public.users.sql" || s.tab != "public.users.sql [+]" || !strings.HasSuffix(s.label, " public.users.sql [+]"):
		t.Fatalf("the draft is named %q, its tab %q, its row %q; want the hint", s.name, s.tab, s.label)
	case s.aText != "Xaaa\n" || !s.aDirty:
		t.Fatalf("a.md's buffer: %q, modified %v; want its own unsaved text", s.aText, s.aDirty)
	}
	if syn := r.syntaxShown(); syn != "SQL (PostgreSQL)" || s.tables || rendered() {
		t.Fatalf("the .sql draft: syntax %q, table keys %v, Rendered view %v; want SQL, neither", syn, s.tables, rendered())
	}
	if k := onLoop(r, r.h.pageKind); k != kind.Text {
		t.Fatalf("the .sql draft reads as %s, want text", k)
	}

	if err := r.newBufferRun("", "# a note\n"); err != nil {
		t.Fatalf("buffer.new with no name: %v", err)
	}
	s = look()
	if s.b == a || s.open || !s.dirty || s.name != untitled || s.tab != untitled+" [+]" {
		t.Fatalf("the draft with no name: %+v", s)
	}
	if syn := r.syntaxShown(); syn != "Markdown" || !s.tables || !rendered() {
		t.Fatalf("the draft with no name: syntax %q, table keys %v, Rendered view %v; want Markdown, both", syn, s.tables, rendered())
	}
}

// TestClosingABufferNewDraftAsks: closing the draft asks about its unsaved text, by its name; Stay
// keeps it, Discard closes it.
func TestClosingABufferNewDraftAsks(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	if err := r.newBufferRun("q.sql", "SELECT 1;\n"); err != nil {
		t.Fatal(err)
	}
	b := onLoop(r, func() *openBuffer { return r.h.active })
	onLoop(r, func() bool { r.h.closeShownBuffer(); return true })
	r.s.WaitForText(t, "q.sql has unsaved changes")
	r.keys(t, key('t')) // Stay
	r.s.WaitFor(t, "the question gone", func(sc string) bool { return !strings.Contains(sc, "has unsaved changes") })
	if onLoop(r, func() bool { return !slices.Contains(r.h.buffers, b) || b.buf.Value() != "SELECT 1;\n" }) {
		t.Fatal("Stay closed the draft, or changed its text")
	}
	onLoop(r, func() bool { r.h.closeShownBuffer(); return true })
	r.s.WaitForText(t, "q.sql has unsaved changes")
	r.keys(t, key('d')) // Discard
	r.s.WaitFor(t, "the draft closed", func(string) bool { return onLoop(r, func() bool { return !slices.Contains(r.h.buffers, b) }) })
}

// TestSavingABufferNewDraftStartsAtItsHint: Ctrl+S on the draft opens the save picker with the hint
// in its field; Create writes the draft's text under it, and the draft becomes that file's buffer.
func TestSavingABufferNewDraftStartsAtItsHint(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "aaa\n"}}, daemonOpts{goFiles: true, chunked: ".sql"})
	r := attached(t, d)
	r.waitListed(t, 1)
	const sql = "SELECT *\nFROM public.users\nLIMIT 100;\n"
	if err := r.newBufferRun("public.users.sql", sql); err != nil {
		t.Fatal(err)
	}
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	if v := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.newFilePath"); return v.Raw }); v != "public.users.sql" {
		t.Fatalf("the save picker's field: %q, want the hint", v)
	}
	r.keys(t, enter())
	r.waitFile(t, "public.users.sql")
	if got := d.read(t, "kb", "public.users.sql"); got != sql {
		t.Fatalf("public.users.sql holds %q, want the draft's text", got)
	}
}

// TestBufferNewRefusesAPathAndTooMuchText: a name with a path in it, text past newBufferMax and a
// third argument are refused, each with its reason, and add no buffer; text of exactly
// newBufferMax is taken whole.
func TestBufferNewRefusesAPathAndTooMuchText(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	state := func() (int, *openBuffer) {
		type st struct {
			n int
			b *openBuffer
		}
		s := onLoop(r, func() st { return st{len(r.h.buffers), r.h.active} })
		return s.n, s.b
	}
	n0, b0 := state()
	refused := func(why string, args ...string) {
		t.Helper()
		err := r.newBufferRun(args...)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Fatalf("buffer.new %.40q: %v, want refused: %s", args, err, why)
		}
		if n, b := state(); n != n0 || b != b0 {
			t.Fatalf("buffer.new %.40q, refused, left %d buffers (want %d), or showed another", args, n, n0)
		}
	}
	for _, name := range []string{"a/b.sql", "/etc/passwd", `a\b.sql`, "..", "../x.sql", "x..sql"} {
		refused("is a name, not a path", name, "SELECT 1;")
	}
	line := strings.Repeat("a", 63) + "\n"
	full := strings.Repeat(line, newBufferMax/len(line))
	if len(full) != newBufferMax {
		t.Fatalf("the text at the cap is %d bytes, want %d", len(full), newBufferMax)
	}
	refused("the text is 257 KiB; at most 256 KiB", "big.txt", full+"a")
	refused("takes a name and a text, not 3 arguments", "a.sql", "x", "y")

	if err := r.newBufferRun("big.txt", full); err != nil {
		t.Fatalf("buffer.new at the cap: %v", err)
	}
	if n, b := state(); n != n0+1 || b == b0 || onLoop(r, func() int { return len(b.buf.Value()) }) != newBufferMax {
		t.Fatalf("buffer.new at the cap: %d buffers, want %d, holding all of the text", n, n0+1)
	}
}

// workspaceDisk is every file of workspace ws on the daemon's disk: its path, its version and its
// content.
func workspaceDisk(t *testing.T, d *daemon, ws string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(dir string)
	walk = func(dir string) {
		infos, err := d.fs[ws].ReadDir(context.Background(), dir)
		if err != nil {
			t.Fatalf("read the folder %s: %v", dir, err)
		}
		for _, fi := range infos {
			if fi.IsDir() {
				walk(fi.Path)
				continue
			}
			out[fi.Path] = string(fi.Version) + "\x00" + d.read(t, ws, fi.Path)
		}
	}
	walk(".")
	return out
}

// TestBufferNewWritesNothing: buffer.new named as a file of the workspace, that file open with
// unsaved text, writes nothing: the daemon is sent no write, the disk is as it was, byte for byte,
// and the file's buffer keeps its text. The draft is a buffer of its own, not the file's.
func TestBufferNewWritesNothing(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "notes/b.md", "bbb\n"}})
	sess := NewSession(d.sock, nil)
	var mu sync.Mutex
	var sent []string
	sess.SetBeforeCall(func(method string, _ []any) {
		mu.Lock()
		sent = append(sent, method)
		mu.Unlock()
	})
	methods := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(sent) }
	r := runTUI(t, sess, Options{})
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	a := bufferOn(r, "a.md")
	disk := workspaceDisk(t, d, "kb")
	from := len(methods())

	if err := r.newBufferRun("a.md", "the draft\n"); err != nil {
		t.Fatal(err)
	}
	// the draft is Markdown: its frontmatter check is the daemon's next call, after any write a
	// buffer.new would have sent at once
	r.s.WaitFor(t, "the draft checked", func(string) bool { return slices.Contains(methods()[from:], "doc.validate") })
	for _, m := range methods()[from:] {
		if strings.Contains(m, "write") || strings.Contains(m, "create") || strings.Contains(m, "rename") || strings.Contains(m, "delete") {
			t.Fatalf("buffer.new sent the daemon %s: %q", m, methods()[from:])
		}
	}
	if now := workspaceDisk(t, d, "kb"); !maps.Equal(now, disk) {
		t.Fatalf("the disk changed:\nbefore %q\nafter  %q", disk, now)
	}
	type st struct {
		active, file *openBuffer
		open         bool
		hint, aText  string
		aDirty       bool
	}
	s := onLoop(r, func() st {
		return st{r.h.active, r.h.bufferOf(fileRef{"kb", "a.md"}), r.h.active.file.open, r.h.active.file.hint, a.buf.Value(), a.file.dirty}
	})
	switch {
	case s.active == a || s.file != a || s.open:
		t.Fatalf("the draft is a.md's buffer (%v), a.md's buffer another (%v), or the draft a file (%v)", s.active == a, s.file != a, s.open)
	case s.hint != "a.md":
		t.Fatalf("the draft's hint: %q", s.hint)
	case s.aText != "Xaaa\n" || !s.aDirty:
		t.Fatalf("a.md's buffer: %q, modified %v; want its own unsaved text", s.aText, s.aDirty)
	}
	if got := onLoop(r, func() string { return r.h.bufferTabLabel(a, bufferTabWidth-2) }); got == onLoop(r, func() string { return r.h.bufferTabLabel(s.active, bufferTabWidth-2) }) {
		t.Fatalf("a.md's tab and the draft's are both %q", got)
	}
}
