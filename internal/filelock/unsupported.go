//go:build !(linux || darwin || windows)

package filelock

// This OS has no implementation of filelock's per-OS half (filelock_unix.go, filelock_windows.go): a
// port adds its file.
var _ = filelockIsNotImplementedOnThisOS
