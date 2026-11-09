//go:build !(linux || darwin || windows)

package fileid

// This OS has no implementation of fileid's per-OS half (fileid_unix.go, fileid_windows.go): a port
// adds its file.
var _ = fileidIsNotImplementedOnThisOS
