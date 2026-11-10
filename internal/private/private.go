// Package private says whether a file is private to this user, and makes one so: the check a program
// makes before it trusts a key or a secret it left for itself, and the way it writes one. A file
// another user may read or write is exposed.
//
// What "private" is on each OS is in this package's per-OS files: no group or other permission
// bits on Unix; on Windows, an owner that is this user and a DACL that grants access to no one but
// this user, SYSTEM and the Administrators (whom no file can keep out, as root on Unix). unsupported.go
// fails the build on any other OS, naming the seam.
package private

import (
	"errors"
	"fmt"
	"os"
)

// ErrExposed is a file others may read or write, or that another user owns.
var ErrExposed = errors.New("private: the file is not private to this user")

// Check is nil when the file at path (a symlink followed, as the keys are read) is private to this
// user; an error wrapping ErrExposed, saying what is exposed and how to make it private in the OS's
// terms, when it is not; and the error of reading it otherwise (os.ErrNotExist's when it is not
// there).
func Check(path string) error {
	exposed, err := check(path)
	if err != nil {
		return fmt.Errorf("private: %s: %w", path, err)
	}
	if exposed != "" {
		return fmt.Errorf("%w: %s %s", ErrExposed, path, exposed)
	}
	return nil
}

// Create makes a new file at path for writing, private to this user from its creation: there is
// never a moment another user could open it. It fails, wrapping os.ErrExist, when a file is there.
func Create(path string) (*os.File, error) {
	f, err := create(path)
	if err != nil {
		return nil, fmt.Errorf("private: %s: %w", path, err)
	}
	return f, nil
}

// Restrict makes the existing file at path private to this user. It fails on a file another user
// owns rather than taking it.
func Restrict(path string) error {
	if err := restrict(path); err != nil {
		return fmt.Errorf("private: %s: %w", path, err)
	}
	return nil
}
