//go:build linux

package peercred

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the user the peer of c ran as when it connected (SO_PEERCRED).
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return int(cred.Uid), nil
}
