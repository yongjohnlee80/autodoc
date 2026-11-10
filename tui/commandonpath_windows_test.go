//go:build windows

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/usershell"
)

// On Windows a command's program is found as Windows finds one: a bare name or an absolute path
// without its extension through PATHEXT, either separator, and a refusal names PowerShell, the shell
// that runs it, never /bin/sh.
func TestACommandIsJudgedAsWindowsFindsAProgram(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + dir, "PATHEXT=.COM;.EXE", "SystemRoot=" + os.Getenv("SystemRoot")}
	shell := usershell.Commands(env)
	for _, found := range []string{"agent --x", "AGENT", filepath.Join(dir, "agent"), filepath.Join(dir, "agent.exe"), filepath.ToSlash(filepath.Join(dir, "agent"))} {
		if err := commandOnPath(shell, found, env); err != nil {
			t.Errorf("%q: %v; want it found", found, err)
		}
	}
	err := commandOnPath(shell, "claude-work", env)
	if err == nil || !strings.Contains(err.Error(), "claude-work") || !strings.Contains(err.Error(), shell.Describe()) || strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("a bare name not on PATH: %v; want it refused, naming %s", err, shell.Describe())
	}
	missing := filepath.Join(dir, "no-such.exe")
	if err := commandOnPath(shell, missing, env); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("an absolute path that isn't there: %v; want it refused, naming it", err)
	}
	if err := commandOnPath(shell, `~\bin\no-such`, env); err != nil {
		t.Errorf("a leading ~: %v; want it left to PowerShell", err)
	}
}
