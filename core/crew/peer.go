package crew

import "net"

// PeerProc is the process reference (ProcRef's "<boot id>:<pid ns>:<pid>:<start time>") of the
// process at c's other end, its user checked as the daemon's: what binds an endpoint's socket to
// the session's own process before every wake (boundTo). An error refuses the endpoint. The host
// that wires the wakers supplies it (AutoDoc's daemon, from the kernel's peer credentials), so the
// module asks the platform for nothing itself (docs/decisions/crew.md#three-modules-and-one-protocol).
type PeerProc func(c net.Conn) (string, error)
