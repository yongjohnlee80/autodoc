//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Network is the network the daemon's address is on, for golib rpc's ClientNetwork and rpc.Probe:
// a named pipe's (go-winio names it so).
const Network = "pipe"

// pipePrefix is where every named pipe's name starts.
const pipePrefix = `\\.\pipe\`

// DefaultAddr is the daemon's address when none is configured: a named pipe named for this user's
// SID, so two users' daemons never share one. runtimeDir is Unix's folder for it; a pipe has none.
func DefaultAddr(runtimeDir string) string {
	sid, err := ourSID()
	if err != nil {
		return pipePrefix + "autodoc"
	}
	return pipePrefix + "autodoc-" + sid.String()
}

// IsLocal is whether a, a connection's peer address, is ipc's: a process of this machine.
func IsLocal(a net.Addr) bool { return a != nil && a.Network() == Network }

// playing is set while a test plays another user (PlayUser): no real peer's SID is then ours.
var playing atomic.Bool

// PlayUser makes this process expect another user, instead of itself, at the other end of its
// pipes, until restore: every real peer is then another user's. It is a test's seam, in any
// package, to show the daemon and its clients refusing another user's process; nothing else calls
// it. A pipe has no uid: uid is Unix's, and any value plays another user here.
// NOTE: process-wide: never from a parallel test (t.Parallel).
func PlayUser(uid int) (restore func()) {
	_ = uid
	playing.Store(true)
	return func() { playing.Store(false) }
}

// ourSID is this process's user's SID.
func ourSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

// ours is whether sid is the user both ends must run as: this process's, unless a test plays
// another (PlayUser).
func ours(sid *windows.SID) bool {
	me, err := ourSID()
	return err == nil && !playing.Load() && sid.Equals(me)
}

// userOnlySDDL is a protected DACL (D:P: nothing inherited) that admits this user's SID alone, with
// every right: the pipe's whole access control.
func userOnlySDDL() (string, error) {
	sid, err := ourSID()
	if err != nil {
		return "", err
	}
	return "D:P(A;;GA;;;" + sid.String() + ")", nil
}

// CheckAddr refuses addr as the daemon's address unless it is a named pipe's name. Who may open the
// pipe is its DACL's to say, which Listen sets and Dial checks.
func CheckAddr(addr string) error {
	if !strings.HasPrefix(addr, pipePrefix) || len(addr) == len(pipePrefix) {
		return &UnsafeDirError{Dir: addr, Why: "is not a named pipe's name (" + pipePrefix + "…)"}
	}
	return nil
}

// bind creates the pipe at addr with the user-only DACL. A pipe already there (a live daemon's, or
// anyone's) is an ErrInUse: Windows removes a pipe with its last handle, so there is never a dead
// one to take over.
func bind(addr string) (net.Listener, error) {
	sddl, err := userOnlySDDL()
	if err != nil {
		return nil, fmt.Errorf("ipc: this user's SID: %w", err)
	}
	ln, err := winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: sddl})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
		return nil, fmt.Errorf("%w: %s: %w", ErrInUse, addr, err)
	}
	return ln, err
}

// ownership is what Listen's Remove checks: nothing, since a pipe goes with its last handle.
type ownership struct{}

// own has nothing to take on a pipe: its DACL was set as it was created.
func own(net.Listener, string) (ownership, error) { return ownership{}, nil }

// remove has nothing to remove: Close took the pipe away.
func (ownership) remove(string) {}

// clientConn is a pipe this process dialled: its peer is the pipe's server.
type clientConn struct{ net.Conn }

// handleOf is c's pipe handle, ok when c is a named pipe's.
func handleOf(c net.Conn) (h windows.Handle, server bool, ok bool) {
	server = true
	if cc, isClient := c.(*clientConn); isClient {
		c, server = cc.Conn, false
	}
	f, isFile := c.(interface{ Fd() uintptr })
	if !isFile || c.LocalAddr() == nil || c.LocalAddr().Network() != Network {
		return 0, false, false
	}
	return windows.Handle(f.Fd()), server, true
}

// peerPID is the process at the other end of c: the client's on the daemon's side, the server's on
// a client's; ok when c is a named pipe's.
func peerPID(c net.Conn) (pid int, ok bool, err error) {
	h, server, ok := handleOf(c)
	if !ok {
		return 0, false, nil
	}
	var p uint32
	if server {
		err = windows.GetNamedPipeClientProcessId(h, &p)
	} else {
		err = windows.GetNamedPipeServerProcessId(h, &p)
	}
	return int(p), true, err
}

// peerUser is the user (its SID) the process at the other end of c runs as, from that process's
// token, whether it is this process's, and ok when c is a named pipe's.
func peerUser(c net.Conn) (user string, isOurs, ok bool, err error) {
	pid, ok, err := peerPID(c)
	if !ok || err != nil {
		return "", false, ok, err
	}
	sid, err := processSID(uint32(pid))
	if err != nil {
		return "", false, true, err
	}
	return "SID " + sid.String(), ours(sid), true, nil
}

// processSID is the user the process pid runs as, from its token.
func processSID(pid uint32) (*windows.SID, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		return nil, err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

// dial connects to the pipe at addr and refuses one whose DACL admits anyone but this user: a pipe
// someone made with a wider access list, the form on Windows of a socket in a folder others can
// write. The server's own user is Check's to read, as on Unix.
func dial(ctx context.Context, addr string) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, addr)
	if err != nil {
		return nil, err
	}
	cc := &clientConn{Conn: c}
	if err := checkDACL(cc, addr); err != nil {
		_ = c.Close()
		return nil, err
	}
	return cc, nil
}

// checkDACL refuses the pipe c is open on unless its DACL is protected (nothing inherited) and each
// of its entries is a plain allow for this user, or a deny: an allowlist, so an entry of any other
// kind (a conditional or callback allow, an object allow) is refused rather than read past.
func checkDACL(c *clientConn, addr string) error {
	h, _, ok := handleOf(c)
	if !ok {
		return nil
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("ipc: reading %s's access list: %w", addr, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("ipc: reading %s's access list: %w", addr, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return &UnsafeDirError{Dir: addr, Why: "has an access list that is not protected, so it may take entries it inherits"}
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return &UnsafeDirError{Dir: addr, Why: "has no access list, so every user may open it"}
	}
	me, err := ourSID()
	if err != nil {
		return err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("ipc: reading %s's access list: %w", addr, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE: // a deny only narrows who may open it
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !sid.Equals(me) {
				return &UnsafeDirError{Dir: addr, Why: "admits another user (" + sid.String() + ") in its access list"}
			}
		default:
			return &UnsafeDirError{Dir: addr, Why: fmt.Sprintf("has an access-list entry of a kind this check does not admit (type %d)", ace.Header.AceType)}
		}
	}
	return nil
}
