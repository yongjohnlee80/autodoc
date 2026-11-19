//go:build linux || darwin

package fileinuse

import (
	"io/fs"
	"syscall"
	"testing"
)

// A file held open refuses nothing on Unix: no error is a file in use, and no rename's refusal a
// replace refused for it.
func TestNothingIsInUseOnUnix(t *testing.T) {
	if Is(fs.ErrPermission) || Is(fs.ErrNotExist) || Is(nil) {
		t.Error("an error read as a file in use")
	}
	if ReplaceRefused(fs.ErrPermission) || ReplaceRefused(syscall.EACCES) || ReplaceRefused(nil) {
		t.Error("a rename's refusal read as a replace refused for a file held open")
	}
}
