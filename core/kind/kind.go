// Package kind is how AutoDoc reads a file, decided by its extension and never by sniffing its
// content (ADR 0212 §4): Markdown, plain text, YAML, or a Pro-only document format that Community
// never reads. The indexer, the document API and the editor all ask here, so they agree.
package kind

import (
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
