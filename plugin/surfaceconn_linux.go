package plugin

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/yongjohnlee80/golib/msgpack"
	"golang.org/x/sys/unix"
)

// THE SIDE CHANNEL'S SOCKET (Linux): a SOCK_SEQPACKET pair, one datagram per message, its fds as
// SCM_RIGHTS. A received fd is the message's until admission takes it (FDGuard): no rejection path
// leaks a descriptor (ADR 1792797354 §4.2, rationale R12).

// SurfaceConn is one end of the side channel. Send and Recv may run on different goroutines; Close
// unblocks a Recv.
type SurfaceConn struct {
	fd        int
	closeOnce sync.Once
	buf, oob  []byte
}

// ErrSurfaceClosed is a side channel whose peer went, or that was closed.
var ErrSurfaceClosed = errors.New("plugin: the side channel is closed")

// NewSurfacePair is the host's end and the file to hand the plugin as fd 3 (exec.Cmd.ExtraFiles).
// Both are close-on-exec; ExtraFiles clears it on the child's fd 3 alone.
func NewSurfacePair() (*SurfaceConn, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return newSurfaceConn(fds[0]), os.NewFile(uintptr(fds[1]), "autodoc-surface"), nil
}

func newSurfaceConn(fd int) *SurfaceConn {
	return &SurfaceConn{fd: fd, buf: make([]byte, MaxSurfaceDatagram+1), oob: make([]byte, unix.CmsgSpace(4*(MaxSurfaceFDs+1)))}
}

// OpenPluginSurfaceConn is the plugin's end, from SurfaceFDEnv: close-on-exec first, so a process the
// plugin starts does not inherit it by accident (hygiene, not a boundary). ok false: no side channel.
func OpenPluginSurfaceConn() (*SurfaceConn, bool, error) {
	v := os.Getenv(SurfaceFDEnv)
	if v == "" {
		return nil, false, nil
	}
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil, false, fmt.Errorf("plugin: %s=%q: want a descriptor from 3", SurfaceFDEnv, v)
	}
	unix.CloseOnExec(fd)
	return newSurfaceConn(fd), true, nil
}

// Send writes m, with fds as SCM_RIGHTS. The fds stay the caller's.
func (c *SurfaceConn) Send(m map[string]any, fds ...int) error {
	b, err := msgpack.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > MaxSurfaceDatagram || len(fds) > MaxSurfaceFDs {
		return fmt.Errorf("%w: %d bytes, %d fds", ErrSurfaceWire, len(b), len(fds))
	}
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	if err := unix.Sendmsg(c.fd, b, oob, nil, 0); err != nil {
		if errors.Is(err, unix.EPIPE) || errors.Is(err, unix.EBADF) || errors.Is(err, unix.ECONNRESET) {
			return ErrSurfaceClosed
		}
		return err
	}
	return nil
}

// surfaceLimits bound a datagram's decode: it is the peer's, so attacker-adjacent.
var surfaceLimits = func() *msgpack.Limits {
	l := msgpack.DefaultLimits()
	l.MaxDepth, l.MaxElements, l.MaxTotalElements = 4, 64, 256
	l.MaxStrBytes, l.MaxBinBytes, l.MaxTotalBytes = 64, 0, MaxSurfaceDatagram
	return l
}()

// Recv reads one datagram: its map, and a guard owning the fds it carried. Any error has closed
// them all. A truncated datagram or control message, a record that isn't SCM_RIGHTS, too many fds,
// or a map that doesn't decode is ErrSurfaceWire: a violation.
func (c *SurfaceConn) Recv() (map[string]any, *FDGuard, error) {
	n, oobn, flags, _, err := unix.Recvmsg(c.fd, c.buf, c.oob, unix.MSG_CMSG_CLOEXEC)
	g := guardRights(c.oob[:oobn])
	switch {
	case err != nil:
		g.CloseAll()
		if errors.Is(err, unix.EBADF) || errors.Is(err, unix.ECONNRESET) {
			return nil, nil, ErrSurfaceClosed
		}
		return nil, nil, err
	case n == 0 && oobn == 0:
		return nil, nil, ErrSurfaceClosed // the peer shut down
	case flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0, n > MaxSurfaceDatagram, g.foreign, g.Len() > MaxSurfaceFDs:
		g.CloseAll()
		return nil, nil, fmt.Errorf("%w: truncated, oversized, or carrying a foreign control record", ErrSurfaceWire)
	}
	v, err := msgpack.Unmarshal(c.buf[:n], surfaceLimits)
	m, ok := v.(map[string]any)
	if err != nil || !ok {
		g.CloseAll()
		return nil, nil, fmt.Errorf("%w: not a map", ErrSurfaceWire)
	}
	return m, g, nil
}

// Close shuts the socket down, which ends a Recv blocked on it, then closes it. Once.
func (c *SurfaceConn) Close() error {
	c.closeOnce.Do(func() {
		unix.Shutdown(c.fd, unix.SHUT_RDWR)
		unix.Close(c.fd)
	})
	return nil
}

// FDGuard owns a received message's fds until Take transfers each: CloseAll closes the rest.
type FDGuard struct {
	fds     []int
	taken   []bool
	foreign bool // a control record that isn't SCM_RIGHTS
}

// guardRights takes every fd the kernel installed from oob, whatever else it holds.
func guardRights(oob []byte) *FDGuard {
	g := &FDGuard{}
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		g.foreign = len(oob) > 0
		return g
	}
	for i := range msgs {
		fds, err := unix.ParseUnixRights(&msgs[i])
		if err != nil {
			g.foreign = true
			continue
		}
		g.fds = append(g.fds, fds...)
	}
	g.taken = make([]bool, len(g.fds))
	return g
}

// Len is how many fds came.
func (g *FDGuard) Len() int { return len(g.fds) }

// Take transfers fd i to the caller; -1 when there is none, or it was taken.
func (g *FDGuard) Take(i int) int {
	if i < 0 || i >= len(g.fds) || g.taken[i] {
		return -1
	}
	g.taken[i] = true
	return g.fds[i]
}

// CloseAll closes every fd not taken. Safe to call more than once.
func (g *FDGuard) CloseAll() *FDGuard {
	for i, fd := range g.fds {
		if !g.taken[i] {
			unix.Close(fd)
			g.taken[i] = true
		}
	}
	return g
}
