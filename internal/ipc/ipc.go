// Package ipc is the connection between AutoDoc's processes of one user: the daemon listens at its
// address (Listen) and its clients dial it (Dial). It is the daemon's only access control: no login,
// no token. Whoever connects can call every verb, file.write anywhere on the user's disk among them.
// So both ends check who is at the other end:
//
//   - the daemon drops a connection whose peer runs as another user (Listen's listener, Guard), and
//     a client refuses a daemon that runs as another user (Dial), from the OS's own record of the
//     peer, not from a file mode that a umask, a chmod race or a wider folder can open;
//   - the daemon binds only where no other user can replace its address, or take it first
//     (CheckAddr).
//
// What the OSes share is here: the guard's refusal, the checks' wording, Listen's order. Each OS's
// primitive is in its own files: on Linux and macOS a unix socket in a folder only its user can
// write, its peer read from the kernel (SO_PEERCRED on Linux, LOCAL_PEERCRED on macOS):
// ipc_unix.go, listen_unix.go, peer_linux.go and peer_darwin.go. unsupported.go fails the build on
// any other OS, naming the seam.
//
// The default address ($XDG_RUNTIME_DIR, or the 0700 state directory) passes every check; they bite
// on a server.socket configured into a shared folder such as /tmp.
//
// Concurrency: every function is safe for concurrent use; a listener is as safe as the one it
// wraps.

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// ErrOtherUser is a peer that runs as another user than this process.
var ErrOtherUser = errors.New("ipc: the other end of the connection is another user's process")

// ErrInUse is an address a live listener holds: another daemon's, or something else's.
var ErrInUse = errors.New("ipc: the address is in use")

// Check refuses a connection whose peer runs as another user than this process. A connection that
// is not between two processes of this machine (not ipc's) is not checked: its peer has no user.
func Check(c net.Conn) error {
	user, ours, ok, err := peerUser(c)
	if !ok {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ipc: reading the peer's user: %w", err)
	}
	if !ours {
		return fmt.Errorf("%w (%s)", ErrOtherUser, user)
	}
	return nil
}

// PeerPID is the process at the other end of a connection, from the OS's record of it when it
// connected: what binds an address to the process that was meant to hold it (a waker checks it
// before every wake). Unlike Check, it never passes for want of a record: a connection that is not
// ipc's answers an error.
func PeerPID(c net.Conn) (int, error) {
	pid, ok, err := peerPID(c)
	if !ok {
		return 0, errors.New("ipc: not an interprocess connection: no peer process")
	}
	if err != nil {
		return 0, fmt.Errorf("ipc: reading the peer's process: %w", err)
	}
	return pid, nil
}

// Dial connects to the daemon's address, refusing (and closing) a connection whose listener runs as
// another user.
func Dial(ctx context.Context, addr string) (net.Conn, error) {
	c, err := dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	if err := Check(c); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return c, nil
}

// RPCDialer is Dial with golib rpc's WithConnDialer signature, for a client given Network: the
// network is ipc's own, whatever it is passed.
func RPCDialer(ctx context.Context, _, addr string) (net.Conn, error) { return Dial(ctx, addr) }

// Guard is ln that drops, without a byte, each connection Check refuses; refused, when not nil, is
// told why. Accept returns only the connections of this user's processes, and ln's own errors. It is
// for a listener bound elsewhere (the editor socket's); the daemon's is Listen's, guarded already.
func Guard(ln net.Listener, refused func(error)) net.Listener {
	return &guarded{Listener: ln, refused: refused}
}

type guarded struct {
	net.Listener
	refused func(error)
}

func (l *guarded) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := Check(c); err != nil {
			_ = c.Close()
			if l.refused != nil {
				l.refused(err)
			}
			continue
		}
		return c, nil
	}
}

// Listener is the daemon's listener at its address (Listen): Accept returns only the connections of
// this user's processes, and Remove takes the address away only while it is still this one's.
type Listener struct {
	net.Listener
	addr string
	id   ownership
}

// Listen binds the daemon's address: only where no other user can replace it or take it first
// (CheckAddr), born private, taking over a crashed daemon's and never a live one's (that is an
// ErrInUse), and dropping another user's connections (refused, when not nil, is told why). Closing
// it leaves the address: Remove takes it away.
func Listen(addr string, refused func(error)) (*Listener, error) {
	if err := CheckAddr(addr); err != nil {
		return nil, err
	}
	ln, err := bind(addr)
	if err != nil {
		return nil, err
	}
	id, err := own(ln, addr)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &Listener{Listener: Guard(ln, refused), addr: addr, id: id}, nil
}

// Remove takes the listener's address away while it is still the one this process bound, so a
// daemon shutting down never removes a successor's. Call it once, after Close.
func (l *Listener) Remove() { l.id.remove(l.addr) }

// UnsafeDirError is a folder for the daemon's address that another user could write in.
type UnsafeDirError struct {
	Dir, Why string
}

func (e *UnsafeDirError) Error() string {
	return fmt.Sprintf("the socket's folder %s %s: another user could replace the socket there, or bind its own first; "+
		"set server.socket in a folder only you can write (the default, $XDG_RUNTIME_DIR, is one)", e.Dir, e.Why)
}
