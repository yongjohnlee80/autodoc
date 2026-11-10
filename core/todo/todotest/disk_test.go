package todotest_test

import (
	"io"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// readBack reads name through dir, opened on d, and answers its content ("" when it can't).
func readBack(t *testing.T, d *todotest.Disk, dir, name string) string {
	t.Helper()
	fsys, _, err := d.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	defer fsys.Close()
	r, err := fsys.Open(ctx, name, 0)
	if err != nil {
		return ""
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// Under Windows' rule, on any OS, the disk opens a drive's root and a share's, each the path it is
// in the slash form, held under that path; a rooted /kb is as it was; and two volumes are two
// places: what is written under C:/kb is not under D:/kb, nor under /kb, nor under a share.
func TestTheDiskKeepsEachVolumeApartUnderWindowsRule(t *testing.T) {
	d := todotest.NewDiskFor(true)
	roots := []string{"C:/kb", "D:/kb", "/kb", "//host/share/kb"}
	for _, r := range roots {
		d.MkdirAll(r)
	}
	d.WriteFile("C:/kb/note.md", []byte("on C"))
	for _, r := range roots {
		fsys, clean, err := d.Open(ctx, r)
		if err != nil || clean != r {
			t.Fatalf("open %s: %q, %v; want it opened as itself", r, clean, err)
		}
		if got := d.Held(); got[r] != 1 {
			t.Errorf("held after opening %s: %v; want it held once, under its own path", r, got)
		}
		_ = fsys.Close()
	}
	if got := readBack(t, d, "C:/kb", "note.md"); got != "on C" {
		t.Errorf("C:/kb/note.md: %q", got)
	}
	for _, r := range []string{"D:/kb", "/kb", "//host/share/kb"} {
		if got := readBack(t, d, r, "note.md"); got != "" {
			t.Errorf("%s/note.md holds %q: two volumes met", r, got)
		}
	}
	if _, _, err := d.Open(ctx, "C:kb"); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Errorf("C:kb (a drive's current folder): %v; want it refused", err)
	}
	for dir, n := range d.Held() {
		if n != 0 {
			t.Errorf("%s still held %d times", dir, n)
		}
	}
}

// Under Unix' rule, as before: /kb opens as /kb, and a drive's path is not absolute.
func TestTheDiskUnderUnixRuleIsAsItWas(t *testing.T) {
	d := todotest.NewDiskFor(false)
	d.MkdirAll("/kb")
	d.WriteFile("/kb/note.md", []byte("rooted"))
	if got := readBack(t, d, "/kb", "note.md"); got != "rooted" {
		t.Errorf("/kb/note.md: %q", got)
	}
	if _, _, err := d.Open(ctx, "C:/kb"); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Errorf("C:/kb under Unix' rule: %v; want it refused, not absolute", err)
	}
}

// Under Windows' rule, on any OS, the disk's helpers take a path in either of Windows' forms: a file
// written as C:\fresh\note.md is C:/fresh/note.md, its folder made with it.
func TestTheDiskWritesWindowsOwnFormUnderWindowsRule(t *testing.T) {
	d := todotest.NewDiskFor(true)
	d.WriteFile(`C:\fresh\note.md`, []byte("native"))
	if got := readBack(t, d, "C:/fresh", "note.md"); got != "native" {
		t.Errorf("C:/fresh/note.md: %q; want what was written as C:\\fresh\\note.md", got)
	}
	if b, err := d.ReadFile(`C:\fresh\note.md`); err != nil || string(b) != "native" {
		t.Errorf("read back in Windows' own form: %q, %v", b, err)
	}
}
