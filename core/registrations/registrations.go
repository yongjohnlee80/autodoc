// Package registrations is a build's chunkers, validated once and frozen when it starts (ADR 0216
// §1.2): what a main hands app.Main, as the daemon, its indexers and its clients read it. A build
// with none, the community build, has the nil *Table, and every method answers as for no
// registrations.
package registrations

import (
	"fmt"
	"maps"
	"slices"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// Table is a build's frozen registrations.
type Table struct {
	chunkers search.Chunkers
	versions map[string]string // a chunker's Version(), by extension, read once
}

// Error is a registration Main refuses, naming the offender.
type Error struct{ Name, Why string }

func (e *Error) Error() string { return fmt.Sprintf("registrations: %q: %s", e.Name, e.Why) }

// New validates chunkers, keyed by lower-case extension with its dot, and freezes them: each
// chunker's version is read here, once, so no document records a version it was not cut under.
// It refuses an extension that is malformed, upper-case, a built-in kind's or a Pro format's, and a
// version outside the identity charset (ValidVersion). No chunkers is the nil Table.
func New(chunkers map[string]search.Chunker) (*Table, error) {
	if len(chunkers) == 0 {
		return nil, nil
	}
	t := &Table{versions: map[string]string{}}
	for _, ext := range slices.Sorted(maps.Keys(chunkers)) {
		c := chunkers[ext]
		switch {
		case !kind.ValidExtension(ext):
			return nil, &Error{ext, "an extension is a dot and 1 to 16 lower-case letters, digits, _, + or -"}
		case kind.IsBuiltIn(ext):
			return nil, &Error{ext, "a built-in kind's extension"}
		case slices.Contains(kind.ProExtensions, ext):
			return nil, &Error{ext, "a Pro document format's extension"}
		case c == nil:
			return nil, &Error{ext, "no chunker"}
		}
		v := c.Version()
		if !ValidVersion(v) {
			return nil, &Error{ext, fmt.Sprintf("version %q: 1 to 32 letters, digits, '.', '_', '+' or '-'", v)}
		}
		if err := t.chunkers.Register(ext, c); err != nil {
			return nil, &Error{ext, err.Error()}
		}
		t.versions[ext] = v
	}
	return t, nil
}

// ValidVersion reports whether v is a chunker's or a deriver's version as document.indexer records
// it (ADR 0216 §1.6): 1 to 32 letters, digits, '.', '_', '+' or '-'.
func ValidVersion(v string) bool { return validID(v, 32, false) }

// ValidDeriverID reports whether id is a deriver's id as document.indexer records it: 1 to 64
// letters, digits, '.', '_', '+', '-' or '/', so autorag's ("autorag/pdf") are kept exactly.
func ValidDeriverID(id string) bool { return validID(id, 64, true) }

func validID(s string, max int, slash bool) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '+', r == '-':
		case r == '/' && slash:
		default:
			return false
		}
	}
	return true
}

// Chunker is the chunker of p's extension, lowercased as kind.Ext does, with its frozen version.
func (t *Table) Chunker(p string) (search.Chunker, string, bool) {
	if t == nil {
		return nil, "", false
	}
	ext := kind.Ext(p)
	c, ok := t.chunkers.For("f" + ext)
	return c, t.versions[ext], ok
}

// Extensions are the registered chunkers' extensions, sorted.
func (t *Table) Extensions() []string {
	if t == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(t.versions))
}

// Versions are the registered chunkers' frozen versions, by extension: a copy.
func (t *Table) Versions() map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return maps.Clone(t.versions)
}

// Kinds are the extensions the registrations give a kind (kind.Registrations).
func (t *Table) Kinds() kind.Registrations { return kind.Registrations{Chunked: t.Extensions()} }

// Collisions are the extensions of text, a workspace's own plain-text extensions, that a
// registration reads instead: the registration wins (ADR 0216 §1.3).
func (t *Table) Collisions(text []string) []string {
	if t == nil {
		return nil
	}
	var out []string
	for _, e := range text {
		if _, ok := t.versions[e]; ok {
			out = append(out, e)
		}
	}
	return out
}
