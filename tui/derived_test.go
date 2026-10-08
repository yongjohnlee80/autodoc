package tui

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// pdfAsText is a build's deriver for tests: a PDF's text is the file's own bytes.
type pdfAsText struct{}

func (pdfAsText) Formats() []string                { return []string{".pdf"} }
func (pdfAsText) Describe(string) (string, string) { return "fake/pdf", "1" }
func (pdfAsText) Derive(_ context.Context, _ string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	b := make([]byte, size)
	_, _ = r.ReadAt(b, 0)
	return registrations.Derived{Text: io.NopCloser(strings.NewReader(string(b))), Bytes: size, ID: "fake/pdf", Version: "1"}, nil
}

const manualPDF = "# Manual\n\n## Setup\n\nkestrel\n"

// TestADerivedDocumentOpensReadOnly: a PDF the daemon's build derives opens as its text, badged
// read-only on the frame and the status line; keys that edit change nothing, a save writes nothing
// and says why, the outline has its headings, and SPC O opens the file itself in the system
// viewer. A Markdown file opened next is writable again.
func TestADerivedDocumentOpensReadOnly(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "# A\n", "manual.pdf", manualPDF}}, daemonOpts{deriver: pdfAsText{}})
	r := attached(t, d)
	opened := make(chan string, 1)
	r.s.WaitFor(t, "the daemon's derived formats", func(string) bool {
		return onLoop(r, func() bool {
			r.h.browser = func(_ context.Context, p string) error { opened <- p; return nil }
			return r.h.kinds.Readable("manual.pdf")
		})
	})
	r.h.p.Post(func() { r.h.openPath("manual.pdf") })
	r.waitFile(t, "manual.pdf")
	r.s.WaitFor(t, "the badge on the frame and the status line", func(sc string) bool {
		return strings.Contains(r.frameTitle(), "manual.pdf  [PDF · read-only] › Manual") && strings.Count(sc, "[PDF · read-only]") == 2
	})
	if !onLoop(r, r.h.core.ReadOnly) || r.editorText() != manualPDF {
		t.Fatalf("the editor is not a read-only view of the derived text: %q", r.editorText())
	}
	r.keys(t, key('x'), key('d'), key('d'), key('i'))
	r.keys(t, key('z'), esc())
	if r.editorText() != manualPDF || r.file().dirty {
		t.Fatalf("keys edited a derived document: %q, dirty %v", r.editorText(), r.file().dirty)
	}
	r.h.p.Post(r.h.save)
	r.waitNoticed(t, "manual.pdf is read-only: its text is derived from the PDF; SPC O opens the original")
	if got := d.read(t, "kb", "manual.pdf"); got != manualPDF {
		t.Fatalf("the PDF was written: %q", got)
	}
	if n := onLoop(r, func() int { return len(r.h.outline.Headings()) }); n != 2 {
		t.Fatalf("the outline has %d headings, want the derived text's 2", n)
	}
	r.leader(t, 'O')
	if p := <-opened; p != "/kb/manual.pdf" {
		t.Fatalf("the default app opened %q", p)
	}
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the badge gone", func(sc string) bool { return !strings.Contains(sc, "· read-only]") })
	if onLoop(r, r.h.core.ReadOnly) {
		t.Fatal("a Markdown file opened after a derived one is read-only")
	}
}

// TestTheDefaultAppNeedsAFile: with no file open, SPC O says what it needs.
func TestTheDefaultAppNeedsAFile(t *testing.T) {
	r := attached(t, startDaemon(t, kbNotes))
	r.h.p.Post(r.h.openWithDefaultApp)
	r.waitNoticed(t, "no file is open: open one, then SPC O opens it with its default app")
}

// wholeGo is a build's chunker for .go in tests: the file, one chunk.
type wholeGo struct{}

func (wholeGo) Version() string { return "whole-1" }
func (wholeGo) Chunk(d search.Doc) ([]search.Chunk, error) {
	return []search.Chunk{{Breadcrumb: d.Path, Body: string(d.Text), ByteEnd: len(d.Text)}}, nil
}

// goWithAHashLine reads as a Markdown heading only while the page takes it for Markdown.
const goWithAHashLine = "# not a heading in Go\n\nfunc A() {}\n"

// holdCapabilities is a session whose sys.capabilities waits for the returned release.
func holdCapabilities(sock string) (*Session, func()) {
	release := make(chan struct{})
	s := NewSession(sock, nil)
	s.beforeCall = func(method string, _ []any) {
		if method == "sys.capabilities" {
			<-release
		}
	}
	return s, sync.OnceFunc(func() { close(release) })
}

// headings is the page's outline's count, -1 while it has none.
func (r *running) headings() int {
	return onLoop(r, func() int {
		if r.h.outline == nil {
			return -1
		}
		return len(r.h.outline.Headings())
	})
}

// typeOnPage types text in Insert mode, and waits for the page to take it.
func (r *running) typeOnPage(t *testing.T, text string) {
	t.Helper()
	r.keys(t, key('i'))
	r.keys(t, decltest.Type(text)...)
	r.keys(t, esc())
	r.s.WaitFor(t, "the page edited", func(string) bool { return strings.Contains(r.editorText(), text) && r.file().dirty })
}

