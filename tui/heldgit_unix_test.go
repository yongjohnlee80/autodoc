//go:build unix

package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestAHeldGitStartsOnceItsCopyIsFree: while a writer still holds heldGit's copied git (a parallel
// test's fork that has not reached its exec yet), an exec of it fails with "text file busy";
// startsOnceFree tries again until the copy starts. The writer lets go at the second refused start,
// so the copy starts only after startsOnceFree has tried again past a refusal and been refused.
func TestAHeldGitStartsOnceItsCopyIsFree(t *testing.T) {
	t.Parallel()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(t.TempDir(), "git")
	if err := copyFile(exe, git); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(git, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() }) // a second close, after the second refused start's, only errors
	busy := exec.Command(git, "version")
	busy.Env = append(os.Environ(), heldGitEnv+"="+real)
	if err := busy.Run(); !errors.Is(err, syscall.ETXTBSY) {
		t.Skipf("this system starts a file a writer holds (%v): there is nothing to wait out", err)
	}
	refused := 0 // startsOnceFree's refused starts: the second lets the writer go
	startsOnceFree(t, git, real, func() {
		if refused++; refused == 2 {
			_ = writer.Close()
		}
	})
	if refused < 2 {
		t.Fatalf("startsOnceFree returned after %d refused starts, while the writer still held the copy; want it to try until the copy starts", refused)
	}
}
