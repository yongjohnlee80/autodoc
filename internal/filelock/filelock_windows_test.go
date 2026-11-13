//go:build windows

package filelock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// On Windows NoFollow refuses any reparse point, a junction as well as a symbolic link: the
// junction is opened as itself, and its target folder is never reached.
func TestNoFollowRefusesAJunction(t *testing.T) {
	dir := t.TempDir()
	target, junction := filepath.Join(dir, "target"), filepath.Join(dir, "junction")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	if _, err := TryLock(junction, NoFollow()); err == nil || errors.Is(err, ErrHeld) {
		t.Errorf("TryLock(NoFollow) on a junction: %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the junction's target folder was written in: %v", entries)
	}
}
