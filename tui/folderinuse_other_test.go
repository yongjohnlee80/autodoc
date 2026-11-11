//go:build !windows

package tui

// folderInUse reports whether err is a removal the OS refused because a process holds the folder:
// no other OS refuses one (folderinuse_windows_test.go).
func folderInUse(error) bool { return false }
