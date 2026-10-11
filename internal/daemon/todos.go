package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// TodoRoots are the served workspaces' roots, as core/todo resolves a workspace name: a workspace
// that could not be opened has no root.
type TodoRoots struct{ Workspaces rpc.Workspaces }

// Root is the root of the workspace named name.
func (r TodoRoots) Root(name string) (string, bool) {
	w, ok := r.Workspaces.Get(name)
	if !ok || w.Err() != nil {
		return "", false
	}
	return w.Root(), true
}

// Names are the served workspaces' names.
func (r TodoRoots) Names() []string {
	var out []string
	for _, w := range r.Workspaces.List() {
		if w.Err() == nil {
			out = append(out, w.Name())
		}
	}
	return out
}

// TodoDisk opens directories on the daemon's own disk for core/todo, each through golib's
// vfs/local, so every name inside is resolved within it: the one place todos meet the disk.
type TodoDisk struct{ Log logger.Logger }

// Open is the filesystem rooted at dir and dir's canonical path. core/todo closes what it opens.
func (d TodoDisk) Open(_ context.Context, dir string) (vfs.FS, string, error) {
	if !filepath.IsAbs(dir) {
		return nil, "", fmt.Errorf("daemon: todos: %s is not an absolute path: %w", dir, vfs.ErrInvalidName)
	}
	canon, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("daemon: todos: %s: %w", dir, vfs.ErrNotExist)
	}
	if err != nil {
		return nil, "", err
	}
	opts := []local.Option{}
	if d.Log != nil {
		opts = append(opts, local.WithLogger(d.Log))
	}
	fsys, err := local.New(canon, opts...)
	if err != nil {
		return nil, "", err
	}
	return fsys, canon, nil
}
