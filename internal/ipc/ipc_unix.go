//go:build linux || darwin

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
)

// Network is the network the daemon's address is on, for golib rpc's ClientNetwork and rpc.Probe:
// a unix socket's.
const Network = "unix"

// DefaultAddr is the daemon's address when none is configured: its socket in runtimeDir.
func DefaultAddr(runtimeDir string) string { return filepath.Join(runtimeDir, "autodoc.sock") }

// IsLocal is whether a, a connection's peer address, is ipc's: a process of this machine.
func IsLocal(a net.Addr) bool { return a != nil && a.Network() == Network }

// played is the uid a test plays (PlayUser), plus one; 0 while none is played.
var played atomic.Int64

// ourUID is the user both ends must run as: this process's, or the one a test plays.
func ourUID() int {
	if u := played.Load(); u != 0 {
		return int(u - 1)
	}
	return os.Getuid()
}

// PlayUser makes this process expect uid, instead of its own, at the other end of its sockets and
// as their folder's owner, until restore: every real peer is then another user's. It is a test's
// seam, in any package, to show the daemon and its clients refusing another user's process; nothing
// else calls it. A daemon or client running meanwhile reads the new uid at its next check.
// NOTE: process-wide, as the uid it stands for: never from a parallel test (t.Parallel), whose
// neighbours' sockets would see another user too.
func PlayUser(uid int) (restore func()) {
	played.Store(int64(uid) + 1)
	return func() { played.Store(0) }
}

// CheckAddr refuses addr as the daemon's socket unless its folder passes CheckDir.
func CheckAddr(addr string) error { return CheckDir(filepath.Dir(addr)) }

// CheckDir refuses dir as a socket's folder unless it is a folder owned by this user (or by root),
// that neither its group nor others can write. A sticky world-writable folder such as /tmp is
// refused too: the sticky bit stops another user from removing the socket, not from binding at its
// path before the daemon does.
func CheckDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return &UnsafeDirError{Dir: dir, Why: "is not a folder"}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != ourUID() && st.Uid != 0 {
		return &UnsafeDirError{Dir: dir, Why: fmt.Sprintf("belongs to another user (uid %d)", st.Uid)}
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return &UnsafeDirError{Dir: dir, Why: fmt.Sprintf("can be written by its group or by others (mode %#o)", fi.Mode().Perm())}
	}
	return nil
}

// peerUser is the user the peer of c ran as when it connected, whether it is this process's, and ok
// when c is a unix socket's (a connection of another kind has no peer user).
func peerUser(c net.Conn) (user string, ours, ok bool, err error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return "", false, false, nil
	}
	uid, err := unixPeerUID(uc)
	if err != nil {
		return "", false, true, err
	}
	return "uid " + strconv.Itoa(uid), uid == ourUID(), true, nil
}

// peerPID is the process at the other end of c when it connected, ok when c is a unix socket's.
func peerPID(c net.Conn) (pid int, ok bool, err error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false, nil
	}
	pid, err = unixPeerPID(uc)
	return pid, true, err
}

// dial connects to the socket at addr.
func dial(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, Network, addr)
}
