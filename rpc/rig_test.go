package rpc

import (
	"net"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// rigListen listens on network as the daemon does: the daemon's own network through ipc.Listen, so
// its peers are local, and tcp as it is, whose peers are not.
func rigListen(network, addr string) (net.Listener, error) {
	if network == ipc.Network {
		return ipc.Listen(addr, func(error) {})
	}
	return net.Listen(network, addr)
}

// rigNetwork is how a client dials network: the daemon's own through ipc, as the product does.
func rigNetwork(network string) []golibrpc.ClientOption {
	if network == ipc.Network {
		return []golibrpc.ClientOption{golibrpc.ClientNetwork(network), golibrpc.WithConnDialer(ipc.RPCDialer)}
	}
	return []golibrpc.ClientOption{golibrpc.ClientNetwork(network)}
}
