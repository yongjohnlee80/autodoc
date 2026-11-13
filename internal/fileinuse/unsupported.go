//go:build !(linux || darwin || windows)

package fileinuse

// This OS has no implementation of fileinuse's per-OS half (fileinuse_unix.go,
// fileinuse_windows.go): a port adds its file.
var _ = fileinuseIsNotImplementedOnThisOS
