package tui

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestWaitGoneWaitsForTheProcess: a process that exits is seen gone once it has; one that runs on
// is not, and the wait ends at its bound.
func TestWaitGoneWaitsForTheProcess(t *testing.T) {
	short := exec.Command("sleep", "0.2")
	if err := short.Start(); err != nil {
		t.Skip("no sleep(1):", err)
	}
	go func() { _ = short.Wait() }() // reaped, as the TUI's spawn reaps its daemon
	start := time.Now()
	if !waitGone(context.Background(), int64(short.Process.Pid), 5*time.Second) {
		t.Fatal("the exited process was not seen gone")
	}
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Fatalf("seen gone after %s, before it exited", took)
	}
	long := exec.Command("sleep", "30")
	if err := long.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = long.Process.Kill(); _ = long.Wait() }()
	if waitGone(context.Background(), int64(long.Process.Pid), 200*time.Millisecond) {
		t.Fatal("a running process was seen gone")
	}
	// the Host's wait gives up with its context, the process running on
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if awaitExit(ctx, int64(long.Process.Pid)) {
		t.Fatal("the Host's wait saw a running process gone")
	}
}
