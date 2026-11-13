//go:build !(linux || darwin || windows)

package proctree

// This OS has no implementation of proctree's per-OS half (proctree_unix.go, proctree_windows.go): a
// port adds its file.
var _ = proctreeIsNotImplementedOnThisOS
