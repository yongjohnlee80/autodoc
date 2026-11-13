//go:build windows

package proctree

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
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

// A program that gave up its console still has somewhere to fail: what it writes to standard error
// and a runtime crash's report both reach the file SetStderr gave it. What the desktop shows is
// another matter, for a check on one.
func TestWhatFollowsGivingUpTheConsoleReachesTheLog(t *testing.T) {
	report := filepath.Join(t.TempDir(), "log")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAProgramGivesUpItsConsoleOnlyWhenAlone$")
	cmd.Env = procenv.Env(os.Environ()).With("PROCTREE_CONSOLE=crash", "PROCTREE_REPORT="+report)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	if err := cmd.Run(); err == nil {
		t.Error("the helper's panic did not end it with a failure")
	}
	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no log: %v", err)
	}
	for _, want := range []string{"said on stderr", "a crash's report", "goroutine"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the log lacks %q:\n%s", want, got)
		}
	}
}

// reportConsole gives up the console if alone on it and writes what it saw to the report file:
// whether it had a console, whether ReleaseConsoleIfAlone said it gave it up, and whether it has one
// after. A file, since a program on its own console has no output the test can read.
func reportConsole() {
	had := consoleWindow()
	dropped := ReleaseConsoleIfAlone()
	_ = os.WriteFile(os.Getenv("PROCTREE_REPORT"), []byte(fmt.Sprintf("had=%v dropped=%v after=%v", had, dropped, consoleWindow())), 0o600)
}

func consoleWindow() bool {
	h, _, _ := getConsoleWindow.Call()
	return h != 0
}

// With --gui's rule: a program alone on its console gives it up; one sharing it, as with the shell
// that started it, keeps it; one with no console has nothing to give up. Each helper is this test
// binary on a console made for it. What a person sees (no window left, no flash from the shortcut)
// is the desktop's to show, not this cell's.
func TestAProgramGivesUpItsConsoleOnlyWhenAlone(t *testing.T) {
	const run = "-test.run=^TestAProgramGivesUpItsConsoleOnlyWhenAlone$"
	switch os.Getenv("PROCTREE_CONSOLE") {
	case "report":
		reportConsole()
		os.Exit(0)
	case "crash": // alone on the console: what it says after giving that up must reach the log
		if ReleaseConsoleIfAlone() {
			f, err := os.Create(os.Getenv("PROCTREE_REPORT"))
			if err != nil {
				os.Exit(3)
			}
			SetStderr(f)
			fmt.Fprintln(os.Stderr, "said on stderr")
			panic("a crash's report")
		}
		os.Exit(4)
	case "share": // on the new console; the report comes from a child that inherits it
		child := exec.Command(os.Args[0], run)
		child.Env = procenv.Env(os.Environ()).With("PROCTREE_CONSOLE=report")
		_ = child.Run()
		os.Exit(0)
	}
	for _, c := range []struct {
		name, role string
		flags      uint32
		want       string
	}{
		{"alone", "report", windows.CREATE_NEW_CONSOLE, "had=true dropped=true after=false"},
		{"shared", "share", windows.CREATE_NEW_CONSOLE, "had=true dropped=false after=true"},
		{"none", "report", windows.DETACHED_PROCESS, "had=false dropped=false after=false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "report")
			cmd := exec.Command(os.Args[0], run)
			cmd.Env = procenv.Env(os.Environ()).With("PROCTREE_CONSOLE="+c.role, "PROCTREE_REPORT="+report)
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: c.flags}
			if err := cmd.Run(); err != nil {
				t.Fatalf("the helper: %v", err)
			}
			got, err := os.ReadFile(report)
			if err != nil {
				t.Fatalf("no report from the helper: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("%s: %s, want %s", c.name, got, c.want)
			}
		})
	}
}
