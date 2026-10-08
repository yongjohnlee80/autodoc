package index

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	codechunk "github.com/yongjohnlee80/golib/search/chunk/code"

	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// counting is a chunker that counts its cuts, under the identity of the one it wraps.
type counting struct {
	search.Chunker
	cuts atomic.Int64
}

func (c *counting) Chunk(d search.Doc) ([]search.Chunk, error) {
	c.cuts.Add(1)
	return c.Chunker.Chunk(d)
}

// everyBuild is the registrations every build has (app.Main's), with goChunker for .go when set.
func everyBuild(t *testing.T, goChunker search.Chunker) *registrations.Table {
	t.Helper()
	chunkers, err := registrations.Builtin(nil)
	if err != nil {
		t.Fatal(err)
	}
	if goChunker != nil {
		chunkers[".go"] = goChunker
	}
	reg, err := registrations.New(chunkers, registrations.Documents(250<<20, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestCodeAPreviousBuildIndexedStaysCurrent: a .go file a Pro build cut with autorag's chunker, at
// the version golib's carries now, is current under every build: not held, not cut again.
func TestCodeAPreviousBuildIndexedStaysCurrent(t *testing.T) {
	ctx := context.Background()
	pro := &counting{Chunker: codechunk.Go{}}
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: registered(t, pro)})
	e.put("a.go", goSrc)
	recorded := e.indexerOf("a.go")
	if !strings.HasPrefix(recorded, "c.go@code-go-2+text-5.") {
		t.Fatalf("the Pro build recorded %q", recorded)
	}
	now := &counting{Chunker: codechunk.Go{}}
	e.open(Options{Match: goAndMarkdown, Registrations: everyBuild(t, now)})
	// a.go touched with a new file beside it: once the new one is cut, the touch has been handled
	e.ix.Touch("a.go")
	e.put("b.go", "package a\n\nfunc C() {\n\treturn\n}\n")
	e.eventually("both handled", func() bool { return e.pendingJobs() == 0 })
	st, err := e.ix.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Held != 0 {
		t.Fatalf("status %+v: a document this build reads is held", st)
	}
	if got := e.indexerOf("a.go"); got != recorded || now.cuts.Load() != 1 {
		t.Fatalf("a.go recorded %q after %d cuts; want %q, and one cut: b.go's", got, now.cuts.Load(), recorded)
	}
}

func documentsToo(p string) bool {
	return goAndMarkdown(p) || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm") || strings.HasSuffix(p, ".docx")
}

// TestEveryBuildDerivesDocumentsAndHTML: DOCX and HTML are indexed from their derived text under
// golib's identities; a page's script and chrome never reach the index; an .html file a previous
// build indexed as Markdown is read again, once, as derived text.
func TestEveryBuildDerivesDocumentsAndHTML(t *testing.T) {
	ctx := context.Background()
	reg := everyBuild(t, nil)
	e := newEnv(t, Options{Match: documentsToo, Registrations: reg})
	// an .html file recorded as Markdown, as a build before this one did
	e.write("old.html", "<p>osprey</p>")
	fi, err := e.fsys.Stat(ctx, "old.html")
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO document(workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, 'old.html', ?, 1, ?, 0)`,
		e.ws, string(fi.Version), "c"+ChunkerVersion+".s3.t512")
	e.open(Options{Match: documentsToo, Registrations: reg})

	page := `<html><head><title>Page</title><script>exfiltrate()</script></head><body><nav>menubar</nav>` +
		`<main><h1>Kestrel</h1><p>hovers <b>still</b></p></main></body></html>`
	e.put("page.html", page, "notes.docx", string(docx(t, "Falcon stoops")))
	for p, prefix := range map[string]string{"page.html": "d.html:golib/html@1+", "notes.docx": "d.docx:golib/docx@1+"} {
		if got := e.indexerOf(p); !strings.HasPrefix(got, prefix) {
			t.Errorf("%s recorded %q, want %s…", p, got, prefix)
		}
	}
	for q, want := range map[string]string{"kestrel": "page.html", "falcon": "notes.docx"} {
		r := e.query(q, QueryOpts{Mode: ModeLexical})
		if len(r.Hits) == 0 || r.Hits[0].Path != want {
			t.Errorf("%q: %+v, want a hit in %s", q, r.Hits, want)
		}
	}
	for _, q := range []string{"exfiltrate", "menubar"} {
		if r := e.query(q, QueryOpts{Mode: ModeLexical}); len(r.Hits) != 0 {
			t.Errorf("%q, the page's noise, is in the index: %+v", q, r.Hits)
		}
	}
	e.ix.Touch("old.html")
	e.eventually("old.html read again as derived text", func() bool {
		return strings.HasPrefix(e.indexerOf("old.html"), "d.html:golib/html@1+")
	})
}

// docx is the smallest DOCX: one paragraph.
func docx(t *testing.T, para string) []byte {
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
