//go:build !(linux || darwin || windows)

package userdirs

// This OS has no implementation of userdirs' per-OS half (userdirs_unix.go, userdirs_<os>.go): a
// port adds its files.
var _ = userdirsIsNotImplementedOnThisOS
