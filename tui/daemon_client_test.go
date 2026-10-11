package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/client"
)

// shortDir is a directory whose socket path fits a unix socket's limit on every platform.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ado")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestTheMovedFunctionsKeepTheirNames: tui's exported API from before internal/client keeps its
// names: a handoff written through tui is client's, and a process that has exited is seen gone.
func TestTheMovedFunctionsKeepTheirNames(t *testing.T) {
	var _ func(string, string) (string, error) = SpawnServe
	dir := shortDir(t)
	sock := filepath.Join(dir, "s.sock")
	if err := WriteHandoff(dir, sock, int64(os.Getpid())); err != nil || !client.HasHandoff(dir, sock) {
		t.Fatalf("WriteHandoff: %v, found %v", err, client.HasHandoff(dir, sock))
	}
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Skip("no true(1):", err)
	}
	if !WaitGone(context.Background(), int64(gone.Process.Pid), time.Second) {
		t.Fatal("an exited process was not seen gone")
	}
}
