//go:build !(linux || darwin || windows)

package procenv

// This OS has no implementation of procenv's per-OS half (procenv_unix.go, procenv_windows.go): a port
// adds its file.
var _ = procenvIsNotImplementedOnThisOS
