package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	bin := t.TempDir()
	name := "line-shell"
	if runtime.GOOS == "windows" {
		name = "pwsh" // the interactive shell the terminal looks for first (internal/usershell)
	}
	sh := standIn(t, bin, name)
	home := t.TempDir()
	tuneEnv(t, "SHELL="+sh, "HOME="+home, "USERPROFILE="+home, "PS1=$ ", lineShellEnv+"=1",
		"PATH="+bin+string(os.PathListSeparator)+systemPath())
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	return d, attached(t, d)
}

// shellInTheTerminal opens the terminal and waits for its shell's prompt, the keyboard in it.
func shellInTheTerminal(t *testing.T, r *running) {
	t.Helper()
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.s.WaitFor(t, "a shell running", func(string) bool { return r.shellsRunning() == 1 })
	r.s.WaitForText(t, "$ ")
}
