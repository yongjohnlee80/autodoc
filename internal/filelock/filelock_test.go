package filelock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The contract every OS's lock keeps: TryLock takes a free lock (creating its file), refuses a held
// one with ErrHeld at once, and frees it on Unlock, which is safe to call twice.
func TestTryLockTakesRefusesAndFrees(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock on a free file: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("TryLock did not create its file: %v", err)
	}
	start := time.Now()
	if _, err := TryLock(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("a second TryLock while held: %v, want ErrHeld", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the refused TryLock waited %v: it must not wait", d)
	}
	if err := unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Errorf("a second Unlock: %v, want nothing", err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	_ = again()
}

// Lock waits while the lock is held, and takes it once its holder unlocks.
func TestLockWaitsForTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	var second Unlock
	go func() {
		u, err := Lock(path)
		second = u
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("Lock returned (%v) while the lock was held: it must wait", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("Lock after the holder unlocked: %v", err)
		}
		_ = second()
	case <-time.After(5 * time.Second):
		t.Fatal("Lock still waits after the holder unlocked")
	}
}

// A missing directory is an error, not ErrHeld: a lock nobody could take is not one somebody holds.
func TestTryLockOnAPathThatCannotBeOpenedIsNotHeld(t *testing.T) {
	_, err := TryLock(filepath.Join(t.TempDir(), "missing", "lock"))
	if err == nil || errors.Is(err, ErrHeld) {
		t.Fatalf("TryLock in a missing directory: %v, want an error that is not ErrHeld", err)
	}
}

// Another process's lock is refused too, and freed when that process ends: the lock is the OS's,
// not this process's bookkeeping. The helper is this test binary, run again as the holder.
func TestTryLockRefusesAnotherProcessAndFreesWhenItEnds(t *testing.T) {
	if os.Getenv("FILELOCK_HOLD") != "" {
		holdForTest()
		return
	}
	path := filepath.Join(t.TempDir(), "lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTryLockRefusesAnotherProcessAndFreesWhenItEnds$")
	cmd.Env = append(os.Environ(), "FILELOCK_HOLD="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "held\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("the holder said %q, %v; want held", line, err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrHeld) {
		t.Errorf("TryLock while another process holds the lock: %v, want ErrHeld", err)
	}
	_ = stdin.Close() // the holder exits without unlocking: its end frees the lock
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the holder: %v", err)
	}
	// a lock its holder's end releases is freed by the OS, which on Windows may take a moment:
	// retry briefly rather than assume it is at once (an Unlock, by contrast, is)
	var unlock Unlock
	deadline := time.Now().Add(5 * time.Second)
	for {
		if unlock, err = TryLock(path); err == nil || !errors.Is(err, ErrHeld) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TryLock after the holder ended: %v", err)
	}
	_ = unlock()
}

// holdForTest takes FILELOCK_HOLD's lock, says so, and holds it until its stdin closes, then exits
// without unlocking.
func holdForTest() {
	if _, err := TryLock(os.Getenv("FILELOCK_HOLD")); err != nil {
		os.Stdout.WriteString("refused: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("held\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

// NoFollow refuses a link at the lock's path, never creating or locking its target; a plain file
// there is locked as without it.
func TestNoFollowRefusesALink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this system makes no link here: %v", err)
	}
	if _, err := TryLock(link, NoFollow()); err == nil || errors.Is(err, ErrHeld) {
		t.Errorf("TryLock(NoFollow) on a link: %v, want it refused", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the link's target was made: %v", err)
	}
	unlock, err := TryLock(filepath.Join(dir, "plain"), NoFollow())
	if err != nil {
		t.Fatalf("TryLock(NoFollow) on a plain path: %v", err)
	}
	if _, err := TryLock(filepath.Join(dir, "plain"), NoFollow()); !errors.Is(err, ErrHeld) {
		t.Errorf("a second TryLock(NoFollow) while held: %v, want ErrHeld", err)
	}
	_ = unlock()
}
