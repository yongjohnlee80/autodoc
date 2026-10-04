// Package docs reads and writes a workspace's notes for AutoDoc's own apps (ADR 0203 §4.6). Every
// write goes through the workspace's vfs, conditional on the version the writer read, so the index
// learns of it the same way it learns of an external edit, and no writer replaces what it has not
// seen.
package docs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	pathpkg "path"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// MaxSize is the largest document read or written: the RPC message limit (4 MiB), less room for
// the message around it. A larger one is refused, never truncated.
const MaxSize = 4<<20 - 64<<10

var (
	// ErrCommitted is a write that landed, then a follow-up step failed (Committed, -32066): the
	// change took effect but no version can be promised. The client reads the document and compares;
	// it never sends the write again.
	ErrCommitted = errors.New("docs: the write landed, then a follow-up step failed")

	// ErrNotEligible is a path the workspace does not index (outside include, inside exclude):
	// AutoDoc's apps edit notes, not every file under the root.
	ErrNotEligible = errs.Sentinel(errs.ErrInvalidArgument, "docs: not a note of this workspace")

	// ErrTooLarge is a document over MaxSize.
	ErrTooLarge = errs.Sentinel(errs.ErrInvalidArgument, "docs: the document is over the size limit")
)

// Docs is the document API over one workspace root.
type Docs struct {
	fsys     vfs.FS
	eligible func(path string) bool // the workspace's include and exclude; nil: every path
	text     func() []string        // the workspace's own plain-text extensions; nil: none
}

// Option configures Docs.
type Option func(*Docs)

// WithTextExtensions reads the workspace's own plain-text extensions (core/kind) from text, asked
// at each read and write, so a change applies without a restart.
func WithTextExtensions(text func() []string) Option { return func(d *Docs) { d.text = text } }

// New returns the documents of fsys that eligible admits.
func New(fsys vfs.FS, eligible func(path string) bool, opts ...Option) *Docs {
	d := &Docs{fsys: fsys, eligible: eligible}
	for _, o := range opts {
		o(d)
	}
	return d
}

func (d *Docs) kindOf(path string) kind.Kind {
	var text []string
	if d.text != nil {
		text = d.text()
	}
	return kind.Of(path, text)
}

// Doc is a document's content at a version.
type Doc struct {
	Content []byte
	Version vfs.Version
}

func (d *Docs) check(path string) error {
	if d.kindOf(path) == kind.Pro {
		return fmt.Errorf("%w: %s", ErrNotEligible, path)
	}
	if d.eligible != nil && !d.eligible(path) {
		return fmt.Errorf("%w: %s", ErrNotEligible, path)
	}
	return nil
}

// Read returns the document at path with the version its content was read at. The file is stat'ed
// before and after the read, and read again when it changed in between, so the version never names
// other bytes than the ones returned.
func (d *Docs) Read(ctx context.Context, path string) (Doc, error) {
	if err := d.check(path); err != nil {
		return Doc{}, err
	}
	for attempt := 0; ; attempt++ {
		before, err := d.fsys.Stat(ctx, path)
		if err != nil {
			return Doc{}, err
		}
		if !before.IsRegular() {
			return Doc{}, fmt.Errorf("docs: %s: %w", path, vfs.ErrNotExist)
		}
		if before.Size > MaxSize {
			return Doc{}, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, path, before.Size)
		}
		r, err := d.fsys.Open(ctx, path, 0)
		if err != nil {
			return Doc{}, err
		}
		content, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
		_ = r.Close()
		if err != nil {
			return Doc{}, err
		}
		after, err := d.fsys.Stat(ctx, path)
		if err != nil {
			return Doc{}, err
		}
		if after.Version == before.Version && len(content) <= MaxSize {
			if d.kindOf(path).IsText() && (!utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0) {
				return Doc{}, fmt.Errorf("%w: %s is not UTF-8 text", ErrNotEligible, path)
			}
			return Doc{Content: content, Version: after.Version}, nil
		}
		if attempt == 4 {
			return Doc{}, fmt.Errorf("docs: %s keeps changing while it is read: %w", path, errs.ErrTimeout)
		}
	}
}

// Write writes content to path if the document is still at version want, and returns the new
// version. want "" creates a document that must not exist yet, and the folders above it. A stale want, or an existing path
// for a create, fails with a *vfs.ConflictError and leaves the file as it is.
func (d *Docs) Write(ctx context.Context, path string, content []byte, want vfs.Version) (vfs.Version, error) {
	if err := d.check(path); err != nil {
		return "", err
	}
	if len(content) > MaxSize {
		return "", fmt.Errorf("%w: %d bytes", ErrTooLarge, len(content))
	}
	if d.kindOf(path).IsText() && (!utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0) {
		return "", fmt.Errorf("%w: %s is not UTF-8 text", ErrNotEligible, path)
	}
	var fi vfs.FileInfo
	var err error
	if want == "" {
		c, ok := d.fsys.(vfs.ExclusiveCreator)
		if !ok {
			return "", fmt.Errorf("docs: creating %s: %w", path, errs.ErrUnsupported)
		}
		// a new note may start a new folder
		if dir := pathpkg.Dir(path); dir != "." {
			if err := d.fsys.MkdirAll(ctx, dir); err != nil {
				return "", err
			}
		}
		fi, err = c.CreateExclusive(ctx, path, bytes.NewReader(content))
	} else {
		c, ok := d.fsys.(vfs.ConditionalWriter)
		if !ok {
			return "", fmt.Errorf("docs: writing %s: %w", path, errs.ErrUnsupported)
		}
		fi, err = c.WriteFileIf(ctx, path, bytes.NewReader(content), want)
	}
	return committed(fi.Version, err)
}

// Rename moves a document to a path where nothing is yet.
func (d *Docs) Rename(ctx context.Context, from, to string) error {
	if err := d.check(from); err != nil {
		return err
	}
	if err := d.check(to); err != nil {
		return err
	}
	c, ok := d.fsys.(vfs.NoReplaceRenamer)
	if !ok {
		return fmt.Errorf("docs: renaming %s: %w", from, errs.ErrUnsupported)
	}
	_, err := committed("", c.RenameNoReplace(ctx, from, to))
	return err
}

// Remove deletes the document at path if it is still at version want.
func (d *Docs) Remove(ctx context.Context, path string, want vfs.Version) error {
	if err := d.check(path); err != nil {
		return err
	}
	c, ok := d.fsys.(vfs.ConditionalWriter)
	if !ok {
		return fmt.Errorf("docs: removing %s: %w", path, errs.ErrUnsupported)
	}
	_, err := committed("", c.RemoveIf(ctx, path, want))
	return err
}

// committed turns a failure after the commit point into ErrCommitted, with no version: the driver
// may not know the new one, and a stale one would send the client's next write into a conflict it
// cannot explain.
func committed(v vfs.Version, err error) (vfs.Version, error) {
	if errors.Is(err, vfs.ErrCommitted) {
		return "", fmt.Errorf("%w: %v", ErrCommitted, err)
	}
	return v, err
}
