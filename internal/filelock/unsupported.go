//go:build !(linux || darwin)

package filelock

// This OS has no implementation of filelock's per-OS half (filelock_unix.go): a port adds its file.
var _ = filelockIsNotImplementedOnThisOS
