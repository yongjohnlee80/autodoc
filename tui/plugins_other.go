//go:build !unix

package tui

import (
	"os/exec"
	"syscall"
)

// pluginProcAttr: no process groups here; the plugin alone is stopped.
func pluginProcAttr() *syscall.SysProcAttr { return nil }

func signalGroup(cmd *exec.Cmd, _ bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
