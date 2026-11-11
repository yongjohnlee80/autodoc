//go:build unix

package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestReadDirIfAnyPassesOnAnyOtherFailure: a failure that is neither "nothing there" nor "not a
// folder" is the read's own error. A path under a file is one on Unix: its read and its Stat both
// say ENOTDIR. (On Windows such a path reads as nothing there, the absent answer.)
func TestReadDirIfAnyPassesOnAnyOtherFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	es, err := ReadDirIfAny(filepath.Join(file, "sub"))
	if es != nil || err == nil || errors.Is(err, ErrNotAFolder) || !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("a path under a file: %v, %v; want the read's own error (ENOTDIR)", es, err)
	}
}
