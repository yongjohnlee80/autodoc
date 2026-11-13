//go:build linux || darwin

package userperm

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path for reading without following a final symbolic link (refused, ELOOP) and
// without blocking on a FIFO, which the check then refuses as no regular file.
func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%w: it is a symbolic link, which a private file never is", ErrExposed)
	}
	return f, err
}

// regular is nil when f holds a regular file.
func regular(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: it is %v, not a regular file", ErrExposed, fi.Mode().Type())
	}
	return nil
}

// exposure is what exposes the file f holds, "" when nothing does: a group or other permission bit.
func exposure(f *os.File) (string, error) {
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if p := fi.Mode().Perm(); p&0o077 != 0 {
		return fmt.Sprintf("is %v, which others may read or write: make it 0600", p), nil
	}
	return "", nil
}

// create is a new file, mode 0600 from its creation (a umask can only take bits away); Go's files
// are close-on-exec.
func create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
