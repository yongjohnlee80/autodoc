//go:build unix

package plugin

import (
	"net"
	"os"
	"syscall"
)

// stdio is the plugin's end of the link, over its stdin and stdout (stdioOf). After it, os.Stdout
// is os.Stderr: stdout is the protocol's, and a stray print would break it.
func stdio() (net.Conn, error) {
	conn, err := stdioOf(os.Stdin, 0, 1)
	if err != nil {
		return nil, err
	}
	keepStdout()
	return conn, nil
}

// stdioOf is a link over duplicates of the descriptors in and out, close-on-exec, made
// non-blocking so the runtime's poller takes them (the link's writes carry deadlines), and owned by
// the link alone: no other *os.File holds them, so nothing else closes them. stdin, a terminal, is
// refused: a plugin runs under AutoDoc.
func stdioOf(stdin *os.File, in, out int) (net.Conn, error) {
	if err := refuseTerminal(stdin); err != nil {
		return nil, err
	}
	r, err := dupPollable(in, "plugin-in")
	if err != nil {
		return nil, err
	}
	w, err := dupPollable(out, "plugin-out")
	if err != nil {
		r.Close()
		return nil, err
	}
	return FileConn(r, w), nil
}

// dupPollable duplicates fd, close-on-exec under the fork lock (a process the plugin starts does
// not inherit the protocol's pipes), non-blocking, as a file of its own.
func dupPollable(fd int, name string) (*os.File, error) {
	syscall.ForkLock.RLock()
	nfd, err := syscall.Dup(fd)
	if err == nil {
		syscall.CloseOnExec(nfd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.SetNonblock(nfd, true); err != nil {
		syscall.Close(nfd)
		return nil, err
	}
	return os.NewFile(uintptr(nfd), name), nil
}
