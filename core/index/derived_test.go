package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/extract"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// pdfDeriver is a build's deriver for tests: a PDF's text is the file's own bytes under a title
// heading, made under the frozen identity unless claim says another. derives counts its calls;
// bytes, when set, is the size it states; reads counts the reads of its texts.
type pdfDeriver struct {
	formats []string
	version string
	claim   string // the version a Derived claims, when not version
	bytes   int64
	fail    atomic.Int32 // a failureMode of the deriver's own, none when 0
	derives atomic.Int64
	reads   atomic.Int64
	mu      sync.Mutex
	seen    []string
}

func (d *pdfDeriver) Formats() []string {
	if d.formats != nil {
		return d.formats
	}
	return []string{".pdf"}
}

func (d *pdfDeriver) Describe(f string) (string, string) {
	return "fake" + strings.Replace(f, ".", "/", 1), d.version
}

// The ways a pdfDeriver fails of its own: a cache's eviction miss on every call, an error, a panic.
const (
	failMiss int32 = iota + 1
	failError
	failPanic
)

func (d *pdfDeriver) Derive(_ context.Context, name string, r io.ReaderAt, size int64) (registrations.Derived, error) {
	d.derives.Add(1)
	d.mu.Lock()
	d.seen = append(d.seen, name)
	d.mu.Unlock()
	switch d.fail.Load() {
	case failMiss:
		return registrations.Derived{}, fmt.Errorf("evicted: %w", fs.ErrNotExist)
	case failError:
		return registrations.Derived{}, errors.New("the staging lease is busy")
	case failPanic:
		panic("pdfDeriver")
	}
	src := make([]byte, min(size, 1<<10)) // a container's first bytes are its text here
	if _, err := r.ReadAt(src, 0); err != nil && err != io.EOF {
		return registrations.Derived{}, err
	}
	text := "# Derived\n\n" + string(src) + "\n"
	id, v := d.Describe(".pdf")
	if strings.HasSuffix(name, ".docx") {
		id, v = d.Describe(".docx")
	}
	if d.claim != "" {
		v = d.claim
	}
	n := int64(len(text))
	if d.bytes != 0 {
		n = d.bytes
	}
	return registrations.Derived{Text: &readCounter{Reader: strings.NewReader(text), n: &d.reads}, Bytes: n,
		Info: extract.Info{Title: "Manual " + name}, ID: id, Version: v}, nil
}

type readCounter struct {
	io.Reader
	n *atomic.Int64
}

func (r *readCounter) Read(p []byte) (int, error) { r.n.Add(1); return r.Reader.Read(p) }
func (r *readCounter) Close() error               { return nil }

func deriving(t testing.TB, d registrations.Deriver) *registrations.Table {
	t.Helper()
	reg, err := registrations.New(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func withPDF(p string) bool {
	return testMatch(p) || strings.HasSuffix(p, ".pdf") || strings.HasSuffix(p, ".docx")
}

// TestADerivedDocumentIsIndexed: a PDF the build derives is indexed from its derived text, chunked
// as Markdown, titled as the deriver says, and recorded under the derived form; a PDF larger than a
// text file may be, but within the container's bound, is derived, not refused.
func TestADerivedDocumentIsIndexed(t *testing.T) {
	d := &pdfDeriver{version: "1"}
	e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, d)})
	e.put("manual.pdf", "kestrel hovering procedure")
	if got, want := e.indexerOf("manual.pdf"), "d.pdf:fake/pdf@1+c"+ChunkerVersion+".s3.t512"; got != want {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	r := e.query("kestrel", QueryOpts{Mode: ModeLexical})
	if len(r.Hits) != 1 || r.Hits[0].Path != "manual.pdf" || r.Hits[0].Hold != "" || !strings.HasPrefix(r.Hits[0].Breadcrumb, "Manual manual.pdf") {
		t.Fatalf("hits %+v", r.Hits)
	}
	e.put("big.pdf", "falcon stoop "+strings.Repeat("x", MaxFileSize))
	if r := e.query("falcon", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 1 || r.Hits[0].Path != "big.pdf" {
		t.Fatalf("a PDF over a text file's bound was not derived: %+v", r.Hits)
	}
	e.failing("", "")
}

// TestADerivedTextUnderAnotherIdentityIsRefused: a Derived claiming another version than the frozen
// one is not indexed; the job records why, and waits for the file to change.
func TestADerivedTextUnderAnotherIdentityIsRefused(t *testing.T) {
	d := &pdfDeriver{version: "1", claim: "2"}
	e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, d), RetryDelay: time.Millisecond})
	e.write("a.pdf", "kestrel")
	e.ix.Touch("a.pdf")
	e.eventually("a.pdf failing", func() bool { ok, _, _ := e.job("a.pdf"); return ok && d.derives.Load() > 0 && e.pendingJobs() == 1 })
	e.eventually("the refusal recorded", func() bool { _, n, _ := e.job("a.pdf"); return n == 1 })
	e.failing("a.pdf", "derived as fake/pdf@2, not the frozen fake/pdf@1")
	time.Sleep(50 * time.Millisecond)
	if n := d.derives.Load(); n != 1 {
		t.Fatalf("derived %d times: a refusal of the file's own is retried", n)
	}
	if _, ok := e.store.Version("a.pdf"); ok {
		t.Fatal("a refused text was indexed")
	}
}

