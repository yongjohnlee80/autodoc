package todo

import (
	"context"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// OS PATHS IN, SLASH PATHS WITHIN. core/todo keeps every absolute path in one form, slash-separated
// and clean (paths.Slash: "C:/Users/u/kb" on Windows), so it can compare, join and store them with
// package path. A path comes in in the OS's own form from three places, and each is turned into
// that form there: the canonical path the disk answers (slashDisk), a workspace's root (canonRoots,
// through the disk too), and a path a caller gives (each verb, through paths.Slash, after
// paths.IsAbs accepts it). Paths are compared exactly: which paths name one place is the disk's
// answer, never a lexical one.

// slashDisk is a Filesystems whose canonical paths are in the form core/todo keeps.
type slashDisk struct{ Filesystems }

func (d slashDisk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	fsys, canon, err := d.Filesystems.Open(ctx, dir)
	return fsys, paths.Slash(canon), err
}

// canonRoots is a Roots whose workspace roots are canonical: each opened through the disk, which
// answers a root's canonical path as it answers a store's (on Windows its long names in the disk's
// own case), in the slash form. So core/todo compares a root with a store's path exactly, and
// identity is the filesystem's, never a lexical match: in a case-sensitive folder, KB is not kb. A
// root the disk cannot open (gone, unreadable) is given as configured, in the slash form, and then
// matches no store's canonical path.
type canonRoots struct {
	Roots
	disk Filesystems // nil: the roots are only put in the slash form
}

func (r canonRoots) Root(workspace string) (string, bool) {
	root, ok := r.Roots.Root(workspace)
	if !ok || root == "" || r.disk == nil {
		return paths.Slash(root), ok
	}
	fsys, canon, err := r.disk.Open(context.Background(), root)
	if err != nil {
		return paths.Slash(root), true
	}
	fsys.Close()
	return canon, true
}
