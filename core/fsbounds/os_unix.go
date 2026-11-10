//go:build unix

package fsbounds

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// links is the hard-link count of the file at path, fi its Lstat.
func links(_ string, fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// isLink reports whether fi, an Lstat, is a link the walk resolves: a symbolic link.
func isLink(fi fs.FileInfo) bool { return fi.Mode()&fs.ModeSymlink != 0 }

// realPath is an existing path as the file system names it. On Unix the walk has resolved every
// link on the way, so it is p.
func realPath(p string) (string, error) { return p, nil }

// resolve is the link at p resolved, all the way.
func resolve(p string) (string, error) { return filepath.EvalSymlinks(p) }
