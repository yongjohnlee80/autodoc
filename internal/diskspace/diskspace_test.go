package diskspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The contract every OS keeps: a directory's file system has some free space (a test's temporary
// directory can be written to), and a directory that is not there is os.ErrNotExist, never zero.
func TestFreeReadsADirectorysFileSystem(t *testing.T) {
	dir := t.TempDir()
	n, err := Free(dir)
	if err != nil {
		t.Fatalf("Free(%s): %v", dir, err)
	}
	if n == 0 {
		t.Errorf("Free(%s) = 0: the test's own directory can be written to", dir)
	}
	if _, err := Free(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Free of a missing directory: %v, want os.ErrNotExist", err)
	}
}

// Two directories on one file system read about the same free space: Free reads the file system,
// not the directory. (A write between the reads may move it; a gigabyte of slack holds the cell.)
func TestFreeIsTheFileSystemsNotTheDirectorys(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := Free(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Free(sub)
	if err != nil {
		t.Fatal(err)
	}
	diff := int64(a) - int64(b)
	if diff < -(1<<30) || diff > 1<<30 {
		t.Errorf("Free(%s) = %d, Free(%s) = %d: one file system, two answers", dir, a, sub, b)
	}
}
