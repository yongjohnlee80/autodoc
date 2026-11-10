// Package userperm keeps a file private to this user: it opens one only when it is private, and
// makes one private from the moment it exists. A key or a secret a program left for itself is read
// through OpenPrivate and written through CreatePrivate, so what is checked is what is read: the
// handle the check was made on is the one returned.
//
// What "private" is on each OS is in this package's per-OS files: no group or other permission
// bits on Unix; on Windows, a DACL that grants access to no one but this user, SYSTEM and the
// Administrators (named administrative trust: no file keeps them out, as root on Unix), with a
// NULL DACL, or an access entry of a form not read, counted as exposure. Either way the owner must
// be this user (internal/fileowner). What this package makes is stricter still: this user alone.
//
// The folder a private file lives in is trusted: its own permissions, and its ancestors' links or
// junctions, are the caller's to choose (a store's own folder). The final name is never followed.
// unsupported.go fails the build on any other OS, naming the seam.
package userperm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/fileowner"
)

// ErrExposed is a file others may read or write, one another user owns, or an entry that is not a
// regular file at all (a link, a reparse point, a device): never one to read a secret from.
var ErrExposed = errors.New("userperm: the file is not private to this user")

// OpenPrivate opens the regular file at path for reading, its final name not followed, when it is
// private to this user, and returns that handle: read the secret through it. An entry that is not
// private, not this user's, a link or not a regular file is an error wrapping ErrExposed, saying
// what and how to fix it in the OS's terms; a missing one wraps os.ErrNotExist.
func OpenPrivate(path string) (*os.File, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("userperm: %s: %w", path, err)
	}
	if err := checkOpen(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("userperm: %s: %w", path, err)
	}
	return f, nil
}

// checkOpen is nil when the object f holds is a regular file this user owns and only this user may
// reach (exposure: private reads it).
func checkOpen(f *os.File) error {
	if err := regular(f); err != nil {
		return err
	}
	if err := fileowner.OwnedByFile(f); err != nil {
		if errors.Is(err, fileowner.ErrNotOurs) {
			return fmt.Errorf("%w: %w", ErrExposed, err)
		}
		return err
	}
	exposed, err := exposure(f)
	if err != nil {
		return err
	}
	if exposed != "" {
		return fmt.Errorf("%w: it %s", ErrExposed, exposed)
	}
	return nil
}

// CreatePrivate makes a new file at path for writing, private to this user from its creation:
// there is never a moment another user could open it. It fails, wrapping os.ErrExist, when anything
// is there. The handle is not inherited by a child process.
func CreatePrivate(path string) (*os.File, error) {
	f, err := create(path)
	if err != nil {
		return nil, fmt.Errorf("userperm: %s: %w", path, err)
	}
	return f, nil
}

// CreatePrivateTemp is os.CreateTemp made private from its creation: a new file in dir whose name
// is pattern with its last "*" replaced by a random string (appended when it has none).
func CreatePrivateTemp(dir, pattern string) (*os.File, error) {
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	for try := 0; ; try++ {
		name := filepath.Join(dir, prefix+strconv.FormatUint(uint64(random()), 36)+suffix)
		f, err := CreatePrivate(name)
		if errors.Is(err, os.ErrExist) && try < 10000 {
			continue
		}
		return f, err
	}
}
