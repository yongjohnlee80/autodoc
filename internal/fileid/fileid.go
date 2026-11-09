// Package fileid is a file's identity on its machine: the same for the file through any of its names
// (a hardlink to it, a symlink to it), and another for any other file. It names what a path leads to,
// so a store reached by two spellings is one store (core/store's lease and store_id).
//
// The OS-specific part, what the file system calls a file's identity, is in this package's per-OS
// files (a device and inode on Unix; a volume serial and file index on Windows); the errors are here.
// unsupported.go fails the build on any other OS, naming the seam.
package fileid

import "fmt"

// ID is the identity of the file path leads to, a symlink followed. A path that is not there is an
// error wrapping os.ErrNotExist.
func ID(path string) (string, error) {
	id, err := id(path)
	if err != nil {
		return "", fmt.Errorf("fileid: %s: %w", path, err)
	}
	return id, nil
}
