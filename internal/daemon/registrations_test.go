package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// wholeFile is a registered chunker for tests: one chunk, the whole file.
type wholeFile struct{}

func (wholeFile) Version() string { return "whole-1" }
func (wholeFile) Chunk(d search.Doc) ([]search.Chunk, error) {
	return []search.Chunk{{Breadcrumb: d.Path, Body: string(d.Text), ByteEnd: len(d.Text)}}, nil
}

// openOn serves the store at path with o, as a daemon would after a restart, until close.
func openOn(t *testing.T, path string, o Options) (m *Workspaces, close func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	o.Poll, o.BatchDelay = 20*time.Millisecond, 5*time.Millisecond
	m = New(ctx, db, o)
	if err := m.OpenAll(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	close = func() {
		once.Do(func() {
			m.StopAll()
			cancel()
			_ = db.Close()
		})
	}
	t.Cleanup(close)
	return m, close
}

func goRegistered(t *testing.T) *registrations.Table {
	t.Helper()
	reg, err := registrations.New(map[string]search.Chunker{".go": wholeFile{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestARegistrationRefusesItsExtensionAsText: a workspace may not declare a registered extension
// as its own text type, through either verb; the registered file is read as its chunker's, by the
// indexer and by the document API.
func TestARegistrationRefusesItsExtensionAsText(t *testing.T) {
	m, _ := openOn(t, filepath.Join(t.TempDir(), "autodoc.db"), Options{Registrations: goRegistered(t)})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.go"}}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 1)
	var e *kind.ErrExtension
	if _, err := m.SetTextExtensions(context.Background(), "kb", []string{".log", "GO"}); !errors.As(err, &e) || e.Ext != ".go" ||
		!strings.Contains(err.Error(), "registered chunker") {
		t.Fatalf("SetTextExtensions .go: %v, want a refusal naming it", err)
	}
	exts := []string{".go"}
	if err := m.Configure(context.Background(), "kb", store.Changes{TextExtensions: &exts}); !errors.As(err, &e) {
		t.Fatalf("Configure .go: %v, want a refusal", err)
	}
	w, _ := m.Get("kb")
	if got := w.TextExtensions(); len(got) != 0 {
		t.Fatalf("a refused change was kept: %v", got)
	}
	if w.Index.Kind("a.go") != kind.Registered {
		t.Fatal("the indexer does not read a.go as registered")
	}
	if err := os.WriteFile(filepath.Join(root, "bin.go"), []byte{0xff, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Docs.Read(context.Background(), "bin.go"); err == nil {
		t.Fatal("the document API read a registered file that is not UTF-8")
	}
}

// TestAStoredCollisionDoesNotStopTheStart: a text extension a community daemon stored for a
// workspace, which a registered build reads with its chunker, does not stop that build's start:
// the registration wins, workspace.list names the collision, and the log says it once.
func TestAStoredCollisionDoesNotStopTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	root := t.TempDir()
	community, closeCommunity := openOn(t, path, Options{})
	if _, err := community.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.go"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := community.SetTextExtensions(context.Background(), "kb", []string{".go", ".log"}); err != nil {
		t.Fatal(err)
	}
	closeCommunity()
	var log bytes.Buffer
	m, _ := openOn(t, path, Options{Registrations: goRegistered(t), Log: logger.New(logger.WithWriter(&log))})
	w, ok := m.Get("kb")
	if !ok || w.Err != nil {
		t.Fatalf("kb: %v, %v", ok, w)
	}
	if got := w.TextCollisions(); !reflect.DeepEqual(got, []string{".go"}) {
		t.Fatalf("collisions %v", got)
	}
	if w.Index.Kind("a.go") != kind.Registered || w.Index.Kind("a.log") != kind.Text {
		t.Fatal("the registration does not win, or the rest of the text types went with it")
	}
	id := mustID(t, m, "kb")
	m.noteCollisions(id, "kb", []string{".go", ".log"})
	if n := strings.Count(log.String(), "workspace.text_collision"); n != 1 {
		t.Fatalf("the collision was logged %d times, want once:\n%s", n, log.String())
	}
}

// TestAStoredHTMLTextTypeIsADocumentNow: .html, which a build before ADR 1791429611 let a
// workspace declare as plain text, is a derived format now. The daemon still starts; workspace.list
// names the collision; .html is read as a document and .log stays text; a new .htm is refused.
func TestAStoredHTMLTextTypeIsADocumentNow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	root := t.TempDir()
	before, closeBefore := openOn(t, path, Options{})
	if _, err := before.Add(ctx, config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.html", "**/*.log"}}); err != nil {
		t.Fatal(err)
	}
	// as the earlier build stored it: kind.TextExtensions refuses .html now, so the store is written
	if err := before.db.SetWorkspaceTextExtensions(ctx, mustID(t, before, "kb"), []string{".html", ".log"}); err != nil {
		t.Fatal(err)
	}
	closeBefore()
	documents, err := registrations.New(nil, registrations.Documents(250<<20, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	m, _ := openOn(t, path, Options{Registrations: documents, Log: logger.New(logger.WithWriter(&log))})
	w, ok := m.Get("kb")
	if !ok || w.Err != nil {
		t.Fatalf("kb: %v, %v", ok, w)
	}
	if got := w.TextCollisions(); !reflect.DeepEqual(got, []string{".html"}) {
		t.Fatalf("collisions %v", got)
	}
	if w.Index.Kind("page.html") != kind.Derived || w.Index.Kind("app.log") != kind.Text {
		t.Fatal(".html is not read as a document, or .log went with it")
	}
	if !strings.Contains(log.String(), "workspace.text_collision") {
		t.Fatalf("the collision was not logged:\n%s", log.String())
	}
	if _, err := m.SetTextExtensions(ctx, "kb", []string{".log", ".htm"}); err == nil || !strings.Contains(err.Error(), "derived document format") {
		t.Fatalf("a new .htm text type: %v", err)
	}
}
