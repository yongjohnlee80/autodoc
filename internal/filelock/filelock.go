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
	"os"
)

// ErrHeld is a lock another holder has: another process, or another TryLock on the same file.
var ErrHeld = errors.New("filelock: the lock is held")

// Unlock releases a lock TryLock took. It is safe to call more than once; later calls do nothing.
type Unlock func() error

// TryLock takes the exclusive lock on the file at path, creating the file (mode 0600) when it is not
// there. It never waits: a lock someone holds is an error wrapping ErrHeld.
func TryLock(path string) (Unlock, error) { return take(path, false) }

// Lock is TryLock that waits while another holder has the lock, until it is free.
func Lock(path string) (Unlock, error) { return take(path, true) }

func take(path string, wait bool) (Unlock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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
		return f.Close() // closing the file releases its lock, on every OS
	}, nil
}
