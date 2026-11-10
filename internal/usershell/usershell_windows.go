//go:build windows

package usershell

import (
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// commands is PowerShell: pwsh when it is on env's PATH, else Windows PowerShell on it, else Windows
// PowerShell where Windows keeps it (every Windows AutoDoc runs on has it), each found as env's PATH
// and PATHEXT find it.
func commands(env procenv.Env) Shell {
	if s, ok := onPath(env); ok {
		return s
	}
	return Shell{Program: filepath.Join(systemRoot(env), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), kind: winps}
}

// onPath is pwsh, else powershell, on env's PATH.
func onPath(env procenv.Env) (Shell, bool) {
	if p, err := procenv.LookPath(env, "pwsh"); err == nil {
		return Shell{Program: p, kind: powershell}, true
	}
	if p, err := procenv.LookPath(env, "powershell"); err == nil {
		return Shell{Program: p, kind: winps}, true
	}
	return Shell{}, false
}

// interactive is PowerShell on env's PATH without its banner, else %COMSPEC%, else cmd.exe.
func interactive(env procenv.Env) []string {
	if s, ok := onPath(env); ok {
		return []string{s.Program, "-NoLogo"}
	}
	return []string{comspec(env)}
}

// systemRoot is env's %SystemRoot%, else C:\Windows.
func systemRoot(env procenv.Env) string {
	if root := env.Get("SystemRoot"); root != "" {
		return root
	}
	return `C:\Windows`
}

// comspec is env's %COMSPEC%, else cmd.exe in its %SystemRoot%.
func comspec(env procenv.Env) string {
	if c := env.Get("ComSpec"); c != "" {
		return c
	}
	return filepath.Join(systemRoot(env), "System32", "cmd.exe")
}
