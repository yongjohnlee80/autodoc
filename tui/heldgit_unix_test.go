//go:build unix

package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// TestAHeldGitStartsOnceItsCopyIsFree: while a writer still holds heldGit's copied git (a parallel
// test's fork that has not reached its exec yet), an exec of it fails with "text file busy"; heldGit
// tries again until the copy starts. The writer lets go at the second refused start, so heldGit
// returns only after it has tried again past a refusal and been refused; and only once its git has
// started, as the git's own witness (heldGitMain writes it as it starts) says.
func TestAHeldGitStartsOnceItsCopyIsFree(t *testing.T) {
	t.Parallel()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	refused := 0 // the starts heldGit saw refused as busy: the second lets the writer go
	heldGitWith(t, procenv.Env(nil).With(heldGitEnv+"_STARTED="+started), func(git string) func() {
		writer, err := os.OpenFile(git, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = writer.Close() }) // a second close, after the second refused start's, only errors
		busy := exec.Command(git, "version")
		busy.Env = append(os.Environ(), heldGitEnv+"="+real)
		switch err := busy.Run(); {
		case err == nil:
			t.Skip("this system starts a file a writer holds: there is nothing to wait out")
		case !errors.Is(err, syscall.ETXTBSY):
			t.Fatalf("the copy, held by a writer: %v; want it refused as busy", err)
		}
		return func() {
			if refused++; refused == 2 {
				_ = writer.Close()
			}
		}
	})
	if refused < 2 {
		t.Fatalf("heldGit returned after %d refused starts, while the writer still held its git; want it to try until the git starts", refused)
	}
	if witness, err := os.ReadFile(started); err != nil || len(witness) == 0 {
		t.Fatalf("heldGit returned before its git ever started (the git's witness: %q, %v)", witness, err)
	}
}
