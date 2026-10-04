package docs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"
	"github.com/yongjohnlee80/golib/vfs/memfs"
)

func md(p string) bool { return strings.HasSuffix(p, ".md") }

func TestCommunityDocumentsRejectProFormatsAndInvalidPlainText(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		docs := New(fsys, func(string) bool { return true })
		for _, extension := range []string{".doc", ".docx", ".odt", ".pdf"} {
			if _, err := docs.Write(ctx, "draft"+extension, []byte("data"), ""); !errors.Is(err, ErrNotEligible) {
				t.Errorf("write %s: %v, want not eligible", extension, err)
			}
		}
		for _, extension := range []string{".txt", ".yaml", ".yml"} {
			if _, err := docs.Write(ctx, "bad"+extension, []byte{0xff}, ""); !errors.Is(err, ErrNotEligible) {
				t.Errorf("invalid UTF-8 %s: %v, want not eligible", extension, err)
			}
		}
		if _, err := docs.Write(ctx, "good.txt", []byte("plain text"), ""); err != nil {
			t.Fatal(err)
		}
	})
}

// roots runs a cell on both drivers: the one the daemon serves (vfs/local) and memfs.
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

func content(t *testing.T, fsys vfs.FS, p string) string {
	t.Helper()
	r, err := fsys.Open(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestWriteReadConditional(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		d := New(fsys, md)
		v1, err := d.Write(ctx, "a.md", []byte("one"), "")
		if err != nil || v1 == "" {
			t.Fatalf("create: %q, %v", v1, err)
		}
		doc, err := d.Read(ctx, "a.md")
		if err != nil || string(doc.Content) != "one" || doc.Version != v1 {
			t.Fatalf("read: %+v, %v", doc, err)
		}
		// a create over an existing path conflicts, and the file is untouched
		var ce *vfs.ConflictError
		if _, err := d.Write(ctx, "a.md", []byte("clobber"), ""); !errors.As(err, &ce) || content(t, fsys, "a.md") != "one" {
			t.Errorf("create over an existing note: %v; content %q", err, content(t, fsys, "a.md"))
		}
		v2, err := d.Write(ctx, "a.md", []byte("two"), v1)
		if err != nil || v2 == v1 {
			t.Fatalf("write at v1: %q, %v", v2, err)
		}
		// a stale version conflicts, and the file is untouched
		if _, err := d.Write(ctx, "a.md", []byte("stale"), v1); !errors.Is(err, vfs.ErrConflict) || content(t, fsys, "a.md") != "two" {
			t.Errorf("stale write: %v; content %q", err, content(t, fsys, "a.md"))
		}
		if err := d.Remove(ctx, "a.md", v1); !errors.Is(err, vfs.ErrConflict) {
			t.Errorf("stale remove: %v", err)
		}
		if err := d.Remove(ctx, "a.md", v2); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Read(ctx, "a.md"); !errors.Is(err, vfs.ErrNotExist) {
			t.Errorf("read after remove: %v", err)
		}
	})
}

func TestCreateMakesItsFolder(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		d := New(fsys, md)
		if _, err := d.Write(context.Background(), "new/deep/a.md", []byte("x"), ""); err != nil {
			t.Fatal(err)
		}
		if content(t, fsys, "new/deep/a.md") != "x" {
			t.Error("not written")
		}
	})
}

func TestRenameNeverReplaces(t *testing.T) {
	roots(t, func(t *testing.T, fsys vfs.FS) {
		ctx := context.Background()
		d := New(fsys, md)
		for _, p := range []string{"a.md", "b.md"} {
			if _, err := d.Write(ctx, p, []byte(p), ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Rename(ctx, "a.md", "b.md"); !errors.Is(err, vfs.ErrConflict) || content(t, fsys, "b.md") != "b.md" {
			t.Errorf("rename onto a note: %v; b.md %q", err, content(t, fsys, "b.md"))
		}
		if err := d.Rename(ctx, "a.md", "c.md"); err != nil || content(t, fsys, "c.md") != "a.md" {
			t.Errorf("rename: %v", err)
		}
	})
}

// TestOnlyFiles: a path the workspace does not index is refused before any I/O.
func TestOnlyFiles(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	if _, err := fsys.WriteFile(ctx, "run.sh", strings.NewReader("echo")); err != nil {
		t.Fatal(err)
	}
	d := New(fsys, md)
	for name, err := range map[string]error{
		"read":        func() error { _, err := d.Read(ctx, "run.sh"); return err }(),
		"write":       func() error { _, err := d.Write(ctx, "x.sh", []byte("x"), ""); return err }(),
		"remove":      d.Remove(ctx, "run.sh", "v"),
		"rename from": d.Rename(ctx, "run.sh", "a.md"),
		"rename to":   d.Rename(ctx, "a.md", "run.sh"),
	} {
		if !errors.Is(err, ErrNotEligible) || !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if content(t, fsys, "run.sh") != "echo" {
		t.Error("run.sh changed")
	}
}

func TestSizeLimit(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	d := New(fsys, md)
	if _, err := d.Write(ctx, "big.md", make([]byte, MaxSize+1), ""); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversize write: %v", err)
	}
	if _, err := fsys.Stat(ctx, "big.md"); !errors.Is(err, vfs.ErrNotExist) {
		t.Error("the oversize write left a file")
	}
	if _, err := d.Write(ctx, "max.md", make([]byte, MaxSize), ""); err != nil {
		t.Fatalf("a write at the limit: %v", err)
	}
	if _, err := fsys.WriteFile(ctx, "big.md", strings.NewReader(strings.Repeat("x", MaxSize+1))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(ctx, "big.md"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversize read: %v", err)
	}
}

// committing wraps memfs: its writes land, then report a failure after the commit point, as
// vfs/local does when the parent directory's fsync fails.
type committing struct{ *memfs.FS }

func (c committing) WriteFileIf(ctx context.Context, name string, r io.Reader, want vfs.Version, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	fi, err := c.FS.WriteFileIf(ctx, name, r, want, opts...)
	if err != nil {
		return fi, err
	}
	return vfs.FileInfo{}, &vfs.CommitError{Path: name, Info: fi, Err: errors.New("fsync: input/output error")}
}

// TestCommittedIsNotAVersion: a write that landed before a follow-up failed is ErrCommitted with no
// version; the documented recovery (read, compare) finds the write, and a re-read version is one a
// next write can use.
func TestCommittedIsNotAVersion(t *testing.T) {
	ctx := context.Background()
	fsys := committing{memfs.New()}
	d := New(fsys, md)
	v1, err := d.Write(ctx, "a.md", []byte("one"), "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.Write(ctx, "a.md", []byte("two"), v1)
	if !errors.Is(err, ErrCommitted) || v != "" {
		t.Fatalf("committed write: %q, %v", v, err)
	}
	if errors.Is(err, vfs.ErrConflict) {
		t.Error("committed reads as a conflict")
	}
	doc, err := d.Read(ctx, "a.md")
	if err != nil || string(doc.Content) != "two" {
		t.Fatalf("the read after Committed: %+v, %v", doc, err)
	}
	// the client adopts the read version: the next conditional write goes through (and lands,
	// reporting Committed again from this driver)
	if _, err := d.Write(ctx, "a.md", []byte("three"), doc.Version); !errors.Is(err, ErrCommitted) {
		t.Errorf("a write at the adopted version: %v", err)
	}
	if content(t, fsys, "a.md") != "three" {
		t.Errorf("content %q", content(t, fsys, "a.md"))
	}
}
