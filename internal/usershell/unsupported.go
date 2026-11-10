//go:build !(linux || darwin || windows)

package usershell

// This OS has no implementation of usershell's per-OS half (usershell_unix.go, usershell_windows.go):
// a port adds its file.
var _ = usershellIsNotImplementedOnThisOS
