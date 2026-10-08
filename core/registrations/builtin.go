package registrations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/yongjohnlee80/golib/extract"
	"github.com/yongjohnlee80/golib/extract/docx"
	"github.com/yongjohnlee80/golib/extract/html"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk/code"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// Builtin are the chunkers every build has: golib's code chunkers (Go, TypeScript and JavaScript,
// Python, Rust), with a main's own added. An extension in both is refused, naming it: a main never
// replaces a built-in chunker.
func Builtin(own map[string]search.Chunker) (map[string]search.Chunker, error) {
	out := code.Extensions()
	for _, ext := range slices.Sorted(maps.Keys(own)) {
		if _, dup := out[ext]; dup {
			return nil, &Error{ext, "a built-in chunker's extension: every build has golib's code chunkers"}
		}
		out[ext] = own[ext]
	}
	return out, nil
}

// versioned is an extractor with the identity its text is made under.
type versioned interface {
	extract.Extractor
	ID() string
	Version() string
}

// Documents is the deriver every build has: .docx, .htm and .html, through golib's extractors.
// maxContainer and maxText bound a file and its text (core/derived's MaxContainer and MaxText).
// There is no cache: both formats cost about a read of the file, and the indexer derives a file
// only when it changed.
func Documents(maxContainer, maxText int64) Deriver {
	byExt := map[string]versioned{".docx": docx.Extractor{}, ".htm": html.Extractor{}, ".html": html.Extractor{}}
	opts := []extract.Option{extract.MaxContainer(maxContainer), extract.MaxText(maxText)}
	for ext, e := range byExt {
		opts = append(opts, extract.Register(e, ext))
	}
	return documents{byExt: byExt, set: extract.New(opts...)}
}

type documents struct {
	byExt map[string]versioned
	set   *extract.Set
}

func (d documents) Formats() []string { return slices.Sorted(maps.Keys(d.byExt)) }

func (d documents) Describe(format string) (id, version string) {
	e, ok := d.byExt[format]
	if !ok {
		return "", ""
	}
	return e.ID(), e.Version()
}

// Derive extracts the file into memory, bounded by the text limit, so the text's size is known
// before it is read.
func (d documents) Derive(ctx context.Context, name string, r io.ReaderAt, size int64) (Derived, error) {
	format := kind.Ext(name)
	e, ok := d.byExt[format]
	if !ok {
		return Derived{}, fmt.Errorf("registrations: documents: %s: no extractor", name)
	}
	var buf bytes.Buffer
	info, err := d.set.Extract(ctx, name, r, size, &buf)
	if err != nil {
		return Derived{}, err
	}
	return Derived{Text: io.NopCloser(bytes.NewReader(buf.Bytes())), Bytes: int64(buf.Len()), Info: info, ID: e.ID(), Version: e.Version()}, nil
}

// byFormat is the build's derivers as one: each format goes to the deriver that named it. New
// fills it, refusing a format two derivers name, and freezes it.
type byFormat map[string]Deriver

func (b byFormat) Formats() []string { return slices.Sorted(maps.Keys(b)) }

func (b byFormat) Describe(format string) (id, version string) {
	if d, ok := b[format]; ok {
		return d.Describe(format)
	}
	return "", ""
}

func (b byFormat) Derive(ctx context.Context, name string, r io.ReaderAt, size int64) (Derived, error) {
	d, ok := b[kind.Ext(name)]
	if !ok {
		return Derived{}, fmt.Errorf("registrations: %s: no deriver for %s", name, kind.Ext(name))
	}
	return d.Derive(ctx, name, r, size)
}
