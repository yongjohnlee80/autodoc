//go:build windows

package proctree

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// detachedFact is whether the helper has a console: none, when it was started detached.
func detachedFact() string {
	if h, _, _ := getConsoleWindow.Call(); h != 0 {
		return "console"
	}
	return "no console"
}

// detached is whether the helper has no console, none inherited and none made.
func detached(t *testing.T, fact string) bool {
	t.Helper()
	return fact == "no console"
}

// A process that ended with exit code 259, STILL_ACTIVE's value, while a handle to it is held (so its
// pid is kept) is not Alive; while it runs, it is. The helper is this test binary run again.
func TestAProcessThatEndedWith259IsNotAlive(t *testing.T) {
	if os.Getenv("PROCTREE_EXIT259") != "" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(259)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAProcessThatEndedWith259IsNotAlive$")
	cmd.Env = append(os.Environ(), "PROCTREE_EXIT259=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("OpenProcess(%d): %v", pid, err)
	}
	defer windows.CloseHandle(h) // held past the end: the pid stays the ended process's
	if !Alive(pid) {
		t.Errorf("Alive(%d) of the running program is false", pid)
	}
	_ = in.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the helper did not end when its stdin closed")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != 259 {
		t.Fatalf("the helper ended with %d (%v); the cell needs 259, STILL_ACTIVE's value", code, err)
	}
	if Alive(pid) {
		t.Errorf("Alive(%d) of a program that ended with 259 is true", pid)
	}
}
