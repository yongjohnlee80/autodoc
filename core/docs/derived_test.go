package docs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// pdfText is a build's deriver for tests: a PDF's text is text when set, else the file's own bytes;
// misses are the eviction misses it answers first; bytes, when set, the size it states.
type pdfText struct {
	text   string
	bytes  int64
	fail   error
	misses atomic.Int32
	calls  atomic.Int32
}

func (d *pdfText) Formats() []string                { return []string{".pdf"} }
func (d *pdfText) Describe(string) (string, string) { return "fake/pdf", "1" }
func (d *pdfText) Derive(_ context.Context, _ string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	d.calls.Add(1)
	if d.misses.Add(-1) >= 0 {
		return registrations.Derived{}, fmt.Errorf("evicted: %w", fs.ErrNotExist)
	}
	if d.fail != nil {
		return registrations.Derived{}, d.fail
	}
	text := d.text
	if text == "" {
		b := make([]byte, size)
		_, _ = r.ReadAt(b, 0)
		text = string(b)
	}
	n := int64(len(text))
	if d.bytes != 0 {
		n = d.bytes
	}
	return registrations.Derived{Text: io.NopCloser(strings.NewReader(text)), Bytes: n, ID: "fake/pdf", Version: "1"}, nil
}

func deriving(t *testing.T, fsys vfs.FS, d registrations.Deriver) *Docs {
	t.Helper()
	reg, err := registrations.New(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return New(fsys, func(string) bool { return true }, WithRegistrations(reg.Kinds()), WithDeriver(reg))
}

// TestADerivedDocumentIsReadOnly: a PDF the build derives reads as its text at the file's version,
// after one eviction miss too; a write, a rename or a removal of it is refused, and so is a read
// by a build without the deriver.
func TestADerivedDocumentIsReadOnly(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		fi, err := fsys.WriteFile(ctx, "manual.pdf", strings.NewReader("# Manual\n\nkestrel\n"))
		if err != nil {
			t.Fatal(err)
		}
		d := &pdfText{}
		d.misses.Store(1)
		docs := deriving(t, fsys, d)
		got, err := docs.Read(ctx, "manual.pdf")
		if err != nil || string(got.Content) != "# Manual\n\nkestrel\n" || got.Version != fi.Version || d.calls.Load() != 2 {
			t.Fatalf("%q at %q after %d calls, %v; want the text at %q after a miss and a retry", got.Content, got.Version, d.calls.Load(), err, fi.Version)
		}
		if _, err := docs.Write(ctx, "manual.pdf", []byte("x"), got.Version); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("write: %v", err)
		}
		if err := docs.Rename(ctx, "manual.pdf", "m.pdf"); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("rename: %v", err)
		}
		if err := docs.Rename(ctx, "a.md", "manual2.pdf"); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("rename onto a derived format: %v", err)
		}
		if err := docs.Remove(ctx, "manual.pdf", got.Version); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("remove: %v", err)
		}
		if content(t, fsys, "manual.pdf") != "# Manual\n\nkestrel\n" {
			t.Fatal("the file changed")
		}
		if _, err := New(fsys, nil).Read(ctx, "manual.pdf"); !errors.Is(err, ErrNotEligible) {
			t.Fatalf("a community read: %v", err)
		}
		if _, err := deriving(t, fsys, &pdfText{}).Read(ctx, "gone.pdf"); !errors.Is(err, vfs.ErrNotExist) {
			t.Fatalf("a missing file: %v", err)
		}
	})
}

