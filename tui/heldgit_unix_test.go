//go:build unix

package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestAHeldGitStartsOnceItsCopyIsFree: while a writer still holds heldGit's copied git (a parallel
// test's fork that has not reached its exec yet), an exec of it fails with "text file busy";
// startsOnceFree waits until the writer is gone and the copy starts.
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
	t.Cleanup(func() { _ = writer.Close() }) // a second close, after the timer's, only errors
	busy := exec.Command(git, "version")
	busy.Env = append(os.Environ(), heldGitEnv+"="+real)
	if err := busy.Run(); !errors.Is(err, syscall.ETXTBSY) {
		t.Skipf("this system starts a file a writer holds (%v): there is nothing to wait out", err)
	}
	freed := time.Now().Add(200 * time.Millisecond)
	time.AfterFunc(200*time.Millisecond, func() { _ = writer.Close() })
	startsOnceFree(t, git, real)
	if time.Now().Before(freed) {
		t.Fatal("the copy started before its writer let go")
	}
}
