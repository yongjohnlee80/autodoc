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

// setStderrHandle has nothing more to do: nothing on Unix gives up its descriptors this way.
func setStderrHandle(*os.File) {}

// osTree has nothing of its own on Unix: the group's id is the root's pid.
type osTree struct{}

// startTree starts cmd as the leader of a process group of its own.
func startTree(cmd *exec.Cmd) (*Tree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{cmd: cmd}, nil
}

// stop sends SIGTERM, or SIGKILL when kill, to the group.
func (t *Tree) stop(kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-t.cmd.Process.Pid, sig)
}

// close has nothing to let go.
func (t *Tree) close() {}
