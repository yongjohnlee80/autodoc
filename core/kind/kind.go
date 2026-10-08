// Package kind is how AutoDoc reads a file, decided by its extension and never by sniffing its
// content (ADR 0212 §4): Markdown, plain text, YAML, a registered chunker's (ADR 0216 §1.3), or a
// derived document format, read only as the text a build's deriver makes of it. The indexer, the
// document API and the editor all ask here, so they agree.
package kind

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Kind is how a file is read.
type Kind int

const (
	Markdown   Kind = iota // the default: any eligible file without another kind
	Text                   // UTF-8 plain text: literal paragraphs, no Markdown structure
	YAML                   // a YAML document: key-path chunks
	Derived                // a document format read only as its derived text: DOCX, HTML, a Pro build's PDF
	Registered             // UTF-8 text a chunker the build registered reads, such as source code (ADR 0216)
)

func (k Kind) String() string {
	switch k {
	case Text:
		return "text"
	case YAML:
		return "yaml"
	case Derived:
		return "derived"
	case Registered:
		return "registered"
	}
	return "markdown"
}

// DerivedExtensions are the formats read only as derived text. Every build derives .docx, .htm
// and .html (core/registrations' Documents); the rest need a deriver that names them, such as a
// Pro build's .pdf.
var DerivedExtensions = []string{".doc", ".docx", ".htm", ".html", ".odt", ".pdf"}

// RawText reports whether a derived format's source is itself text a person edits: HTML. Raw access
// to a file's own bytes (core/docs' ReadRaw and WriteRaw) is for these formats alone, whatever
// another format's bytes look like: a PDF may be ASCII, and is still no source to edit.
func RawText(p string) bool { e := Ext(p); return e == ".html" || e == ".htm" }

// Ext is a path's extension, lowercased: ".MD" and ".md" are one kind.
func Ext(p string) string { return strings.ToLower(path.Ext(p)) }

// Of is the kind of path in a build with no registrations. text lists the workspace's own
// plain-text extensions (".log", ".rst"), lowercased with their dot; the built-in kinds win over it.
func Of(p string, text []string) Kind { return Registrations{}.Of(p, text) }

// Registrations are the extensions a build's registrations read, lowercased with
// their dot: its chunkers' (Chunked), and the derived formats its derivers make text of (Derived). The
// zero value is a build with none.
type Registrations struct{ Chunked, Derived []string }

// Of is the kind of path: a built-in kind, then a registered chunker's, then the workspace's own
// text extension (text), then Markdown.
func (r Registrations) Of(p string, text []string) Kind {
	ext := Ext(p)
	switch ext {
	case ".txt":
		return Text
	case ".yaml", ".yml":
		return YAML
	}
	switch {
	case slices.Contains(DerivedExtensions, ext):
		return Derived
	case slices.Contains(r.Chunked, ext):
		return Registered
	case slices.Contains(text, ext):
		return Text
	}
	return Markdown
}

// Readable reports whether path is a derived format the build derives: kind Derived, never written,
// but its derived text can be read.
func (r Registrations) Readable(p string) bool { return slices.Contains(r.Derived, Ext(p)) }

// Label is how a reader names a derived format's kind: its extension, upper-case, without the dot
// ("PDF", "DOCX").
func Label(p string) string { return strings.ToUpper(strings.TrimPrefix(Ext(p), ".")) }

// IsText reports whether the kind is read as UTF-8 text that must validate as such.
func (k Kind) IsText() bool { return k == Text || k == YAML || k == Registered }

// ErrExtension is a custom text extension that cannot be one: malformed, a built-in kind's, or a
// derived document format's (which is read only as derived text, whatever a workspace says).
type ErrExtension struct{ Ext, Why string }

func (e *ErrExtension) Error() string { return fmt.Sprintf("kind: %q: %s", e.Ext, e.Why) }

// builtIn are the extensions whose kind is fixed.
var builtIn = []string{".md", ".txt", ".yaml", ".yml"}

// IsBuiltIn reports whether ext (lowercased, with its dot) is a built-in kind's.
func IsBuiltIn(ext string) bool { return slices.Contains(builtIn, ext) }

// ValidExtension reports whether e is an extension as a workspace or a build may name one: a dot
// and 1 to 16 lower-case letters, digits, '_', '+' or '-'.
func ValidExtension(e string) bool {
	name, ok := strings.CutPrefix(e, ".")
	if !ok || len(name) < 1 || len(name) > 16 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '+' || r == '-') {
			return false
		}
	}
	return true
}

// TextExtensions normalizes a workspace's own plain-text extensions: each lowercased with its dot,
// 1 to 16 letters, digits, '_', '+' or '-' after it, none a built-in kind's or a derived format's, in
// the order given without repeats. They are plain text by declaration: nothing is sniffed.
func TextExtensions(in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		switch {
		case !ValidExtension(e):
			return nil, &ErrExtension{raw, "an extension is a dot and 1 to 16 letters, digits, _, + or -"}
		case slices.Contains(DerivedExtensions, e):
			return nil, &ErrExtension{raw, "a derived document format, read as its derived text"}
		case slices.Contains(builtIn, e):
			return nil, &ErrExtension{raw, "already a built-in kind"}
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}
