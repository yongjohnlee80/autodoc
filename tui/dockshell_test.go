package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
)

// The dock's shell cells, and the terminal keymap's, run on every OS. Their shell is this test binary
// as a minimal line shell (lineShellMain): it prints "$ " and reads lines until exit or the end of its
// input, which is all these cells ask of a shell: one running in the terminal's tab, at its prompt,
// and running what is typed to it. On Unix it is the line shell set as SHELL; on Windows the terminal
// starts pwsh first on PATH, so it is pwsh.exe there, put first on the program's PATH.

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
// A line "shout <words>" prints the words in capitals first: what the terminal echoes as it is typed
// never reads so, so seeing them says the shell ran the line. Two more, for the environment cell
// (hostenv_test.go): "ishome PATH MARK" prints home-MARK when its home folder (userdirs.Home, as the
// program reads it) is PATH, and "run PROGRAM MARK" runs PROGRAM, found on its PATH, with MARK, its
// output the shell's.
func lineShellMain() int {
	fmt.Print("$ ")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(strings.TrimRight(in.Text(), "\r"))
		if line == "exit" {
			return 0
		}
		if words, ok := strings.CutPrefix(line, "shout "); ok {
			fmt.Println(strings.ToUpper(words))
		}
		switch f := strings.Fields(line); {
		case len(f) == 3 && f[0] == "ishome":
			if home, err := userdirs.Home(nil); err == nil && home == f[1] {
				fmt.Printf("home-%s\n", f[2])
			}
		case len(f) == 3 && f[0] == "run":
			if path, err := procenv.LookPath(nil, f[1]); err == nil {
				cmd := exec.Command(path, f[2])
				cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
				_ = cmd.Run()
			}
		}
		fmt.Print("$ ")
	}
	return 0
}

// runLineShellTUI is runShellTUI on every OS, its shell the line shell.
func runLineShellTUI(t *testing.T, prefs map[string]string) (*daemon, *running) {
	t.Helper()
	useLineShell(t)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	return d, attached(t, d)
}

// useLineShell makes the line shell the shell of the programs the test starts next, its home a folder
// of its own (HOME, and USERPROFILE: Windows' home).
func useLineShell(t *testing.T) {
	t.Helper()
	bin := shellTempDir(t, "autodoc-line-shell-")
	sh := lineShellIn(t, bin)
	home := shellTempDir(t, "autodoc-line-home-") // the shell's working folder on Windows
	tuneEnv(t, "SHELL="+sh, "HOME="+home, "USERPROFILE="+home, "PS1=$ ", lineShellEnv+"=1",
		"PATH="+bin+string(os.PathListSeparator)+systemPath())
}

// lineShellIn puts the line shell in bin under the name the terminal starts it by, and answers its
// path: SHELL on Unix; on Windows the terminal starts pwsh first on PATH (internal/usershell), so it
// is pwsh.exe there, bin put first on the program's PATH.
func lineShellIn(t *testing.T, bin string) string {
	t.Helper()
	name := "line-shell"
	if runtime.GOOS == "windows" {
		name = "pwsh" // the interactive shell the terminal looks for first (internal/usershell)
	}
	return standIn(t, bin, name)
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
		err := os.RemoveAll(dir)
		for deadline := time.Now().Add(10 * time.Second); err != nil && time.Now().Before(deadline); err = os.RemoveAll(dir) {
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil { // left in the temp folder: a shell still held it, which the log should say
			t.Logf("the line shell's folder was left: %v", err)
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
