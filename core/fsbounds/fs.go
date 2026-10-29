package fsbounds

import (
	"context"
	"io"

	"github.com/yongjohnlee80/golib/vfs"
)

// Wrap is fsys, the files of workspace name() at root dir, behind g: an operation whose ctx carries
// Bounds runs only when every name it is given passes g's check; any other is fsys's own, unchanged.
// The answer has exactly fsys's capabilities (ConditionalWriter, ExclusiveCreator, NoReplaceRenamer,
// Copier, Watcher), each checked the same way, and passes versions and commit errors through as
// fsys gives them. name is a function: a workspace renamed keeps its files.
func (g *Guard) Wrap(name func() string, dir string, fsys vfs.FS) vfs.FS {
	b := &boundedFS{fsys: fsys, g: g, name: name, dir: dir}
	cw, _ := fsys.(vfs.ConditionalWriter)
	ec, _ := fsys.(vfs.ExclusiveCreator)
	nr, _ := fsys.(vfs.NoReplaceRenamer)
	cp, _ := fsys.(vfs.Copier)
	wa, _ := fsys.(vfs.Watcher)
	return compose(b, cw, ec, nr, cp, wa)
}

// boundedFS is vfs.FS checked against the ctx's Bounds.
type boundedFS struct {
	fsys vfs.FS
	g    *Guard
	name func() string
	dir  string
}

// check is nil for the user's ctx, else g's answer for each of names.
func (b *boundedFS) check(ctx context.Context, o op, names ...string) error {
	bounds, ok := From(ctx)
	if !ok {
		return nil
	}
	ws := Root{Name: b.name(), Dir: b.dir}
	for _, n := range names {
		if err := b.g.check(ws, n, bounds, o); err != nil {
			return err
		}
	}
	return nil
}

func (b *boundedFS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	if err := b.check(ctx, opRead, name); err != nil {
		return vfs.FileInfo{}, err
	}
	return b.fsys.Stat(ctx, name)
}

func (b *boundedFS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	if err := b.check(ctx, opRead, name); err != nil {
		return nil, err
	}
	return b.fsys.ReadDir(ctx, name)
}

func (b *boundedFS) Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	if err := b.check(ctx, opRead, name); err != nil {
		return nil, err
	}
	return b.fsys.Open(ctx, name, offset)
}

func (b *boundedFS) WriteFile(ctx context.Context, name string, r io.Reader, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := b.check(ctx, opWrite, name); err != nil {
		return vfs.FileInfo{}, err
	}
	return b.fsys.WriteFile(ctx, name, r, opts...)
}

func (b *boundedFS) MkdirAll(ctx context.Context, name string) error {
	if err := b.check(ctx, opWrite, name); err != nil {
		return err
	}
	return b.fsys.MkdirAll(ctx, name)
}

func (b *boundedFS) Remove(ctx context.Context, name string) error {
	if err := b.check(ctx, opWrite, name); err != nil {
		return err
	}
	return b.fsys.Remove(ctx, name)
}

func (b *boundedFS) RemoveAll(ctx context.Context, name string) error {
	if err := b.check(ctx, opTree, name); err != nil {
		return err
	}
	return b.fsys.RemoveAll(ctx, name)
}

func (b *boundedFS) Rename(ctx context.Context, from, to string) error {
	if err := b.check(ctx, opTree, from); err != nil {
		return err
	}
	if err := b.check(ctx, opWrite, to); err != nil {
		return err
	}
	return b.fsys.Rename(ctx, from, to)
}

func (b *boundedFS) Close() error { return b.fsys.Close() }

// the capabilities, each checked as the method it extends

type conditional struct {
	b  *boundedFS
	cw vfs.ConditionalWriter
}

func (c conditional) WriteFileIf(ctx context.Context, name string, r io.Reader, want vfs.Version, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := c.b.check(ctx, opWrite, name); err != nil {
		return vfs.FileInfo{}, err
	}
	return c.cw.WriteFileIf(ctx, name, r, want, opts...)
}

func (c conditional) RemoveIf(ctx context.Context, name string, want vfs.Version) error {
	if err := c.b.check(ctx, opWrite, name); err != nil {
		return err
	}
	return c.cw.RemoveIf(ctx, name, want)
}

type exclusive struct {
	b  *boundedFS
	ec vfs.ExclusiveCreator
}

func (e exclusive) CreateExclusive(ctx context.Context, name string, r io.Reader, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := e.b.check(ctx, opWrite, name); err != nil {
		return vfs.FileInfo{}, err
	}
	return e.ec.CreateExclusive(ctx, name, r, opts...)
}

type noReplace struct {
	b  *boundedFS
	nr vfs.NoReplaceRenamer
}

func (n noReplace) RenameNoReplace(ctx context.Context, from, to string) error {
	if err := n.b.check(ctx, opTree, from); err != nil {
		return err
	}
	if err := n.b.check(ctx, opWrite, to); err != nil {
		return err
	}
	return n.nr.RenameNoReplace(ctx, from, to)
}

type copier struct {
	b  *boundedFS
	cp vfs.Copier
}

func (c copier) Copy(ctx context.Context, from, to string, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := c.b.check(ctx, opCopy, from); err != nil {
		return vfs.FileInfo{}, err
	}
	if err := c.b.check(ctx, opWrite, to); err != nil {
		return vfs.FileInfo{}, err
	}
	return c.cp.Copy(ctx, from, to, opts...)
}

type watcher struct {
	b  *boundedFS
	wa vfs.Watcher
}

func (w watcher) Watch(ctx context.Context, dir string, opts ...vfs.WatchOption) (<-chan vfs.Event, error) {
	if err := w.b.check(ctx, opRead, dir); err != nil {
		return nil, err
	}
	return w.wa.Watch(ctx, dir, opts...)
}
