//go:build linux

package app

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestPeerProcIsTheProcessAtTheSocketsOtherEnd: the daemon's crew.PeerProc (the comms module's port,
// docs/decisions/crew.md#three-modules-and-one-protocol) answers the reference of the process at a
// unix connection's other end, from the kernel's peer credentials: here this test process, on both
// ends. A connection that is no unix socket's has no peer process to answer, so it is refused,
// never passed.
func TestPeerProcIsTheProcessAtTheSocketsOtherEnd(t *testing.T) {
	dir, err := os.MkdirTemp("", "pp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l, err := net.Listen("unix", filepath.Join(dir, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	c, err := net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	defer server.Close()
	want, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for name, conn := range map[string]net.Conn{"the dialer's side": c, "the listener's side": server} {
		if got, err := peerProc(conn); err != nil || got != want {
			t.Errorf("%s: %q, %v; want this process, %q", name, got, err, want)
		}
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got, err := peerProc(a); err == nil {
		t.Errorf("a pipe, no unix socket: %q, want refused", got)
	}
}
