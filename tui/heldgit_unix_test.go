//go:build unix

package tui

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// TestAHeldGitStartsOnceItsCopyIsFree: while a writer still holds heldGit's copied git (a parallel
// test's fork that has not reached its exec yet), an exec of it fails with "text file busy"; heldGit
// tries again until the copy starts. The writer lets go at the second refused start, so heldGit
// returns only after it has tried again past a refusal and been refused, and its git then runs.
func TestAHeldGitStartsOnceItsCopyIsFree(t *testing.T) {
	t.Parallel()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	refused := 0 // the starts heldGit saw refused as busy: the second lets the writer go
	held, _, _ := heldGitWith(t, procenv.Env(nil), func(git string) func() {
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
	git := exec.Command(procenv.Resolve(held, "git"), "version")
	git.Env = held.Environ()
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("the held git's git version: %v\n%s", err, out)
	}
}
