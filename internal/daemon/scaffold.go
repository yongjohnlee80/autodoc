package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// Scaffold creates a KB type's files in workspace name (workspace.scaffold, ADR 1792349867 §4.3).
//
// It writes through the workspace's own filesystem, golib vfs/local, so every name resolves
// through os.Root under the root: neither ".." nor a symlinked folder inside the KB reaches
// outside it. It doesn't ask the workspace's matcher, so a file the workspace doesn't index is
// still created, and one it does index reaches the index through the follower. A plain file is
// created exclusively: one that exists is kept. Only the root KB_OPERATIONS.md, marked managed,
// may replace a copy, and only one whose declared autodoc_version parses and is older than the
// incoming copy's. An immutable folder's descriptor is skipped when that folder holds anything.
//
// A file's refusal is its result. The call's error is for what stops it whole: no such workspace,
// too many files, or ctx ending, when the results so far are returned with it.
//
// It holds m.chg for the call, so no restart closes the filesystem under it; the filesystem is
// borrowed from the served workspace and never closed here.
func (m *Workspaces) Scaffold(ctx context.Context, name string, files []rpc.ScaffoldFile) ([]rpc.ScaffoldResult, error) {
	if len(files) > rpc.MaxScaffoldFiles {
		return nil, fmt.Errorf("%w: at most %d files a call", errs.ErrInvalidArgument, rpc.MaxScaffoldFiles)
	}
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	case s.fsys == nil:
		return nil, fmt.Errorf("%w: %s is not open: %v", errs.ErrUnsupported, name, s.err)
	}
	out := make([]rpc.ScaffoldResult, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out = append(out, scaffoldOne(ctx, s.fsys, f))
	}
	return out, nil
}

func scaffoldOne(ctx context.Context, fsys vfs.FS, f rpc.ScaffoldFile) rpc.ScaffoldResult {
	r := rpc.ScaffoldResult{Path: f.Path}
	refuse := func(why string) rpc.ScaffoldResult {
		r.Outcome, r.Err = rpc.ScaffoldRefused, why
		return r
	}
	switch {
	case !kbtype.ValidPath(f.Path):
		return refuse("not a clean relative path")
	case len(f.Content) > kbtype.MaxFileSize:
		return refuse(fmt.Sprintf("over %d bytes", kbtype.MaxFileSize))
	case !kind.Textual(f.Content):
		return refuse("not UTF-8 text")
	}
	dir := path.Dir(f.Path)
	if f.Immutable != "" {
		// the mark must be this file's own folder's, and the file that folder's descriptor: a caller
		// cannot skip, or reach, another folder by naming it
		if f.Immutable != dir || f.Path != kbtype.Descriptor(dir) {
			return refuse("immutable names a folder this file is not the descriptor of")
		}
		entries, err := fsys.ReadDir(ctx, dir)
		switch {
		case err == nil && len(entries) > 0:
			r.Outcome = rpc.ScaffoldSkipped
			return r
		case err != nil && !errors.Is(err, vfs.ErrNotExist):
			return refuse(publicReason(err))
		}
	}
	if f.Managed && f.Path == kbtype.Operations {
		return scaffoldManaged(ctx, fsys, f, r)
	}
	return scaffoldCreate(ctx, fsys, f, r)
}

// scaffoldCreate creates f exclusively, its folders first.
func scaffoldCreate(ctx context.Context, fsys vfs.FS, f rpc.ScaffoldFile, r rpc.ScaffoldResult) rpc.ScaffoldResult {
	c, ok := fsys.(vfs.ExclusiveCreator)
	if !ok {
		r.Outcome, r.Err = rpc.ScaffoldRefused, "the workspace's driver cannot create exclusively"
		return r
	}
	if dir := path.Dir(f.Path); dir != "." {
		if err := fsys.MkdirAll(ctx, dir); err != nil {
			r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
			return r
		}
	}
	_, err := c.CreateExclusive(ctx, f.Path, bytes.NewReader(f.Content))
	switch {
	case err == nil:
		r.Outcome = rpc.ScaffoldCreated
	case errors.Is(err, vfs.ErrConflict):
		r.Outcome = rpc.ScaffoldKept
	default:
		r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
	}
	return r
}

// scaffoldManaged creates the root KB_OPERATIONS.md, or replaces a copy at the version read, only
// when the copy's declared version parses and is older than f's. Anything else is kept.
func scaffoldManaged(ctx context.Context, fsys vfs.FS, f rpc.ScaffoldFile, r rpc.ScaffoldResult) rpc.ScaffoldResult {
	fi, err := fsys.Stat(ctx, f.Path)
	if errors.Is(err, vfs.ErrNotExist) {
		return scaffoldCreate(ctx, fsys, f, r)
	}
	if err != nil {
		r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
		return r
	}
	rc, err := fsys.Open(ctx, f.Path, 0)
	if err != nil {
		r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
		return r
	}
	have, err := io.ReadAll(io.LimitReader(rc, kbtype.MaxFileSize+1))
	_ = rc.Close()
	if err != nil {
		r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
		return r
	}
	older, ok := kbtype.Older(kbtype.DeclaredVersion(have), kbtype.DeclaredVersion(f.Content))
	if !ok || !older {
		r.Outcome = rpc.ScaffoldKept // unreadable, unparsable, the same or newer: kept
		return r
	}
	w, ok := fsys.(vfs.ConditionalWriter)
	if !ok {
		r.Outcome = rpc.ScaffoldKept
		return r
	}
	_, err = w.WriteFileIf(ctx, f.Path, bytes.NewReader(f.Content), fi.Version)
	switch {
	case err == nil:
		r.Outcome = rpc.ScaffoldUpdated
	case errors.Is(err, vfs.ErrConflict):
		r.Outcome = rpc.ScaffoldKept // changed since it was read: the newer edit stands
	default:
		r.Outcome, r.Err = rpc.ScaffoldRefused, publicReason(err)
	}
	return r
}

// publicReason is what a file's refusal says on the wire: the kind of failure, never a driver's
// own text, which may name a path outside the workspace.
func publicReason(err error) string {
	switch {
	case errors.Is(err, vfs.ErrNotExist):
		return "not found"
	case errors.Is(err, vfs.ErrConflict):
		return "changed meanwhile"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, errs.ErrInvalidArgument):
		return "refused by the workspace's filesystem"
	}
	return "could not be written"
}
