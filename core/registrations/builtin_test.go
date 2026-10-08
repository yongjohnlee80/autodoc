package registrations

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk/code"
)

// community is the community build's registrations, as app.Main makes them.
func community(t *testing.T) *Table {
	t.Helper()
	chunkers, err := Builtin(nil)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := New(chunkers, Documents(250<<20, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

// TestEveryBuildReadsCodeAndDocuments: golib's code chunkers at their own versions, and .docx,
// .htm and .html derived as golib/docx@1 and golib/html@1.
func TestEveryBuildReadsCodeAndDocuments(t *testing.T) {
	tab := community(t)
	for ext, c := range code.Extensions() {
		if got, ok := tab.Tables().Chunkers[ext]; !ok || got != c.Version() {
			t.Errorf("%s: %q, %v; want golib's %q", ext, got, ok, c.Version())
		}
	}
	if got := tab.Tables().Chunkers[".go"]; got != "code-go-2+text-5" {
		t.Errorf(".go: %q: the version is a stored identity and must not move", got)
	}
	for f, want := range map[string]Format{".docx": {"golib/docx", "1"}, ".htm": {"golib/html", "1"}, ".html": {"golib/html", "1"}} {
		if got, ok := tab.Format(f); !ok || got != want {
			t.Errorf("%s: %+v, %v; want %+v", f, got, ok, want)
		}
	}
}

// TestTheCommunityFingerprintIsPinned: the community build's tables are no longer empty; their
// fingerprint is pinned, so a change to them is a decision, not an accident.
func TestTheCommunityFingerprintIsPinned(t *testing.T) {
	got := community(t).Tables().Fingerprint()
	if got == (Tables{}).Fingerprint() {
		t.Fatal("the community build's tables hash as empty")
	}
	const want = "08ad64aa80b3ada6abe8ff2b1d116cb4b91378c368618e7427844d00f8766e10"
	if got != want {
		t.Errorf("community fingerprint %s, pinned %s", got, want)
	}
}

// TestTheSeamRefuses: a main's chunker for a built-in code extension, and a deriver naming a
// format every build derives, are refused naming them; a Pro-style deriver of .pdf joins.
func TestTheSeamRefuses(t *testing.T) {
	if _, err := Builtin(map[string]search.Chunker{".go": ver("mine")}); err == nil || !strings.Contains(err.Error(), `".go"`) {
		t.Errorf("Builtin with .go: %v", err)
	}
	own, err := Builtin(map[string]search.Chunker{".zig": ver("1")})
	if err != nil || own[".zig"] == nil || own[".go"] == nil {
		t.Fatalf("Builtin with .zig: %v, %v", own, err)
	}
	docs := Documents(250<<20, 16<<20)
	if _, err := New(own, docs, autorag()); err == nil || !strings.Contains(err.Error(), `".docx": named twice`) {
		t.Errorf("a second deriver naming .docx: %v", err)
	}
	pdf := &deriver{formats: []string{".pdf"}, ids: map[string][2]string{".pdf": {"autorag/pdf", "1"}}}
	tab, err := New(own, docs, pdf, nil)
	if err != nil {
		t.Fatalf("a .pdf deriver beside Documents: %v", err)
	}
	if strings.Join(tab.Formats(), " ") != ".docx .htm .html .pdf" {
		t.Fatalf("formats %v", tab.Formats())
	}
	if _, err := tab.Deriver().Derive(context.Background(), "m.pdf", nil, 0); err == nil || err.Error() != "unused" {
		t.Errorf(".pdf went to %v, want its own deriver", err)
	}
}

func derive(t *testing.T, d Deriver, name string, src []byte) (string, Derived) {
	t.Helper()
	got, err := d.Derive(context.Background(), name, bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text, _ := io.ReadAll(got.Text)
	if int64(len(text)) != got.Bytes {
		t.Fatalf("%s: Bytes %d for %d bytes of text", name, got.Bytes, len(text))
	}
	return string(text), got
}

// TestDocumentsDerive: HTML as its content, DOCX as Markdown, each under its identity; text over
// the limit is an error, never a silent cut.
func TestDocumentsDerive(t *testing.T) {
	d := Documents(250<<20, 16<<20)
	page := []byte(`<html><head><title>Page</title><script>steal()</script></head><body><nav>menu</nav><main><h1>Kestrel</h1><p>hovers <b>still</b></p></main></body></html>`)
	text, got := derive(t, d, "page.HTML", page)
	if !strings.Contains(text, "# Kestrel") || !strings.Contains(text, "hovers **still**") || strings.Contains(text, "steal") || strings.Contains(text, "menu") {
		t.Errorf("html text %q", text)
	}
	if got.ID != "golib/html" || got.Version != "1" || got.Info.Title != "Page" {
		t.Errorf("html identity %s@%s, title %q", got.ID, got.Version, got.Info.Title)
	}
	text, got = derive(t, d, "notes.docx", docxOf(t, "Falcon notes"))
	if !strings.Contains(text, "Falcon notes") || got.ID != "golib/docx" {
		t.Errorf("docx: %q as %s", text, got.ID)
	}
	small := Documents(250<<20, 8)
	if _, err := small.Derive(context.Background(), "page.html", bytes.NewReader(page), int64(len(page))); err == nil {
		t.Error("text over the limit derived without an error")
	}
	if _, err := d.Derive(context.Background(), "x.pdf", bytes.NewReader(nil), 0); err == nil || errors.Is(err, io.EOF) {
		t.Errorf("Documents derived a .pdf: %v", err)
	}
}

// docxOf is the smallest DOCX: one paragraph.
func docxOf(t *testing.T, para string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + para + `</w:t></w:r></w:p></w:body></w:document>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
