package workspace

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"
)

// ErrNotADirectory is a workspace root that is not a directory.
var ErrNotADirectory = errs.Sentinel(errs.ErrInvalidArgument, "workspace: the root is not a directory")

// Workspace is one open root.
type Workspace struct {
	Name    string
	Root    string
	FS      vfs.FS
	Matcher Matcher
}

// Open opens the workspace's root with the local driver; a root that is not a directory is
// ErrNotADirectory. The driver holds the opened directory (os.OpenRoot refuses anything else), so
// no later check of it is needed.
func Open(cfg Spec) (*Workspace, error) {
	fsys, err := local.New(cfg.Root)
	switch {
	case errors.Is(err, fs.ErrPermission): // a directory it may not open is not a missing one
		return nil, fmt.Errorf("workspace %q: opening root %s: %w", cfg.Name, cfg.Root, err)
	case err != nil: // gone, or a file
		return nil, fmt.Errorf("workspace %q: %s: %w: %w", cfg.Name, cfg.Root, ErrNotADirectory, err)
	}
	return &Workspace{Name: cfg.Name, Root: cfg.Root, FS: fsys, Matcher: NewMatcher(cfg.Include, cfg.Exclude)}, nil
}

// Close closes the root.
func (w *Workspace) Close() error { return w.FS.Close() }
