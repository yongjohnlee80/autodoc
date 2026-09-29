// Package workspace opens a workspace's root: its files as a vfs.FS and the matcher of its include
// and exclude patterns. The workspace itself is a row of the store (core/store), and the store's
// lease makes the daemon the only one serving it.
package workspace

import (
	"errors"
	"fmt"
	"os"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/config"
)

// ErrNotADirectory is a workspace root that is not a directory.
var ErrNotADirectory = errors.New("workspace: the root is not a directory")

// Workspace is one open root.
type Workspace struct {
	Name    string
	Root    string
	FS      vfs.FS
	Matcher Matcher
}

// Open opens the workspace's root with the local driver.
func Open(cfg config.Workspace) (*Workspace, error) {
	if fi, err := os.Stat(cfg.Root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("workspace %q: %s: %w", cfg.Name, cfg.Root, ErrNotADirectory)
	}
	fsys, err := local.New(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("workspace %q: opening root %s: %w", cfg.Name, cfg.Root, err)
	}
	return &Workspace{Name: cfg.Name, Root: cfg.Root, FS: fsys, Matcher: NewMatcher(cfg.Include, cfg.Exclude)}, nil
}

// Close closes the root.
func (w *Workspace) Close() error { return w.FS.Close() }
