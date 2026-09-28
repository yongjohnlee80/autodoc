// Package workspace opens a configured workspace: its root as a vfs.FS, its state directory, and the
// single-instance lease on its index store.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/config"
)

// ErrWorkspaceBusy is a workspace another AutoDoc instance already serves: its store's lease is held.
var ErrWorkspaceBusy = errors.New("workspace: another instance serves this workspace")

// Workspace is one open root.
type Workspace struct {
	Name     string
	Root     string
	FS       vfs.FS
	StateDir string // $STATE/workspaces/<name>: the index lives here, never in the root
	Matcher  Matcher

	lease *os.File
}

// Open opens the workspace's root with the local driver and takes the lease on its store. A lease
// another process holds is ErrWorkspaceBusy; the caller serves its other workspaces.
func Open(cfg config.Workspace, stateDir string) (*Workspace, error) {
	dir := filepath.Join(stateDir, "workspaces", cfg.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("workspace %q: creating its state directory: %w", cfg.Name, err)
	}
	lease, err := acquireLease(filepath.Join(dir, "index.db"))
	if err != nil {
		return nil, fmt.Errorf("workspace %q: %w", cfg.Name, err)
	}
	fsys, err := local.New(cfg.Root)
	if err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("workspace %q: opening root %s: %w", cfg.Name, cfg.Root, err)
	}
	return &Workspace{Name: cfg.Name, Root: cfg.Root, FS: fsys, StateDir: dir,
		Matcher: NewMatcher(cfg.Include, cfg.Exclude), lease: lease}, nil
}

// IndexPath is the workspace's SQLite store, the file the lease is held on.
func (w *Workspace) IndexPath() string { return filepath.Join(w.StateDir, "index.db") }

// Close closes the root and releases the lease.
func (w *Workspace) Close() error {
	err := w.FS.Close()
	if w.lease != nil {
		if cerr := w.lease.Close(); err == nil {
			err = cerr
		}
		w.lease = nil
	}
	return err
}

// acquireLease locks the store file itself, so two spellings of one path (a symlink, a hard link)
// cannot both be granted it: the identity is the inode. flock does not disturb SQLite, whose unix
// VFS uses the independent fcntl record locks. An empty file is a valid empty SQLite database, so
// creating it here is what a first run's store starts as.
func acquireLease(store string) (*os.File, error) {
	f, err := os.OpenFile(store, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the store %s to lease it: %w", store, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", ErrWorkspaceBusy, store)
		}
		return nil, fmt.Errorf("locking the store %s: %w", store, err)
	}
	// who holds it, for a human reading a refusal: beside the store, never read back
	if info, err := os.OpenFile(store+".lease-info", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		_, _ = fmt.Fprintf(info, "pid %d\nsince %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
		_ = info.Close()
	}
	return f, nil
}
