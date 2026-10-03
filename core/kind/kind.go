// Package kind is how AutoDoc reads a file, decided by its extension and never by sniffing its
// content (ADR 0212 §4): Markdown, plain text, YAML, or a Pro-only document format that Community
// never reads. The indexer, the document API and the editor all ask here, so they agree.
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
	Markdown Kind = iota // the default: any eligible file without another kind
	Text                 // UTF-8 plain text: literal paragraphs, no Markdown structure
	YAML                 // a YAML document: key-path chunks
	Pro                  // a binary document format only AutoDoc Pro's extractors read
)

func (k Kind) String() string {
	switch k {
	case Text:
		return "text"
	case YAML:
		return "yaml"
	case Pro:
		return "pro"
	}
	return "markdown"
}

// ProExtensions are the binary document formats Community refuses (AutoDoc 02/03 own them).
var ProExtensions = []string{".doc", ".docx", ".odt", ".pdf"}

// Ext is a path's extension, lowercased: ".MD" and ".md" are one kind.
func Ext(p string) string { return strings.ToLower(path.Ext(p)) }

// Of is the kind of path. text lists the workspace's own plain-text extensions (".log", ".rst"),
// lowercased with their dot; the built-in kinds win over it.
func Of(p string, text []string) Kind {
	ext := Ext(p)
	switch ext {
	case ".txt":
		return Text
	case ".yaml", ".yml":
		return YAML
	}
	switch {
	case slices.Contains(ProExtensions, ext):
		return Pro
	case slices.Contains(text, ext):
		return Text
	}
	return Markdown
}

// IsText reports whether the kind is read as UTF-8 text that must validate as such.
func (k Kind) IsText() bool { return k == Text || k == YAML }

// ErrExtension is a custom text extension that cannot be one: malformed, a built-in kind's, or a
// Pro document format's (which stays unavailable whatever a workspace says).
type ErrExtension struct{ Ext, Why string }

func (e *ErrExtension) Error() string { return fmt.Sprintf("kind: %q: %s", e.Ext, e.Why) }

// builtIn are the extensions whose kind is fixed.
var builtIn = []string{".md", ".txt", ".yaml", ".yml"}

// TextExtensions normalizes a workspace's own plain-text extensions: each lowercased with its dot,
// 1 to 16 letters, digits, '_', '+' or '-' after it, none a built-in kind's or a Pro format's, in
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
		name := e[1:]
		ok := len(name) >= 1 && len(name) <= 16
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '+' || r == '-') {
				ok = false
			}
		}
		switch {
		case !ok:
			return nil, &ErrExtension{raw, "an extension is a dot and 1 to 16 letters, digits, _, + or -"}
		case slices.Contains(ProExtensions, e):
			return nil, &ErrExtension{raw, "a Pro document format, unavailable in Community"}
		case slices.Contains(builtIn, e):
			return nil, &ErrExtension{raw, "already a built-in kind"}
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}
