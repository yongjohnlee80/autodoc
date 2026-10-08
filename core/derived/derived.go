// Package derived makes a derived document's text through the build's deriver, for the indexer and the
// document API alike: the file is read at offsets, a cache's eviction miss is retried once, and a
// text made under another identity than the frozen one is refused.
//
// Three bounds apply to a derived document, each its own: MaxContainer bounds the file on disk, so a
// 50 MB PDF that is mostly images is read; MaxText bounds the text the indexer indexes; and the
// document API's own limit (core/docs) bounds what one RPC message carries. The deriver may bound
// its text too (autorag's MaxText); that bound is the deriver's.
package derived

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// MaxContainer is the largest file a deriver is handed. A PDF or a DOCX is a container: most of a
// large one is images and fonts, so it gets its own bound, far above the 16 MiB a text file may be.
const MaxContainer = 250 << 20

// MaxText is the largest derived text the indexer reads: about three million words.
const MaxText = 16 << 20

var (
	// ErrNoDeriver is a path whose format the build does not derive.
	ErrNoDeriver = errs.Sentinel(errs.ErrInvalidArgument, "derived: this build derives no such format")
	// ErrTooLarge is derived text over the reader's bound.
	ErrTooLarge = errs.Sentinel(errs.ErrInvalidArgument, "derived: the text is over the size limit")
	// ErrDeriverFailed is the deriver's own failure to make a document's text: an error other than
	// a miss, a second miss, a panic, or no text. It may pass (a busy lease, a full cache disk), so
	// it is the document's error for now, tried again; it never says what the file holds.
	ErrDeriverFailed = errors.New("derived: the deriver could not make the text")
	// ErrRefused is a text the deriver delivered that AutoDoc refuses, which the same bytes would
	// deliver again: made under another identity than the frozen one, or a negative stated size.
	ErrRefused = errors.New("derived: the derived text is refused")
)

// Text derives the text of path, a file of size bytes in fsys, with reg's deriver. The text is the
// caller's to close; it holds the file open until then. A deriver's error matching fs.ErrNotExist
// is a cache's eviction miss: the text went between its making and its reading, so Derive is called
// once more, and a second miss is ErrDeriverFailed. The deriver's other failures are
// ErrDeriverFailed; a text it delivers whose identity is not the frozen one for its format is
// ErrRefused. A file gone before it could be opened is returned as it is.
func Text(ctx context.Context, reg *registrations.Table, fsys vfs.FS, path string, size int64) (registrations.Derived, error) {
	format := kind.Ext(path)
	d := reg.Deriver()
	if _, ok := reg.Format(format); !ok || d == nil {
		return registrations.Derived{}, fmt.Errorf("%w: %s", ErrNoDeriver, path)
	}
	var out registrations.Derived
	var err error
	for attempt := 0; ; attempt++ {
		var miss bool
		out, miss, err = derive(ctx, d, fsys, path, size)
		if !miss || ctx.Err() != nil {
			break
		}
		if attempt == 1 {
			err = fmt.Errorf("%w: %s: its text was evicted twice: %v", ErrDeriverFailed, path, err)
			break
		}
	}
	if err != nil {
		return registrations.Derived{}, err
	}
	if err := reg.CheckDerived(format, out); err != nil {
		_ = out.Text.Close()
		return registrations.Derived{}, fmt.Errorf("%w: %s: %v", ErrRefused, path, err)
	}
	if out.Bytes < 0 {
		_ = out.Text.Close()
		return registrations.Derived{}, fmt.Errorf("%w: %s: the deriver gave its text's size as %d", ErrRefused, path, out.Bytes)
	}
	return out, nil
}

// derive calls d once, and reports whether its error is a miss. The deriver is another module's
// code: a panic is its failure, as an error is. A failure to open the file is returned as it is, so
// a file gone is seen as gone.
func derive(ctx context.Context, d registrations.Deriver, fsys vfs.FS, path string, size int64) (out registrations.Derived, miss bool, err error) {
	src, err := fsys.Open(ctx, path, 0)
	if err != nil {
		return registrations.Derived{}, false, err
	}
	r, ok := src.(io.ReaderAt) // a local file is one; another driver is read at offsets
	if !ok {
		r = offsets{ctx: ctx, fsys: fsys, name: path}
	}
	defer func() {
		if p := recover(); p != nil {
			out, miss, err = registrations.Derived{}, false, fmt.Errorf("%w: %s: the deriver panicked: %v", ErrDeriverFailed, path, p)
		}
		if err != nil {
			if out.Text != nil {
				_ = out.Text.Close()
			}
			_ = src.Close()
		}
	}()
	out, err = d.Derive(ctx, path, r, size)
	switch {
	case ctx.Err() != nil && err != nil:
		return out, false, err
	case errors.Is(err, fs.ErrNotExist):
		return out, true, err
	case err != nil:
		return out, false, fmt.Errorf("%w: %s: %v", ErrDeriverFailed, path, err)
	case out.Text == nil:
		return out, false, fmt.Errorf("%w: %s: the deriver gave no text", ErrDeriverFailed, path)
	}
	out.Text = closing{ReadCloser: out.Text, src: src}
	return out, false, nil
}

// closing is a derived text that closes the file it was made of with itself.
type closing struct {
	io.ReadCloser
	src io.Closer
}

func (c closing) Close() error {
	err := c.ReadCloser.Close()
	if serr := c.src.Close(); err == nil {
		err = serr
	}
	return err
}

// offsets reads a file at offsets through a driver that streams it from one.
type offsets struct {
	ctx  context.Context
	fsys vfs.FS
	name string
}

func (o offsets) ReadAt(p []byte, off int64) (int, error) {
	r, err := o.fsys.Open(o.ctx, o.name, off)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	n, err := io.ReadFull(r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}

// Read reads d's text whole, at most limit bytes, and closes it. Text whose Bytes is over limit is
// refused before anything is read, and so is text that turns out longer than limit.
func Read(d registrations.Derived, limit int64) ([]byte, error) {
	defer d.Text.Close()
	if d.Bytes > limit {
		return nil, fmt.Errorf("%w: %d bytes of text, over %d", ErrTooLarge, d.Bytes, limit)
	}
	b, err := io.ReadAll(io.LimitReader(d.Text, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: more than %d bytes of text", ErrTooLarge, limit)
	}
	return b, nil
}
