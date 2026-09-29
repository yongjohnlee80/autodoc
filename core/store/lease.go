package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrBusy is a store another AutoDoc instance already serves: its lease is held.
var ErrBusy = errors.New("store: another instance serves this store")

// acquireLease takes the single-instance lease on the store at path: an flock
// on a sidecar named for the store's device and inode, beside it.
//
// The lock names the DATABASE, not the spelling of the path: the store, a
// symlink to it and a hardlink to it in its directory are one store. It is a
// sidecar and never the store itself, because on darwin an flock is refused
// while the process holds an fcntl lock on the same file, and SQLite's unix VFS
// holds those from the moment the store opens. The store is never opened here:
// closing any descriptor to it would drop SQLite's fcntl locks; os.Stat reads
// the inode without one. A missing store is created first (an empty file is a
// valid empty SQLite database).
func acquireLease(path string) (*os.File, error) {
	lockPath, err := leaseLockPath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: opening the lease file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", ErrBusy, path)
		}
		return nil, fmt.Errorf("store: locking %s: %w", path, err)
	}
	// who holds it, for a human reading a refusal: beside the store, never read back
	if info, err := os.OpenFile(path+".lease-info", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		_, _ = fmt.Fprintf(info, "pid %d\nsince %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
		_ = info.Close()
	}
	return f, nil
}

func leaseLockPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("store: resolving %s: %w", path, err)
	}
	if _, err := os.Lstat(abs); errors.Is(err, os.ErrNotExist) {
		f, cerr := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if cerr != nil && !errors.Is(cerr, os.ErrExist) {
			return "", fmt.Errorf("store: creating %s: %w", abs, cerr)
		}
		if f != nil {
			_ = f.Close()
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("store: resolving %s: %w", abs, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("store: reading %s: %w", resolved, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("store: no inode for %s on this platform", resolved)
	}
	return filepath.Join(filepath.Dir(resolved), fmt.Sprintf(".autodoc-lease-%d-%d", st.Dev, st.Ino)), nil
}
