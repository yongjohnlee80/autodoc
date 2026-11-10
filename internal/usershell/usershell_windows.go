//go:build windows

package usershell

import (
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// commands is PowerShell: pwsh when it is on env's PATH, else Windows PowerShell, else %COMSPEC%
// (cmd.exe), each found as env's PATH and PATHEXT find it.
func commands(env procenv.Env) Shell {
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := procenv.LookPath(env, name); err == nil {
			return Shell{Program: p, kind: powershell}
		}
	}
	return Shell{Program: comspec(env), kind: cmd}
}

// interactive is commands' shell, started for a person: PowerShell without its banner, or cmd.exe.
func interactive(env procenv.Env) []string {
	s := commands(env)
	if s.kind == powershell {
		return []string{s.Program, "-NoLogo"}
	}
	return []string{s.Program}
}

// comspec is env's %COMSPEC%, else cmd.exe in its %SystemRoot%.
func comspec(env procenv.Env) string {
	if c := env.Get("ComSpec"); c != "" {
		return c
	}
	root := env.Get("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "cmd.exe")
}
