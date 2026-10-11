package tui

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestTheHostsWaitEndsWithItsContext: the Host's wait for a stopped daemon gives up with its
// context, the process running on.
func TestTheHostsWaitEndsWithItsContext(t *testing.T) {
	long := exec.Command("sleep", "30")
	if err := long.Start(); err != nil {
		t.Skip("no sleep(1):", err)
	}
	defer func() { _ = long.Process.Kill(); _ = long.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if awaitExit(ctx, int64(long.Process.Pid)) {
		t.Fatal("the Host's wait saw a running process gone")
	}
}
