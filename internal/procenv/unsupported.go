//go:build !(linux || darwin)

package procenv

// This OS has no implementation of procenv's per-OS half (procenv_unix.go): a port adds its file.
var _ = procenvIsNotImplementedOnThisOS
