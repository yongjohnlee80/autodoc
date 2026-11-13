package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheBridgeLockIsItsSessions: a session's bridge lock is held by one bridge at a time; another
// session's bridge in the same folder takes its own; a lock its holder let go of (or died holding:
// the kernel drops it) is taken again.
func TestTheBridgeLockIsItsSessions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unlock, err := lockBridge(dir, "agy", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockBridge(dir, "agy", "s1"); !errors.Is(err, errBridgeRunning) {
		t.Errorf("a second bridge for s1: %v, want errBridgeRunning", err)
	}
	other, err := lockBridge(dir, "agy", "s2")
	if err != nil {
		t.Errorf("s2's bridge beside s1's: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := lockBridge(dir, "agy", "s1")
	if err != nil {
		t.Errorf("s1's lock after its holder let go: %v", err)
	} else {
		again()
	}
}

// TestTheBridgeLocksRefuseALink: the bridge's lock and the prompts' lock are files in the agent's
// folder, where another program may put a link; a link there is refused and its target never
// created or locked.
func TestTheBridgeLocksRefuseALink(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "target")
	lockPath, _ := bridgeFiles(dir, "agy", "s1")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Skipf("this system makes no link here: %v", err)
	}
	if _, err := lockBridge(dir, "agy", "s1"); err == nil || errors.Is(err, errBridgeRunning) {
		t.Errorf("the bridge's lock at a link: %v, want it refused", err)
	}
	prompts := filepath.Join(dir, "prompts-lock")
	if err := os.Symlink(target, prompts); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := lockFolder(ctx, prompts); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the prompts' lock at a link: %v, want it refused at once", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the link's target was made: %v", err)
	}
}

// TestThePromptsLockWaitsForItsHolder: one hook at a time for a session. A second waits while the
// first holds the lock, takes it once it is let go, and gives up when its own bound ends first.
func TestThePromptsLockWaitsForItsHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := lockFolder(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := lockFolder(short, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a hook while another holds it, past its bound: %v, want its deadline", err)
	}
	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unlock, err := lockFolder(ctx, path)
		if err == nil {
			unlock()
		}
		got <- err
	}()
	time.Sleep(50 * time.Millisecond)
	first()
	if err := <-got; err != nil {
		t.Fatalf("a hook after the holder let go: %v", err)
	}
}
