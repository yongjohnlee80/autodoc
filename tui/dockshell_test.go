package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The dock's shell cells run on every OS. Their shell is this test binary as a minimal line shell
// (lineShellMain): it prints "$ " and reads lines until exit or the end of its input, which is all
// these cells ask of a shell: one running in the terminal's tab, at its prompt. On Unix it is the
// user's shell through SHELL; on Windows the terminal starts pwsh first on PATH, so it is pwsh.exe
// there, put first on the program's PATH.

func init() {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "line-shell", "pwsh":
		if os.Getenv(lineShellEnv) == "1" {
			os.Exit(lineShellMain())
		}
	}
}

// lineShellEnv marks the program's environment as the line shell's, so a real pwsh never reads as it.
const lineShellEnv = "AUTODOC_TEST_LINE_SHELL"

// lineShellMain prints the prompt, then a prompt after each line it reads, until exit or its input ends.
func lineShellMain() int {
	fmt.Print("$ ")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		if strings.TrimSpace(strings.TrimRight(in.Text(), "\r")) == "exit" {
			return 0
		}
		fmt.Print("$ ")
	}
	return 0
}

// runLineShellTUI is runShellTUI on every OS, its shell the line shell.
func runLineShellTUI(t *testing.T, prefs map[string]string) (*daemon, *running) {
	t.Helper()
	bin := shellTempDir(t, "autodoc-line-shell-")
	name := "line-shell"
	if runtime.GOOS == "windows" {
		name = "pwsh" // the interactive shell the terminal looks for first (internal/usershell)
	}
	sh := standIn(t, bin, name)
	home := shellTempDir(t, "autodoc-line-home-") // the shell's working folder on Windows
	tuneEnv(t, "SHELL="+sh, "HOME="+home, "USERPROFILE="+home, "PS1=$ ", lineShellEnv+"=1",
		"PATH="+bin+string(os.PathListSeparator)+systemPath())
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	return d, attached(t, d)
}

// shellTempDir is a folder of the line shell's (its program, its home), not t.TempDir's: on Windows
// a running program's file, or the working folder of a process, can't be removed, and the shell may
// still be exiting as the test ends. Its removal is registered before the program starts, so it runs
// after the program's own cleanup has stopped its shells (cleanups run newest first), and is retried
// while Windows still holds the exiting shell.
func shellTempDir(t *testing.T, pattern string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for deadline := time.Now().Add(10 * time.Second); os.RemoveAll(dir) != nil && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		}
	})
	return dir
}

// shellInTheTerminal opens the terminal and waits for its shell's prompt, the keyboard in it.
func shellInTheTerminal(t *testing.T, r *running) {
	t.Helper()
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.s.WaitFor(t, "a shell running", func(string) bool { return r.shellsRunning() == 1 })
	r.s.WaitForText(t, "$ ")
}
