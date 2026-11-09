//go:build !(linux || darwin)

package diskspace

// This OS has no implementation of diskspace's per-OS half (diskspace_unix.go): a port adds its file.
var _ = diskspaceIsNotImplementedOnThisOS
