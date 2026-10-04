// Package docs reads and writes a workspace's files for AutoDoc's own apps (ADR 0203 §4.6). Every
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

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// MaxSize is the largest document read or written: the RPC message limit (4 MiB), less room for
// the message around it. A larger file is refused, never truncated. A derived document's text is
// the one exception: it is cut to fit, and says so (Read).
const MaxSize = 4<<20 - 64<<10

var (
	// ErrCommitted is a write that landed, then a follow-up step failed (Committed, -32066): the
	// change took effect but no version can be promised. The client reads the document and compares;
	// it never sends the write again.
	ErrCommitted = errors.New("docs: the write landed, then a follow-up step failed")

	// ErrNotEligible is a path the workspace does not index (outside include, inside exclude):
	// AutoDoc's apps edit files, not every file under the root.
	ErrNotEligible = errs.Sentinel(errs.ErrInvalidArgument, "docs: not a file of this workspace")

	// ErrTooLarge is a document over MaxSize, or a derived document's file over derived.MaxContainer.
	ErrTooLarge = errs.Sentinel(errs.ErrInvalidArgument, "docs: the document is over the size limit")

	// ErrReadOnly is a write, a rename or a removal of a derived document: AutoDoc serves its
	// text, and never changes the file it was made of.
	ErrReadOnly = errs.Sentinel(errs.ErrInvalidArgument, "docs: a derived document is read-only")
)

// Docs is the document API over one workspace root.
type Docs struct {
	fsys     vfs.FS
	eligible func(path string) bool // the workspace's include and exclude; nil: every path
	text     func() []string        // the workspace's own plain-text extensions; nil: none
	kinds    kind.Registrations     // the build's registered extensions (ADR 0216)
	reg      *registrations.Table   // the build's deriver, for its formats' text; nil: none
}

// Option configures Docs.
type Option func(*Docs)

// WithTextExtensions reads the workspace's own plain-text extensions (core/kind) from text, asked
// at each read and write, so a change applies without a restart.
func WithTextExtensions(text func() []string) Option { return func(d *Docs) { d.text = text } }

// WithRegistrations reads files of the build's registered extensions as their kind (kind.Registered:
// UTF-8 text, validated as such on a write).
func WithRegistrations(r kind.Registrations) Option { return func(d *Docs) { d.kinds = r } }

// WithDeriver reads the formats reg derives as their derived text, read-only.
func WithDeriver(reg *registrations.Table) Option { return func(d *Docs) { d.reg = reg } }

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
	return d.kinds.Of(path, text)
}

// Doc is a document's content at a version.
type Doc struct {
	Content []byte
	Version vfs.Version
}

// derives reports whether path is a format the build derives.
func (d *Docs) derives(path string) bool {
	_, ok := d.reg.Format(kind.Ext(path))
	return ok && d.kindOf(path) == kind.Pro
}

// check refuses a path the workspace does not index, a Pro format the build does not derive, and
// a change (write) to one it does.
func (d *Docs) check(path string, write bool) error {
	if d.kindOf(path) == kind.Pro && !d.derives(path) {
		return fmt.Errorf("%w: %s", ErrNotEligible, path)
	}
	if d.eligible != nil && !d.eligible(path) {
		return fmt.Errorf("%w: %s", ErrNotEligible, path)
	}
	if write && d.derives(path) {
		return fmt.Errorf("%w: %s", ErrReadOnly, path)
	}
	return nil
}

// Read returns the document at path with the version its content was read at. The file is stat'ed
// before and after the read, and read again when it changed in between, so the version never names
// other bytes than the ones returned.
func (d *Docs) Read(ctx context.Context, path string) (Doc, error) {
	if err := d.check(path, false); err != nil {
		return Doc{}, err
	}
	if d.derives(path) {
		return d.readDerived(ctx, path)
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
	if err := d.check(path, true); err != nil {
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
		// a new file may start a new folder
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
	if err := d.check(from, true); err != nil {
		return err
	}
	if err := d.check(to, true); err != nil {
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
	if err := d.check(path, true); err != nil {
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

// readDerived returns a derived document's text with the version of the file it was made of,
// stat'ed before and after as Read does. Text over MaxSize is cut to fit one message (excerpt).
func (d *Docs) readDerived(ctx context.Context, path string) (Doc, error) {
	for attempt := 0; ; attempt++ {
		before, err := d.fsys.Stat(ctx, path)
		if err != nil {
			return Doc{}, err
		}
		if !before.IsRegular() {
			return Doc{}, fmt.Errorf("docs: %s: %w", path, vfs.ErrNotExist)
		}
		if before.Size > derived.MaxContainer {
			return Doc{}, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, path, before.Size)
		}
		text, err := derived.Text(ctx, d.reg, d.fsys, path, before.Size)
		if err != nil {
			return Doc{}, err
		}
		content, err := excerpt(text, kind.Label(path))
		if err != nil {
			return Doc{}, err
		}
		after, err := d.fsys.Stat(ctx, path)
		if err != nil {
			return Doc{}, err
		}
		if after.Version == before.Version {
			if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
				return Doc{}, fmt.Errorf("%w: %s: its derived text is not UTF-8", derived.ErrDerive, path)
			}
			return Doc{Content: content, Version: after.Version}, nil
		}
		if attempt == 4 {
			return Doc{}, fmt.Errorf("docs: %s keeps changing while it is read: %w", path, errs.ErrTimeout)
		}
	}
}

// excerpt reads a derived text whole when it fits in MaxSize. A longer one is cut at a clean place
// within the room left for its label: the last blank line in the second half of that room, else
// the last line break, else the last whole character. The label says how much was shown of how
// much, the whole from the text's stated size (Derived.Bytes), and where the rest is.
func excerpt(t registrations.Derived, label string) ([]byte, error) {
	defer t.Text.Close()
	b, err := io.ReadAll(io.LimitReader(t.Text, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) <= MaxSize {
		return b, nil
	}
	note := func(shown int) string {
		whole := fmt.Sprintf("the %d bytes", t.Bytes)
		if t.Bytes <= int64(len(b)) {
			whole = fmt.Sprintf("more than %d bytes", MaxSize) // its stated size was short
		}
		return fmt.Sprintf("\n\n---\n\n> Truncated: this is the first %d of %s of text derived from this %s, "+
			"as much as one read carries. Open the original in its system viewer for the rest.\n", shown, whole, label)
	}
	room := MaxSize - len(note(MaxSize))
	cut := cleanCut(b, room)
	return append(b[:cut:cut], note(cut)...), nil
}

// cleanCut is where to cut b, longer than room, within room bytes.
func cleanCut(b []byte, room int) int {
	w := b[:room]
	if i := bytes.LastIndex(w, []byte("\n\n")); i >= room/2 {
		return i
	}
	if i := bytes.LastIndexByte(w, '\n'); i > 0 {
		return i
	}
	for n := room; n > room-utf8.UTFMax && n > 0; n-- {
		if utf8.RuneStart(b[n]) {
			return n
		}
	}
	return room
}
