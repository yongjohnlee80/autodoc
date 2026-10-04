package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// derivations is a build's deriver for tests, kept outside the daemon so its calls are counted
// across restarts: each format's version is versions', and every call is recorded by file name.
type derivations struct {
	mu       sync.Mutex
	versions map[string]string // by format
	calls    []string
}

func (d *derivations) Formats() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for f := range d.versions {
		out = append(out, f)
	}
	return out
}

func (d *derivations) Describe(f string) (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return "fake/" + strings.TrimPrefix(f, "."), d.versions[f]
}

func (d *derivations) Derive(_ context.Context, name string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	d.mu.Lock()
	d.calls = append(d.calls, filepath.Base(name))
	d.mu.Unlock()
	b := make([]byte, size)
	if _, err := r.ReadAt(b, 0); err != nil && err != io.EOF {
		return registrations.Derived{}, err
	}
	id, v := d.Describe(filepath.Ext(name))
	return registrations.Derived{Text: io.NopCloser(strings.NewReader(string(b))), Bytes: size, ID: id, Version: v}, nil
}

// since are the calls after the first n, sorted.
func (d *derivations) since(n int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Sorted(slices.Values(d.calls[n:]))
}

func (d *derivations) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

// buildWith is a build whose deriver offers versions, sharing d's record of calls.
func buildWith(t *testing.T, d *derivations, versions map[string]string) *registrations.Table {
	t.Helper()
	d.mu.Lock()
	d.versions = versions
	d.mu.Unlock()
	reg, err := registrations.New(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// settled waits for cond, then for the index to stay idle a while: no job and no new derivation.
func settled(t *testing.T, m *Workspaces, d *derivations, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := m.Get("kb")
	for n, quiet := d.count(), 0; quiet < 30; time.Sleep(10 * time.Millisecond) {
		st, err := w.Index.Status(context.Background())
		if err != nil || st.PendingJobs != 0 || d.count() != n {
			n, quiet = d.count(), 0
			if time.Now().After(deadline) {
				t.Fatalf("%s: the index did not settle", what)
			}
			continue
		}
		quiet++
	}
}

// TestARestartDerivesOnlyWhatChanged counts Derive calls, outside the daemon, across restarts of a
// daemon over one store: unchanged files add none while a file changed while it was down adds one,
// which proves the restart scanned; one format's new version adds exactly that format's files; and
// a format the build no longer offers stays held, its documents searchable, never derived.
func TestARestartDerivesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.pdf", "# A\n\nkestrel\n")
	write("b.pdf", "# B\n\nfalcon\n")
	write("control.pdf", "# Control\n\nmerlin\n")
	write("c.docx", "# C\n\nharrier\n")
	write("d.docx", "# D\n\nosprey\n")
	write("n.md", "# N\n\nnotes\n")
	d := &derivations{}
	v1 := map[string]string{".pdf": "1", ".docx": "1"}
	m, stop := openOn(t, path, Options{Registrations: buildWith(t, d, v1)})
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root,
		Include: []string{"**/*.pdf", "**/*.docx", "**/*.md"}}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 6)
	if got := d.since(0); !slices.Equal(got, []string{"a.pdf", "b.pdf", "c.docx", "control.pdf", "d.docx"}) {
		t.Fatalf("the first index derived %q", got)
	}
	stop()

	// unchanged files add none; the control file changed while the daemon was down adds one
	write("control.pdf", "# Control\n\nmerlin, edited while the daemon was down\n")
	n := d.count()
	m, stop = openOn(t, path, Options{Registrations: buildWith(t, d, v1)})
	settled(t, m, d, "the control file derived again", func() bool { return slices.Contains(d.since(n), "control.pdf") })
	if got := d.since(n); !slices.Equal(got, []string{"control.pdf"}) {
		t.Fatalf("a restart over unchanged files derived %q, want the control file alone", got)
	}
	stop()

	// one format's new version adds exactly its files
	n = d.count()
	m, stop = openOn(t, path, Options{Registrations: buildWith(t, d, map[string]string{".pdf": "1", ".docx": "2"})})
	settled(t, m, d, "the DOCX files derived again", func() bool { return len(d.since(n)) >= 2 })
	if got := d.since(n); !slices.Equal(got, []string{"c.docx", "d.docx"}) {
		t.Fatalf("a new DOCX version derived %q, want the DOCX files alone", got)
	}
	stop()

	// a format the build no longer offers stays held; the control file changed again proves the scan
	write("control.pdf", "# Control\n\nmerlin, edited twice\n")
	n = d.count()
	m, _ = openOn(t, path, Options{Registrations: buildWith(t, d, map[string]string{".pdf": "1"})})
	settled(t, m, d, "the control file derived again", func() bool { return slices.Contains(d.since(n), "control.pdf") })
	if got := d.since(n); !slices.Equal(got, []string{"control.pdf"}) {
		t.Fatalf("a build without DOCX derived %q, want the control file alone", got)
	}
	w, _ := m.Get("kb")
	st, err := w.Index.Status(context.Background())
	if err != nil || st.Held != 2 || st.Docs != 6 {
		t.Fatalf("status %+v, %v: want the two DOCX files held among six documents", st, err)
	}
	for _, p := range []string{"c.docx", "d.docx"} {
		if _, ok := w.Index.Store().Version(p); !ok {
			t.Fatalf("held %s left the index", p)
		}
	}
	// the document API reads what the build derives, read-only, and refuses what it holds
	doc, err := w.Docs.Read(context.Background(), "a.pdf")
	if err != nil || string(doc.Content) != "# A\n\nkestrel\n" {
		t.Fatalf("doc.read a.pdf: %q, %v", doc.Content, err)
	}
	if _, err := w.Docs.Write(context.Background(), "a.pdf", []byte("x"), doc.Version); !errors.Is(err, docs.ErrReadOnly) {
		t.Fatalf("doc.write a.pdf: %v", err)
	}
	if _, err := w.Docs.Read(context.Background(), "c.docx"); !errors.Is(err, docs.ErrNotEligible) {
		t.Fatalf("doc.read of a held DOCX file: %v", err)
	}
}
