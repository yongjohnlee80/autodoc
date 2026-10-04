package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// pdfMarkdown is a build's deriver for tests: a PDF's text is the file's own bytes, or fails.
type pdfMarkdown struct{ fail bool }

func (pdfMarkdown) Formats() []string                { return []string{".pdf"} }
func (pdfMarkdown) Describe(string) (string, string) { return "fake/pdf", "1" }
func (d pdfMarkdown) Derive(_ context.Context, _ string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	if d.fail {
		return registrations.Derived{}, errors.New("encrypted")
	}
	b := make([]byte, size)
	_, _ = r.ReadAt(b, 0)
	return registrations.Derived{Text: io.NopCloser(strings.NewReader(string(b))), Bytes: size, ID: "fake/pdf", Version: "1"}, nil
}

// TestADerivedDocumentOnTheWire: doc.read serves a PDF's derived text, doc.outline its headings,
// and doc.write refuses it as read-only; a text that cannot be derived says so.
func TestADerivedDocumentOnTheWire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fsys := memfs.New()
	if _, err := fsys.WriteFile(ctx, "manual.pdf", strings.NewReader("# Manual\n\n## Setup\n\nkestrel\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.WriteFile(ctx, "locked.pdf", strings.NewReader("%PDF")); err != nil {
		t.Fatal(err)
	}
	serveWith := func(name string, d registrations.Deriver) *Workspace {
		reg, err := registrations.New(nil, d)
		if err != nil {
			t.Fatal(err)
		}
		row, err := db.AddWorkspace(ctx, name, "/roots/"+name, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		pdf := func(p string) bool { return strings.HasSuffix(p, ".pdf") }
		ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: pdf, BatchDelay: 5 * time.Millisecond, Registrations: reg})
		return &Workspace{Name: name, Index: ix, Docs: docs.New(fsys, pdf, docs.WithRegistrations(reg.Kinds()), docs.WithDeriver(reg))}
	}
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(serveWith("kb", pdfMarkdown{}), serveWith("locked", pdfMarkdown{fail: true})), "v-test", WithListener(ln))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	cli := (&rig{t: t, sock: sock}).dial(true)

	doc := call(t, cli, "doc.read", "kb", "manual.pdf").(map[string]any)
	if string(doc["content"].([]byte)) != "# Manual\n\n## Setup\n\nkestrel\n" || doc["version"] == "" {
		t.Fatalf("doc.read %v", doc)
	}
	hs := call(t, cli, "doc.outline", "kb", "manual.pdf").(map[string]any)["headings"].([]any)
	if len(hs) != 2 || hs[1].(map[string]any)["text"] != "Setup" {
		t.Fatalf("doc.outline %v", hs)
	}
	var re *golibrpc.Error
	_, err = cli.Call(ctx, "doc.write", "kb", "manual.pdf", []byte("x"), doc["version"])
	if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.HasPrefix(re.Message, "a derived document is read-only") {
		t.Fatalf("doc.write: %v", err)
	}
	_, err = cli.Call(ctx, "doc.read", "locked", "locked.pdf")
	if !errors.As(err, &re) || re.Code != CodeUnsupported || !strings.Contains(re.Message, "could not be derived") {
		t.Fatalf("doc.read of a text that cannot be derived: %v", err)
	}
}
