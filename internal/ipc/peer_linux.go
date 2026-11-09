//go:build linux

package ipc

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred is the kernel's record of the peer of c when it connected (SO_PEERCRED).
func peerCred(c *net.UnixConn) (*unix.Ucred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return nil, err
	}
	return cred, cerr
}

// unixPeerUID is the user the peer of c ran as when it connected.
func unixPeerUID(c *net.UnixConn) (int, error) {
	cred, err := peerCred(c)
	if err != nil {
		return 0, err
	}
	return int(cred.Uid), nil
}

// unixPeerPID is the process at the other end of c when it connected.
func unixPeerPID(c *net.UnixConn) (int, error) {
	cred, err := peerCred(c)
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}
