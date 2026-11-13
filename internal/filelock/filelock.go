// Package filelock is an exclusive lock on a file, held by one holder at a time: TryLock takes it or
// says another holder has it (ErrHeld) without waiting, and Lock waits for it. A lock lasts until
// its Unlock runs or the process ends, so a crashed holder never leaves it taken.
//
// The OS-specific part, the lock itself, is in this package's per-OS files (flock on Linux and
// macOS, LockFileEx on Windows); everything they share (opening the file, the errors) is here.
package filelock

import (
	"errors"
	"fmt"
)

// ErrHeld is a lock another holder has: another process, or another TryLock on the same file.
var ErrHeld = errors.New("filelock: the lock is held")

// Unlock releases a lock TryLock took. It is safe to call more than once; later calls do nothing.
type Unlock func() error

// Option changes how TryLock and Lock open the lock's file.
type Option func(*options)

type options struct{ noFollow bool }

// NoFollow refuses a path whose last element is a link, rather than lock the file it leads to: for a
// lock file in a folder another program may write in. On Windows any reparse point is refused.
func NoFollow() Option { return func(o *options) { o.noFollow = true } }

// TryLock takes the exclusive lock on the file at path, creating the file (mode 0600) when it is not
// there. It never waits: a lock someone holds is an error wrapping ErrHeld.
func TryLock(path string, opts ...Option) (Unlock, error) { return take(path, false, opts) }

// Lock is TryLock that waits while another holder has the lock, until it is free.
func Lock(path string, opts ...Option) (Unlock, error) { return take(path, true, opts) }

func take(path string, wait bool, opts []Option) (Unlock, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	f, err := open(path, o.noFollow)
	if err != nil {
		return nil, fmt.Errorf("filelock: opening %s: %w", path, err)
	}
	held, err := lock(f, wait)
	if err != nil || held {
		_ = f.Close()
		if held {
			return nil, fmt.Errorf("%w: %s", ErrHeld, path)
		}
		return nil, fmt.Errorf("filelock: locking %s: %w", path, err)
	}
	done := false
	return func() error {
		if done {
			return nil
		}
		done = true
		// released before the close: closing alone releases it too, but Windows may do that
		// lazily, so a lock taken right after could still be refused
		return errors.Join(unlock(f), f.Close())
	}, nil
}
