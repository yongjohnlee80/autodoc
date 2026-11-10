//go:build linux || darwin

package usershell

import "github.com/yongjohnlee80/autodoc/internal/procenv"

// commands is /bin/sh: a command line is POSIX sh's on every Unix.
func commands(procenv.Env) Shell { return Shell{Program: "/bin/sh", kind: posix} }

// interactive is the user's $SHELL, else /bin/sh.
func interactive(env procenv.Env) []string {
	if sh := env.Get("SHELL"); sh != "" {
		return []string{sh}
	}
	return []string{"/bin/sh"}
}
