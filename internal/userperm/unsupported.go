//go:build !(linux || darwin || windows)

package userperm

// This OS has no implementation of userperm's per-OS half (userperm_unix.go, userperm_windows.go): a
// port adds its file.
var _ = userpermIsNotImplementedOnThisOS
