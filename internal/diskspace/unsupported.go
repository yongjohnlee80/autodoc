//go:build !(linux || darwin || windows)

package diskspace

// This OS has no implementation of diskspace's per-OS half (diskspace_unix.go, diskspace_windows.go):
// a port adds its file.
var _ = diskspaceIsNotImplementedOnThisOS