// TestALongDerivedTextIsCutToFitAndSaysSo: text over MaxSize is cut at a paragraph's end within one
// message, and labelled with how much of how much it shows; text that fits is whole.
func TestALongDerivedTextIsCutToFitAndSaysSo(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	if _, err := fsys.WriteFile(ctx, "big.pdf", strings.NewReader("%PDF")); err != nil {
		t.Fatal(err)
	}
	para := "## Section\n\n" + strings.Repeat("word ", 2000) + "\n\n"
	text := strings.Repeat(para, MaxSize/len(para)+3)
	got, err := deriving(t, fsys, &pdfText{text: text}).Read(ctx, "big.pdf")
	if err != nil {
		t.Fatal(err)
	}
	body, label, ok := strings.Cut(string(got.Content), "\n\n---\n\n"+TruncatedMark+" ")
	if !ok || len(got.Content) > MaxSize {
		t.Fatalf("%d bytes (limit %d), labelled %v", len(got.Content), MaxSize, ok)
	}
	if want := fmt.Sprintf("the first %d of the %d bytes of text derived from this PDF", len(body), len(text)); !strings.HasPrefix(label, want) {
		t.Fatalf("label %q, want it to start %q", label, want)
	}
	if strings.Count(label, "\n") != 1 || !strings.HasSuffix(label, "\n") {
		t.Fatalf("the note is not one last line: %q", label)
	}
	if !strings.HasPrefix(text, body) || strings.HasSuffix(body, "\n") || !strings.HasPrefix(text[len(body):], "\n\n") || len(body) < MaxSize/2 {
		t.Fatalf("not cut at a paragraph's end: ...%q", body[len(body)-20:])
	}
	short, err := deriving(t, fsys, &pdfText{text: text, bytes: 10}).Read(ctx, "big.pdf")
	if err != nil || !strings.Contains(string(short.Content), fmt.Sprintf("of more than %d bytes of text", MaxSize)) {
		t.Fatalf("an understated size: %v", err)
	}
	whole := strings.Repeat("a", MaxSize)
	if got, err := deriving(t, fsys, &pdfText{text: whole}).Read(ctx, "big.pdf"); err != nil || string(got.Content) != whole {
		t.Fatalf("text that fits was changed: %d bytes, %v", len(got.Content), err)
	}
}

// TestACutFallsOnALineOrACharacter: with no paragraph break late enough, the cut is at the last line
// break; with none at all, before the character that would not fit whole.
func TestACutFallsOnALineOrACharacter(t *testing.T) {
	lines := []byte("one\n\n" + strings.Repeat("x", 40) + "\nyy" + strings.Repeat("z", 20))
	if got := cleanCut(lines, 50); got != 45 {
		t.Fatalf("cut at %d, want the line break at 45", got)
	}
	runes := []byte(strings.Repeat("é", 30)) // two bytes each
	if got := cleanCut(runes, 21); got != 20 {
		t.Fatalf("cut at %d, want 20, between two characters", got)
	}
}

// TestADerivationThatFailsIsTheDocumentsError: a deriver's failure, and a file over the container's
// bound, are refused with their reasons.
func TestADerivationThatFailsIsTheDocumentsError(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	if _, err := fsys.WriteFile(ctx, "a.pdf", strings.NewReader("%PDF")); err != nil {
		t.Fatal(err)
	}
	if _, err := deriving(t, fsys, &pdfText{fail: errors.New("encrypted")}).Read(ctx, "a.pdf"); !errors.Is(err, derived.ErrDeriverFailed) {
		t.Fatalf("a failed derivation: %v", err)
	}
	if _, err := deriving(t, fsys, &pdfText{text: "\xff"}).Read(ctx, "a.pdf"); !errors.Is(err, derived.ErrRefused) {
		t.Fatalf("a text that is not UTF-8: %v", err)
	}
	big := sized{FS: fsys, size: derived.MaxContainer + 1}
	if _, err := deriving(t, big, &pdfText{}).Read(ctx, "a.pdf"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a file over the container's bound: %v", err)
	}
}

// sized reports every file as size bytes.
type sized struct {
	*memfs.FS
	size int64
}

func (s sized) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	fi, err := s.FS.Stat(ctx, name)
	fi.Size = s.size
	return fi, err
}

