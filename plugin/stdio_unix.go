//go:build unix

package plugin

import (
	"errors"
	"net"
	"os"
	"syscall"
)

// keptStdout is the process's stdout as it started. stdio points os.Stdout at os.Stderr; without
// this reference the original *os.File would be garbage, and its finalizer would close fd 1 at the
// next GC — the pipe the host reads frames from, which ended every plugin some seconds in.
var keptStdout *os.File

// stdio is the plugin's end of the link: duplicates of its stdin and stdout, close-on-exec, made
// non-blocking so the runtime's poller takes them (the link's writes carry deadlines), and owned by
// the link alone — no other *os.File holds these descriptors, so nothing else closes them. After
// it, os.Stdout is os.Stderr: stdout is the protocol's, and a stray print would break it.
func stdio() (net.Conn, error) {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return nil, errors.New("plugin: stdin is a terminal; a plugin runs under AutoDoc, from its Plugins menu")
	}
	in, err := dupCloexec(0)
	if err != nil {
		return nil, err
	}
	out, err := dupCloexec(1)
	if err != nil {
		syscall.Close(in)
		return nil, err
	}
	for _, fd := range []int{in, out} {
		if err := syscall.SetNonblock(fd, true); err != nil {
			syscall.Close(in)
			syscall.Close(out)
			return nil, err
		}
	}
	keptStdout = os.Stdout
	os.Stdout = os.Stderr
	return FileConn(os.NewFile(uintptr(in), "plugin-in"), os.NewFile(uintptr(out), "plugin-out")), nil
}

// dupCloexec duplicates fd, close-on-exec, under the fork lock: a process the plugin starts does
// not inherit the protocol's pipes.
func dupCloexec(fd int) (int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	nfd, err := syscall.Dup(fd)
	if err != nil {
		return -1, err
	}
	syscall.CloseOnExec(nfd)
	return nfd, nil
}
