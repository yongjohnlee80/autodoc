package store

import (
	"encoding/json"
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
	return f, nil
}

func leaseLockPath(path string) (string, error) {
	dir, id, err := identity(path, true)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".autodoc-lease-"+id), nil
}

// identity is the store at path's directory and identity, its device and inode, as the lease names
// it: the store, a symlink to it and a hardlink to it are one store. A missing store is created when
// create is set (the lease's case), and is an os.ErrNotExist otherwise (a client's: no daemon can
// hold a store that is not there).
func identity(path string, create bool) (dir, id string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("store: resolving %s: %w", path, err)
	}
	if _, err := os.Lstat(abs); create && errors.Is(err, os.ErrNotExist) {
		f, cerr := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if cerr != nil && !errors.Is(cerr, os.ErrExist) {
			return "", "", fmt.Errorf("store: creating %s: %w", abs, cerr)
		}
		if f != nil {
			_ = f.Close()
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("store: resolving %s: %w", abs, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("store: reading %s: %w", resolved, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("store: no inode for %s on this platform", resolved)
	}
	return filepath.Dir(resolved), fmt.Sprintf("%d-%d", st.Dev, st.Ino), nil
}

// Identity is the store at path's identity, its device and inode: the name its lease is taken
// under, and what its daemon reports as store_id. It never creates the store.
func Identity(path string) (string, error) {
	_, id, err := identity(path, false)
	return id, err
}

// LeaseInfo is what the daemon holding a store's lease says of itself, beside the store: how a
// client started with another config, and so perhaps another socket, finds the daemon serving the
// store it names. It is a hint: a client believes it only when that address answers as this
// store's daemon (store_id) and this process (instance).
type LeaseInfo struct {
	StoreID     string    `json:"store_id"`
	StorePath   string    `json:"store_path"`
	Addr        string    `json:"addr"`
	PID         int64     `json:"pid"`
	Instance    string    `json:"instance"`
	Version     string    `json:"version"`
	Protocol    int64     `json:"protocol"`
	MinProtocol int64     `json:"min_protocol"`
	Since       time.Time `json:"since"`
}

// leaseInfoPath is where the store at path's lease-info is kept: beside it, named, as the lock is,
// for its identity, so every spelling of one store's path reads one file.
func leaseInfoPath(path string, create bool) (string, error) {
	dir, id, err := identity(path, create)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".autodoc-lease-"+id+".json"), nil
}

// WriteLeaseInfo records the lease holder for the store at path, atomically: written to a temporary
// file, synced, then renamed over the old one, so a reader sees a whole record or the previous one.
func WriteLeaseInfo(path string, li LeaseInfo) error {
	dest, err := leaseInfoPath(path, false)
	if err != nil {
		return err
	}
	data, err := json.Marshal(li)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".autodoc-lease-info-*")
	if err != nil {
		return fmt.Errorf("store: writing the lease-info: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// ReadLeaseInfo reads the lease-info recorded for the store at path.
func ReadLeaseInfo(path string) (LeaseInfo, error) {
	src, err := leaseInfoPath(path, false)
	if err != nil {
		return LeaseInfo{}, err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return LeaseInfo{}, err
	}
	var li LeaseInfo
	if err := json.Unmarshal(data, &li); err != nil {
		return LeaseInfo{}, fmt.Errorf("store: the lease-info %s: %w", src, err)
	}
	return li, nil
}

// RemoveLeaseInfo removes the store at path's lease-info when it is still instance's: a daemon
// shutting down takes its own record away, never a successor's.
func RemoveLeaseInfo(path, instance string) error {
	li, err := ReadLeaseInfo(path)
	if err != nil || li.Instance != instance {
		return nil
	}
	src, err := leaseInfoPath(path, false)
	if err != nil {
		return err
	}
	if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
