//go:build linux

package crew_test

import (
	"fmt"
	"net"
	"os"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// selfPeer is the cells' crew.PeerProc: every socket a cell's waker dials is held by a stand-in in
// this test process, so the peer at its other end is this process. Refusing another process is
// boundTo's own comparison of that reference with the locator's, which the cells exercise; the
// kernel's peer credentials are the daemon's adapter (app's peerProc) and are tested there.
func selfPeer(c net.Conn) (string, error) {
	if _, ok := c.(*net.UnixConn); !ok {
		return "", fmt.Errorf("selfPeer: %T is no unix connection", c)
	}
	return crew.ProcRef(os.Getpid())
}
