//go:build !(linux || darwin || windows)

package private

// This OS has no implementation of private's per-OS half (private_unix.go, private_windows.go): a
// port adds its file.
var _ = privateIsNotImplementedOnThisOS
