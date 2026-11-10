//go:build windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
)

// agentCommandCases on Windows: Windows PowerShell 5.1 (a PATH without pwsh) and pwsh where the
// runner has it, each calling the CLI two ways: an .exe by its quoted path, and a batch file (npm's
// .cmd shim, which runs the .exe through %*) by its bare name, found through PATHEXT.
func agentCommandCases(t *testing.T) []agentCommandCase {
	exe := agentCLI(t, "o'brien dir", "agent.exe")
	shimDir := filepath.Join(t.TempDir(), "o'brien shim")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "agentcli.cmd"), []byte("@\""+exe+"\" %*\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sys32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	shells := []struct {
		label string
		path  string
	}{{"winps", filepath.Join(sys32, "WindowsPowerShell", "v1.0")}}
	if pwsh, err := exec.LookPath("pwsh"); err == nil {
		shells = append(shells, struct {
			label string
			path  string
		}{"pwsh", filepath.Dir(pwsh)})
	}
	var cases []agentCommandCase
	for _, s := range shells {
		env := procenv.Env(nil).With("PATH="+shimDir+";"+s.path+";"+sys32, "PATHEXT=.COM;.EXE;.BAT;.CMD")
		sh := usershell.Commands(env)
		cases = append(cases,
			agentCommandCase{label: s.label + " exe", env: env, call: "& " + sh.Quote(exe)},
			agentCommandCase{label: s.label + " cmd shim", env: env, call: "agentcli"})
	}
	return cases
}
