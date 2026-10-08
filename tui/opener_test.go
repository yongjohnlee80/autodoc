package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An opener that fails at once is an error; one still running after the grace (xdg-open running
// the app itself, as on Hyprland) is the app: success, and it is left running, not killed.
func TestTheOpenerLeavesTheAppRunning(t *testing.T) {
	if err := startOpener(context.Background(), "false"); err == nil {
		t.Error("an opener that fails at once gave no error")
	}
	mark := filepath.Join(t.TempDir(), "survived")
	start := time.Now()
	// the "app": runs past the grace, then marks that it was not killed
	err := startOpener(context.Background(), "sh", "-c", "sleep 3; touch "+mark)
	if err != nil {
		t.Fatalf("a running app reported %v", err)
	}
	if d := time.Since(start); d < openerGrace || d > openerGrace+time.Second {
		t.Errorf("returned after %v, want the grace (%v)", d, openerGrace)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mark); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the app was killed: it never finished")
}
