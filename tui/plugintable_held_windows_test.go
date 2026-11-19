//go:build windows

package tui

import (
	"errors"
	"os"
	"testing"
)

// On Windows, an export over a file another program holds open is refused at its rename: the
// notice says what is known (open elsewhere, or a folder not writable) and that the old file is
// kept, which it is, and the .part is gone. The mutant, named here: the refusal said as the OS's
// error (the generic "not exported" notice).
func TestAnExportOverAFileHeldOpenSaysItCouldNotReplaceIt(t *testing.T) {
	t.Parallel()
	r, path, _ := exportOverAHeldFile(t)
	r.waitNotice(t, notReplacedNotice(path))
	if b, _ := os.ReadFile(path); string(b) != "old\n" {
		t.Errorf("the held file holds %q, want the old file kept", b)
	}
	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the .part is left: %v", err)
	}
}
