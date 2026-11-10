package todo

import (
	"context"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// OS PATHS IN, SLASH PATHS WITHIN. core/todo keeps every absolute path in one form, slash-separated
// and clean (paths.Slash: "C:/Users/u/kb" on Windows), so it can compare, join and store them with
// package path. A path comes in in the OS's own form from three places, and each is turned into
// that form there: the canonical path the disk answers (slashDisk), a workspace's root (slashRoots),
// and a path a caller gives (each verb, through paths.Slash, after paths.IsAbs accepts it).

// slashDisk is a Filesystems whose canonical paths are in the form core/todo keeps.
type slashDisk struct{ Filesystems }

func (d slashDisk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	fsys, canon, err := d.Filesystems.Open(ctx, dir)
	return fsys, paths.Slash(canon), err
}

// slashRoots is a Roots whose workspace roots are in the form core/todo keeps.
type slashRoots struct{ Roots }

func (r slashRoots) Root(workspace string) (string, bool) {
	root, ok := r.Roots.Root(workspace)
	return paths.Slash(root), ok
}
