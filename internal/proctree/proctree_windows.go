//go:build windows

package proctree

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach starts cmd in a process group of its own (a console's Ctrl+C never reaches it) and with no
// console at all, none inherited and none made (DETACHED_PROCESS, CREATE_NO_WINDOW).
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW
}

// stillActive is GetExitCodeProcess's code for a process that has not ended (STILL_ACTIVE).
const stillActive = 259

// alive is whether pid exists and has not ended: its handle opened for a query, its exit code read.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
