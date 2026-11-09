// Package fileowner says whether a file belongs to this process's user: the check a program makes
// before it trusts a socket, a folder or a file another program left for it. It reads the entry at the
// path itself, a symlink not followed, so a link cannot lend its target's owner.
//
// The OS-specific part, who owns a file, is in this package's per-OS files (the uid on Unix; the
// owner SID on Windows); the errors are here. unsupported.go fails the build on any other OS, naming
// the seam.
package fileowner

import (
	"errors"
	"fmt"
)

// ErrNotOurs is a file another user owns.
var ErrNotOurs = errors.New("fileowner: the file belongs to another user")

// OwnedBy is nil when the entry at path, a symlink not followed, belongs to this process's user; an
// error wrapping ErrNotOurs, naming its owner, when another's; os.ErrNotExist's when it is not there.
func OwnedBy(path string) error {
	owner, ours, err := owner(path)
	if err != nil {
		return fmt.Errorf("fileowner: %s: %w", path, err)
	}
	if !ours {
		return fmt.Errorf("%w: %s is owned by %s", ErrNotOurs, path, owner)
	}
	return nil
}
