//go:build !unix

package tui

import "syscall"

// detached is the platform's default: nothing to set.
func detached() *syscall.SysProcAttr { return nil }
