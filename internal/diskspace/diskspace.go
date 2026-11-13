// Package diskspace is the free space on a file system, as an unprivileged writer sees it: what a
// program may still write before the disk is full.
//
// The OS-specific part, asking the file system, is in this package's per-OS files; the errors are
// here.
package diskspace

import "fmt"

// Free is the free space, in bytes, on the file system holding the directory dir, as a writer
// without special privileges may use it. A dir that is not there is an error wrapping
// os.ErrNotExist.
func Free(dir string) (uint64, error) {
	n, err := free(dir)
	if err != nil {
		return 0, fmt.Errorf("diskspace: %s: %w", dir, err)
	}
	return n, nil
}
