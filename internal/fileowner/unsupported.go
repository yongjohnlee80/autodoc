//go:build !(linux || darwin || windows)

package fileowner

// This OS has no implementation of fileowner's per-OS half (fileowner_unix.go, fileowner_windows.go):
// a port adds its file.
var _ = fileownerIsNotImplementedOnThisOS
