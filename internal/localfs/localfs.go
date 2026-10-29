// Package localfs writes a file of the local disk the way core writes a workspace's: atomically,
// through golib's vfs/local, so a reader never sees half of it. It is for the programs' own files
// outside every workspace (an export's destination, a preview's page); core/ never imports it.
// Resolve is where a local path really is, for asking whether one path is under another.
//
// Concurrency: WriteFile and Resolve may be called from any goroutine; two writes of one path end
// with one of them whole.
package localfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
)

// WriteFile replaces the file at path with content, atomically: a temporary file beside it,
// renamed over it. path's directory must exist. Under a gated script's bounds it writes nothing:
// a bounded call reaches files only through a workspace's guarded filesystem (ADR 1792004705
// §4.2a.1), so a route here is one the guard never checked.
func WriteFile(ctx context.Context, path string, content []byte) error {
	if path == "" {
		return errors.New("localfs: no file to write")
	}
	if _, bounded := fsbounds.From(ctx); bounded {
		return fsbounds.Outside(path)
	}
	dir, err := local.New(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("localfs: %s: %w", filepath.Dir(path), err)
	}
	_, err = dir.WriteFile(ctx, filepath.Base(path), bytes.NewReader(content))
	return errors.Join(err, dir.Close())
}

// Resolve is abs with its symlinks resolved, as far as it exists: a file not there yet (a write
// that creates it, its folders too) keeps the names of what does not exist under the resolved
// folder that does.
func Resolve(abs string) string {
	var missing []string
	p := abs
	for ; p != filepath.Dir(p); p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, missing...)...)
		}
		missing = append([]string{filepath.Base(p)}, missing...)
	}
	return filepath.Join(append([]string{p}, missing...)...) // the root, which is itself
}
