package derived

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
	"github.com/yongjohnlee80/golib/vfs/local"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// deriver is a build's deriver for tests: its text is "# <name>\n\n" and the file's bytes, read
// through the io.ReaderAt it is handed; misses are the eviction misses it answers first, and id
// and version are the identity it claims, the frozen one by default.
type deriver struct {
	misses      atomic.Int32
	calls       atomic.Int32
	id, version string
	fail        error
	panics      bool
	noText      bool
	bytes       int64 // the size it gives, when not 0
	closed      atomic.Int32
}

func (d *deriver) Formats() []string                    { return []string{".pdf"} }
func (d *deriver) Describe(string) (id, version string) { return "fake/pdf", "1" }
func (d *deriver) Derive(_ context.Context, name string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	d.calls.Add(1)
	if d.misses.Add(-1) >= 0 {
		return registrations.Derived{}, fmt.Errorf("evicted: %w", fs.ErrNotExist)
	}
	switch {
	case d.panics:
		panic("fake deriver")
	case d.fail != nil:
		return registrations.Derived{}, d.fail
	case d.noText:
		return registrations.Derived{ID: "fake/pdf", Version: "1"}, nil
	}
	src := make([]byte, size)
	if n, err := r.ReadAt(src, 0); int64(n) != size || (err != nil && err != io.EOF) {
		return registrations.Derived{}, fmt.Errorf("read %d of %d: %v", n, size, err)
	}
	text := "# " + name + "\n\n" + string(src)
	id, version := "fake/pdf", "1"
	if d.id != "" {
		id, version = d.id, d.version
	}
	n := int64(len(text))
	if d.bytes != 0 {
		n = d.bytes
	}
	return registrations.Derived{Text: &counted{Reader: strings.NewReader(text), closed: &d.closed}, Bytes: n, ID: id, Version: version}, nil
}

type counted struct {
	io.Reader
	closed *atomic.Int32
}

func (c *counted) Close() error { c.closed.Add(1); return nil }

func table(t *testing.T, d registrations.Deriver) *registrations.Table {
	t.Helper()
	reg, err := registrations.New(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// roots runs a cell on both drivers: a local file is read through its own ReadAt, memfs at offsets.
func roots(t *testing.T, cell func(t *testing.T, fsys vfs.FS)) {
	t.Run("local", func(t *testing.T) {
		fsys, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cell(t, fsys)
	})
	t.Run("memfs", func(t *testing.T) { cell(t, memfs.New()) })
}

func put(t *testing.T, fsys vfs.FS, p, content string) int64 {
	t.Helper()
	fi, err := fsys.WriteFile(context.Background(), p, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size
}

// TestADocumentsTextIsDerived: the deriver reads the file at offsets on either driver, and its text
// is read whole under the limit.
func TestADocumentsTextIsDerived(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		d := &deriver{}
		size := put(t, fsys, "a.pdf", "%PDF body")
		got, err := Text(context.Background(), table(t, d), fsys, "a.pdf", size)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Read(got, MaxText)
		if err != nil || string(b) != "# a.pdf\n\n%PDF body" {
			t.Fatalf("%q, %v", b, err)
		}
		if d.closed.Load() != 1 {
			t.Fatal("Read did not close the text")
		}
	})
}

// TestAnEvictionMissIsRetriedOnce: one miss costs one more Derive; two misses are the error.
func TestAnEvictionMissIsRetriedOnce(t *testing.T) {
	fsys := memfs.New()
	size := put(t, fsys, "a.pdf", "x")
	d := &deriver{}
	d.misses.Store(1)
	got, err := Text(context.Background(), table(t, d), fsys, "a.pdf", size)
	if err != nil || d.calls.Load() != 2 {
		t.Fatalf("one miss: %v after %d calls, want the text after 2", err, d.calls.Load())
	}
	_ = got.Text.Close()
	d = &deriver{}
	d.misses.Store(2)
	if _, err := Text(context.Background(), table(t, d), fsys, "a.pdf", size); !errors.Is(err, ErrDeriverFailed) || errors.Is(err, fs.ErrNotExist) || d.calls.Load() != 2 {
		t.Fatalf("two misses: %v after %d calls, want the deriver's failure after 2, not a file gone", err, d.calls.Load())
	}
}

// TestADerivedTextIsRefused: the deriver's own failures (an error, a panic, no text) are
// ErrDeriverFailed; a text it delivered under another identity or with a negative size is
// ErrRefused, and closed; a format the build does not derive is ErrNoDeriver.
func TestADerivedTextIsRefused(t *testing.T) {
	fsys := memfs.New()
	size := put(t, fsys, "a.pdf", "x")
	put(t, fsys, "b.docx", "x")
	for name, c := range map[string]struct {
		d    *deriver
		path string
		want error
		why  string
	}{
		"another identity": {&deriver{id: "fake/pdf", version: "2"}, "a.pdf", ErrRefused, "not the frozen fake/pdf@1"},
		"a failure":        {&deriver{fail: errors.New("encrypted")}, "a.pdf", ErrDeriverFailed, "encrypted"},
		"a panic":          {&deriver{panics: true}, "a.pdf", ErrDeriverFailed, "the deriver panicked: fake deriver"},
		"no text":          {&deriver{noText: true}, "a.pdf", ErrDeriverFailed, "gave no text"},
		"a negative size":  {&deriver{bytes: -1}, "a.pdf", ErrRefused, "size as -1"},
		"another format":   {&deriver{}, "b.docx", ErrNoDeriver, "b.docx"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Text(context.Background(), table(t, c.d), fsys, c.path, size)
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("%v, want %v saying %q", err, c.want, c.why)
			}
			if made := c.d.calls.Load() > 0 && c.d.fail == nil && !c.d.panics && !c.d.noText; made && c.d.closed.Load() != 1 {
				t.Fatal("a refused text was not closed")
			}
		})
	}
	if _, err := Text(context.Background(), nil, fsys, "a.pdf", size); !errors.Is(err, ErrNoDeriver) {
		t.Fatalf("the community build derived: %v", err)
	}
	if _, err := Text(context.Background(), table(t, &deriver{}), fsys, "gone.pdf", 1); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a file gone: %v", err)
	}
}

// TestOversizedTextIsRefusedBeforeItIsRead: Bytes over the limit refuses with nothing read; text
// longer than its Bytes said is refused once it passes the limit.
func TestOversizedTextIsRefusedBeforeItIsRead(t *testing.T) {
	r := &counting{}
	if _, err := Read(registrations.Derived{Text: r, Bytes: 11}, 10); !errors.Is(err, ErrTooLarge) || r.reads != 0 || !r.closed {
		t.Fatalf("%v after %d reads (closed %v), want refused before any", err, r.reads, r.closed)
	}
	lying := registrations.Derived{Text: io.NopCloser(strings.NewReader(strings.Repeat("a", 11))), Bytes: 3}
	if _, err := Read(lying, 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("text past the limit: %v", err)
	}
	failing := registrations.Derived{Text: io.NopCloser(iotestErr{}), Bytes: 3}
	if _, err := Read(failing, 10); err == nil {
		t.Fatal("a failed read returned text")
	}
}

type counting struct {
	reads  int
	closed bool
}

func (c *counting) Read([]byte) (int, error) { c.reads++; return 0, io.EOF }
func (c *counting) Close() error             { c.closed = true; return nil }

type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, errors.New("disk") }
