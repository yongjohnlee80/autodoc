package tui

import (
	"context"
	"io"
	"strings"
	"testing"

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
	if !onLoop(r, r.h.editor.ReadOnly) || r.editorText() != manualPDF {
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
		t.Fatalf("the system viewer opened %q", p)
	}
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the badge gone", func(sc string) bool { return !strings.Contains(sc, "· read-only]") })
	if onLoop(r, r.h.editor.ReadOnly) {
		t.Fatal("a Markdown file opened after a derived one is read-only")
	}
}

// TestTheSystemViewerNeedsAFile: with no file open, SPC O says what it needs.
func TestTheSystemViewerNeedsAFile(t *testing.T) {
	r := attached(t, startDaemon(t, kbNotes))
	r.h.p.Post(r.h.openSystemViewer)
	r.waitNoticed(t, "no file is open: open one, then SPC O opens it in the system viewer")
}
