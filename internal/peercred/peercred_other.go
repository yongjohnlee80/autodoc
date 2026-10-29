//go:build !linux && !darwin

package peercred

import "net"

// peerUID has no form here: AutoDoc's daemon is built for Linux and macOS.
func peerUID(*net.UnixConn) (int, error) { return 0, errUnsupported }