// TestOversizedDerivedTextIsRefusedUnread: a derived text whose stated size is over derived.MaxText
// is refused before a byte of it is read, and the document indexed before leaves the index.
func TestOversizedDerivedTextIsRefusedUnread(t *testing.T) {
	d := &pdfDeriver{version: "1"}
	e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, d)})
	e.put("a.pdf", "kestrel")
	d.bytes = derived.MaxText + 1
	d.reads.Store(0)
	e.write("a.pdf", "kestrel, now with a gazetteer")
	e.ix.Touch("a.pdf")
	e.eventually("a.pdf refused", func() bool { _, ok := e.store.Version("a.pdf"); return !ok })
	e.failing("a.pdf", fmt.Sprintf("%d bytes of text, over %d", derived.MaxText+1, derived.MaxText))
	if n := d.reads.Load(); n != 0 {
		t.Fatalf("the oversized text was read %d times", n)
	}
}

// TestAFormatTheBuildDerivesIsNotHeld: a document another deriver recorded is derived again by a
// build that derives its format, under this build's identity; a format the build does not derive
// stays held, and its file is never derived.
func TestAFormatTheBuildDerivesIsNotHeld(t *testing.T) {
	ctx := context.Background()
	both := &pdfDeriver{version: "1", formats: []string{".pdf", ".docx"}}
	e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, both)})
	e.put("a.pdf", "kestrel", "b.docx", "falcon")
	pdfOnly := &pdfDeriver{version: "7"}
	e.open(Options{Match: withPDF, Registrations: deriving(t, pdfOnly)})
	e.eventually("a.pdf derived again", func() bool {
		return e.indexerOf("a.pdf") == "d.pdf:fake/pdf@7+c"+ChunkerVersion+".s3.t512" && e.pendingJobs() == 0
	})
	e.ix.Touch("b.docx")
	e.eventually("b.docx checked", func() bool {
		st, err := e.ix.Status(ctx)
		return err == nil && st.Held == 1 && st.HeldUnchecked == 0
	})
	if got := e.query("falcon", QueryOpts{Mode: ModeLexical}); len(got.Hits) != 1 || got.Hits[0].Hold != HoldCurrent {
		t.Fatalf("b.docx's hits %+v, want held and current", got.Hits)
	}
	pdfOnly.mu.Lock()
	defer pdfOnly.mu.Unlock()
	if len(pdfOnly.seen) != 1 || pdfOnly.seen[0] != "a.pdf" {
		t.Fatalf("derived %q, want a.pdf alone", pdfOnly.seen)
	}
}

// TestTheDeriversOwnFailureKeepsTheRow: a deriver that misses twice, fails or panics fails the
// document for now: its indexed row stays, searchable, and its job is tried again with the backoff,
// each try a Derive; once the deriver answers, the file's new text is indexed.
func TestTheDeriversOwnFailureKeepsTheRow(t *testing.T) {
	for _, c := range []struct {
		name string
		mode int32
		why  string
	}{
		{"two misses", failMiss, "its text was evicted twice"},
		{"an error", failError, "the staging lease is busy"},
		{"a panic", failPanic, "the deriver panicked: pdfDeriver"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &pdfDeriver{version: "1"}
			e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, d), RetryDelay: time.Millisecond, MaxRetryDelay: 5 * time.Millisecond})
			e.put("a.pdf", "kestrel")
			indexed, _ := e.store.Version("a.pdf")
			d.fail.Store(c.mode)
			before := d.derives.Load()
			e.write("a.pdf", "kestrel and merlin")
			e.ix.Touch("a.pdf")
			e.eventually("tried again", func() bool { _, n, _ := e.job("a.pdf"); return n >= 3 })
			e.failing("a.pdf", c.why)
			if n := d.derives.Load() - before; n < 3 {
				t.Fatalf("%d Derive calls over three tries", n)
			}
			if v, ok := e.store.Version("a.pdf"); !ok || v != indexed {
				t.Fatalf("the row left or moved: %q, %v", v, ok)
			}
			if r := e.query("kestrel", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 1 {
				t.Fatalf("the document is no longer searchable: %+v", r.Hits)
			}
			d.fail.Store(0)
			e.indexedAt("a.pdf")
			if r := e.query("merlin", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 1 {
				t.Fatalf("the new text was not indexed once the deriver answered: %+v", r.Hits)
			}
		})
	}
}

// TestARefusalDeletesOnlyARowItsFileLeft: a build whose deriver delivers text under another identity
// than it describes re-derives an unchanged file for the identity alone; the refusal keeps the row,
// still searchable, and fails the job without trying again. Once the file is edited, the same
// refusal deletes the row: its text is no longer the file's.
func TestARefusalDeletesOnlyARowItsFileLeft(t *testing.T) {
	e := newEnv(t, Options{Match: withPDF, Registrations: deriving(t, &pdfDeriver{version: "1"}), RetryDelay: time.Millisecond})
	e.put("a.pdf", "kestrel")
	recorded := e.indexerOf("a.pdf")
	liar := &pdfDeriver{version: "2", claim: "1"}
	e.open(Options{Match: withPDF, Registrations: deriving(t, liar), RetryDelay: time.Millisecond})
	e.eventually("the refusal recorded", func() bool { _, n, _ := e.job("a.pdf"); return n == 1 })
	e.failing("a.pdf", "derived as fake/pdf@1, not the frozen fake/pdf@2")
	time.Sleep(50 * time.Millisecond)
	if n := liar.derives.Load(); n != 1 {
		t.Fatalf("derived %d times: a refusal is tried again", n)
	}
	if got := e.indexerOf("a.pdf"); got != recorded {
		t.Fatalf("the row changed: %q, was %q", got, recorded)
	}
	if r := e.query("kestrel", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 1 {
		t.Fatalf("an unchanged file's row was deleted: %+v", r.Hits)
	}
	e.write("a.pdf", "kestrel, edited")
	e.ix.Touch("a.pdf")
	e.eventually("the edited file's row deleted", func() bool { _, ok := e.store.Version("a.pdf"); return !ok })
}
