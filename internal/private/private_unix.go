//go:build linux || darwin

package private

import (
	"fmt"
	"os"
)

// check is what makes the file exposed, "" when nothing does: on Unix, a group or other permission
// bit (mode & 0o077).
func check(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if p := fi.Mode().Perm(); p&0o077 != 0 {
		return fmt.Sprintf("is %v, which others may read or write: make it 0600", p), nil
	}
	return "", nil
}

// create is a new file, mode 0600: a umask can only take bits away.
func create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// restrict makes the file 0600.
func restrict(path string) error { return os.Chmod(path, 0o600) }
