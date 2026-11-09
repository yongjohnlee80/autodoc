//go:build !(linux || darwin)

package ipc

// This OS has no implementation of ipc's per-OS half (ipc_unix.go, listen_unix.go and the peer
// files): a port adds its own.
var _ = ipcIsNotImplementedOnThisOS
