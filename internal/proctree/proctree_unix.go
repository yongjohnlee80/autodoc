//go:build linux || darwin

package proctree

import (
	"os"
	"os/exec"
	"syscall"
)

// detach starts cmd as the leader of a session of its own (setsid).
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// alive is whether pid exists: signal 0 checks without sending.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// replace is exec.
func replace(path string, args, env []string) error { return syscall.Exec(path, args, env) }

// releaseConsoleIfAlone has no console to give up.
func releaseConsoleIfAlone() bool { return false }
