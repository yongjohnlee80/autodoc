//go:build unix

package tui

import "syscall"

// detached starts a process in a session of its own: an app the desktop opens outlives AutoDoc,
// and a signal to AutoDoc's process group does not reach it.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
