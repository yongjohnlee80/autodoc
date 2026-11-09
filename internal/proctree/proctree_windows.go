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

// alive is whether pid exists and has not ended: its handle, opened to wait on, is not yet
// signalled (a zero-time wait). The exit code cannot tell: STILL_ACTIVE (259) is also the code of a
// process that ended with 259, which a handle held anywhere keeps, pid and all. A pid that cannot be
// opened (none, or another user's) is not alive, as signal 0's error makes it on Unix.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
