//go:build linux || darwin

package fileinuse

import (
	"io/fs"
	"testing"
)

// A file held open refuses nothing on Unix: no error is a file in use.
func TestNothingIsInUseOnUnix(t *testing.T) {
	if Is(fs.ErrPermission) || Is(fs.ErrNotExist) || Is(nil) {
		t.Error("an error read as a file in use")
	}
}
