package registrations

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/yongjohnlee80/golib/extract"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// Deriver makes text of a build's document formats (ADR 0216 §1.8): every build has Documents, and
// a Pro build adds one that adapts autorag's derive.Cache. The indexer and doc.read call them
// through core/derived, as one (byFormat).
type Deriver interface {
	// Formats are the formats it reads: lower-case extensions with their dot, each a derived format.
	Formats() []string
	// Describe is the identity format's text is made under, known without extracting. It is read
	// once, at entry, and frozen.
	Describe(format string) (id, version string)
	// Derive makes the text of the file name, of size bytes, read through r. An error matching
	// fs.ErrNotExist is a cache's eviction miss (autorag's derive.ErrMiss is one): the text went
	// between its making and its reading, and AutoDoc calls Derive once more.
	Derive(ctx context.Context, name string, r io.ReaderAt, size int64) (Derived, error)
}

// Derived is one file's text.
type Derived struct {
	Text  io.ReadCloser
	Bytes int64 // the text's size, known before it is read
	Info  extract.Info
	// ID and Version are the identity the text was made under: a Derived whose identity is not
	// the frozen one for its format is refused (CheckDerived).
	ID, Version string
}

// addDeriver validates d and freezes its formats into t: each a derived format, not a registered
// chunker's, described once by an id (ValidDeriverID) and a version (ValidVersion).
func (t *Table) addDeriver(d Deriver) error {
	formats := d.Formats()
	if len(formats) == 0 {
		return &Error{"deriver", "it reads no format"}
	}
	for _, f := range formats {
		switch {
		case !kind.ValidExtension(f):
			return &Error{f, "a format is a dot and 1 to 16 lower-case letters, digits, _, + or -"}
		case !slices.Contains(kind.DerivedExtensions, f):
			return &Error{f, "not a derived document format"}
		}
		if _, dup := t.formats[f]; dup {
			return &Error{f, "named twice"}
		}
		id, version := d.Describe(f)
		switch {
		case !ValidDeriverID(id):
			return &Error{f, fmt.Sprintf("deriver id %q: 1 to 64 letters, digits, '.', '_', '+', '-' or '/'", id)}
		case !ValidVersion(version):
			return &Error{f, fmt.Sprintf("deriver version %q: 1 to 32 letters, digits, '.', '_', '+' or '-'", version)}
		}
		t.formats[f] = Format{ID: id, Version: version}
		t.byFormat[f] = d
	}
	return nil
}

// Format is the frozen identity of format's text, and whether the build derives it.
func (t *Table) Format(format string) (Format, bool) {
	if t == nil {
		return Format{}, false
	}
	f, ok := t.formats[format]
	return f, ok
}

// Deriver is the build's deriver; nil for none.
func (t *Table) Deriver() Deriver {
	if t == nil {
		return nil
	}
	return t.deriver
}

// CheckDerived refuses a Derived of format made under another identity than the frozen one, so no
// document records an identity its text was not made under.
func (t *Table) CheckDerived(format string, d Derived) error {
	f, ok := t.Format(format)
	switch {
	case !ok:
		return &Error{format, "this build derives no such format"}
	case d.ID != f.ID || d.Version != f.Version:
		return &Error{format, fmt.Sprintf("derived as %s@%s, not the frozen %s@%s", d.ID, d.Version, f.ID, f.Version)}
	}
	return nil
}