// TestAnHTMLPageReadsAsItsContent: every build derives HTML; doc.read serves the page's content,
// without its script or chrome, and the page is read-only.
func TestAnHTMLPageReadsAsItsContent(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		page := `<html><head><script>exfiltrate()</script></head><body><nav>menubar</nav><main><h1>Kestrel</h1><p>hovers</p></main></body></html>`
		if _, err := fsys.WriteFile(ctx, "page.html", strings.NewReader(page)); err != nil {
			t.Fatal(err)
		}
		docs := deriving(t, fsys, registrations.Documents(derived.MaxContainer, derived.MaxText))
		got, err := docs.Read(ctx, "page.html")
		if err != nil {
			t.Fatal(err)
		}
		text := string(got.Content)
		if !strings.Contains(text, "# Kestrel") || strings.Contains(text, "exfiltrate") || strings.Contains(text, "menubar") {
			t.Fatalf("page.html read as %q", text)
		}
		if _, err := docs.Write(ctx, "page.html", []byte("<p>x</p>"), got.Version); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("write: %v", err)
		}
	})
}

// TestRawReadsAndWritesAnHTMLFile: ReadRaw gives an HTML file's own bytes and WriteRaw saves new
// ones at their version, where Read derives its text and Write refuses it; a stale version is a
// conflict. Any other derived format is refused raw, before anything is read or written: a DOCX,
// and a PDF whose bytes are ASCII text, which a byte test alone would let through, are left as
// they were though the write names their current version. A document that is not derived reads and
// writes raw as it does plainly.
func TestRawReadsAndWritesAnHTMLFile(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		const page = "<h1>Title</h1><p>body</p>\n"
		for name, body := range map[string]string{"page.html": page, "manual.pdf": "%PDF body\n", "a.md": "# a\n"} {
			if _, err := fsys.WriteFile(ctx, name, strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		}
		docs := deriving(t, fsys, registrations.Documents(derived.MaxContainer, derived.MaxText))
		pdfs := deriving(t, fsys, &pdfText{})

		raw, err := docs.ReadRaw(ctx, "page.html")
		if err != nil || string(raw.Content) != page {
			t.Fatalf("ReadRaw: %q, %v; want the page's bytes", raw.Content, err)
		}
		if got, err := docs.Read(ctx, "page.html"); err != nil || strings.Contains(string(got.Content), "<h1>") {
			t.Fatalf("Read: %q, %v; want the derived text", got.Content, err)
		}
		if _, err := docs.Write(ctx, "page.html", []byte("<p>x</p>"), raw.Version); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("Write of an HTML file: %v, want read-only", err)
		}
		v, err := docs.WriteRaw(ctx, "page.html", []byte("<p>edited</p>\n"), raw.Version)
		if err != nil || content(t, fsys, "page.html") != "<p>edited</p>\n" {
			t.Fatalf("WriteRaw: %v; the file holds %q", err, content(t, fsys, "page.html"))
		}
		var conflict *vfs.ConflictError
		if _, err := docs.WriteRaw(ctx, "page.html", []byte("<p>late</p>"), raw.Version); !errors.As(err, &conflict) {
			t.Fatalf("WriteRaw at a stale version: %v, want a conflict", err)
		}
		if _, err := docs.WriteRaw(ctx, "page.html", []byte("bad\x00"), v); !errors.Is(err, ErrNotEligible) {
			t.Fatalf("WriteRaw of bytes that are not text: %v", err)
		}

		pdf, err := pdfs.Read(ctx, "manual.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pdfs.ReadRaw(ctx, "manual.pdf"); !errors.Is(err, ErrNotEligible) {
			t.Errorf("ReadRaw of an ASCII PDF: %v, want refused", err)
		}
		if _, err := pdfs.WriteRaw(ctx, "manual.pdf", []byte("plain text"), pdf.Version); !errors.Is(err, ErrNotEligible) {
			t.Errorf("WriteRaw over a PDF at its version: %v, want refused", err)
		}
		if content(t, fsys, "manual.pdf") != "%PDF body\n" {
			t.Errorf("the PDF changed: %q", content(t, fsys, "manual.pdf"))
		}

		md, err := docs.ReadRaw(ctx, "a.md")
		if err != nil || string(md.Content) != "# a\n" {
			t.Fatalf("ReadRaw of a note: %q, %v", md.Content, err)
		}
		if _, err := docs.WriteRaw(ctx, "a.md", []byte("# b\n"), md.Version); err != nil || content(t, fsys, "a.md") != "# b\n" {
			t.Fatalf("WriteRaw of a note: %v", err)
		}
	})
}