// TestADerivedPageIsReadOnlyBeforeTheRegistrationsCome: a PDF read before the daemon's
// registrations answer opens read-only and badged all the same, with its outline; keys change
// nothing and a save says why; once they come it stays read-only.
func TestADerivedPageIsReadOnlyBeforeTheRegistrationsCome(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"manual.pdf", manualPDF}}, daemonOpts{deriver: pdfAsText{}})
	sess, release := holdCapabilities(d.sock)
	t.Cleanup(release)
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "the files listed, the registrations not", func(string) bool { return len(r.listed()) > 0 })
	r.h.p.Post(func() { r.h.openPath("manual.pdf") })
	r.waitFile(t, "manual.pdf")
	if onLoop(r, func() bool { return r.h.kinds.Readable("manual.pdf") }) {
		t.Fatal("the registrations came before the read: the cell observes nothing")
	}
	if !onLoop(r, r.h.core.ReadOnly) || !strings.Contains(r.frameTitle(), "[PDF · read-only]") || r.headings() != 2 {
		t.Fatalf("before the registrations: read-only %v, frame %q, %d headings", onLoop(r, r.h.core.ReadOnly), r.frameTitle(), r.headings())
	}
	r.keys(t, key('x'), key('d'), key('d'), key('i'), key('z'), esc())
	if r.editorText() != manualPDF || r.file().dirty {
		t.Fatalf("keys edited it: %q", r.editorText())
	}
	r.h.p.Post(r.h.save)
	r.waitNoticed(t, "manual.pdf is read-only: its text is derived from the PDF")
	release()
	r.s.WaitFor(t, "the registrations", func(string) bool {
		return onLoop(r, func() bool { return r.h.kinds.Readable("manual.pdf") })
	})
	if !onLoop(r, r.h.core.ReadOnly) || !strings.Contains(r.frameTitle(), "[PDF · read-only]") {
		t.Fatal("the page became writable once the registrations came")
	}
}

// TestARegisteredPageReadsByTheRegistrationsWhenTheyCome: a .go file read before the registrations
// answer is writable, read as the community build reads it (Markdown, a "# " line its heading);
// when they come the page reads it as the registered chunker's: plain text, no headings, writable.
func TestARegisteredPageReadsByTheRegistrationsWhenTheyCome(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.go", goWithAHashLine}}, daemonOpts{goChunker: wholeGo{}})
	sess, release := holdCapabilities(d.sock)
	t.Cleanup(release)
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "the files listed, the registrations not", func(string) bool { return len(r.listed()) > 0 })
	r.h.p.Post(func() { r.h.openPath("a.go") })
	r.waitFile(t, "a.go")
	if onLoop(r, r.h.core.ReadOnly) || r.headings() != 1 {
		t.Fatalf("before the registrations: read-only %v, %d headings", onLoop(r, r.h.core.ReadOnly), r.headings())
	}
	release()
	r.s.WaitFor(t, "the page read as registered", func(string) bool { return r.headings() == 0 })
	r.typeOnPage(t, "typed ")
}

// TestAReconnectReadsThePageByTheNewBuild: a .go file open over a reconnect to a build without the
// .go chunker reads as the last daemon's (plain text) until that build's registrations answer; when
// they come, the page reads it as the new build does: Markdown, its "# " line a heading.
func TestAReconnectReadsThePageByTheNewBuild(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s.sock")
	pro := startDaemonWith(t, sock, map[string][]string{"kb": {"a.go", goWithAHashLine}}, daemonOpts{goChunker: wholeGo{}, version: "v-pro"})
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	t.Cleanup(release)
	var asked atomic.Int32
	sess := NewSession(sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "sys.capabilities" && asked.Add(1) > 1 {
			<-held // the second connection's
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "connected — autodoc v-pro")
	r.s.WaitFor(t, "the files listed", func(string) bool { return len(r.listed()) > 0 }) // the workspace entered
	r.h.p.Post(func() { r.h.openPath("a.go") })
	r.waitFile(t, "a.go")
	r.s.WaitFor(t, "the page read as registered", func(string) bool { return r.headings() == 0 })
	pro.stop()
	startDaemonWith(t, sock, map[string][]string{"kb": {"a.go", goWithAHashLine}}, daemonOpts{goFiles: true, version: "v-community"})
	r.s.WaitFor(t, "the community daemon's workspace", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.connected && r.h.session.Version() == "v-community" && r.h.entered && len(r.h.filesAll) > 0
		})
	})
	if f := r.file(); !f.open || f.path != "a.go" {
		t.Fatalf("the reconnect closed the page: %+v", f)
	}
	if asked.Load() != 2 || r.headings() != 0 {
		t.Fatalf("before the new build's registrations: %d asked, %d headings; want 2 and the last build's reading", asked.Load(), r.headings())
	}
	release()
	r.s.WaitFor(t, "the page read as Markdown by the new build", func(string) bool { return r.headings() == 1 })
}
