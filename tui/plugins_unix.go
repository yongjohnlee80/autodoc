//go:build unix

package tui

import (
	"os/exec"
	"syscall"
)

// pluginProcAttr puts a plugin in a process group of its own, so its close reaches what it started.
func pluginProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends SIGTERM, or SIGKILL when kill, to the plugin's process group.
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
