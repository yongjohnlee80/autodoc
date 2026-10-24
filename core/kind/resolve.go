package kind

import (
	"fmt"

	"github.com/yongjohnlee80/golib/parse/markdown"
)

// Resolver is how a workspace's paths are read: the build's registrations (its chunked extensions,
// and the formats it derives beyond the built-in list), and the workspace's own text extensions,
// asked on each call since the workspace may change them while the daemon runs. The indexer, the
// document API and the editor each hold one, so they agree.
type Resolver struct {
	Registrations Registrations
	Text          func() []string // the workspace's own plain-text extensions; nil for none
}

// Of is the kind of path.
func (r Resolver) Of(p string) Kind {
	var text []string
	if r.Text != nil {
		text = r.Text()
	}
	return r.Registrations.Of(p, text)
}

// numKinds is how many kinds there are: every table over them has one entry each.
const numKinds = Registered + 1

// Traits are what a kind is wherever it is read: facts of the kind, not of any package.
type Traits struct {
	Text        bool // UTF-8 that must validate as such (Textual)
	Frontmatter bool // a schema checks its frontmatter: Markdown alone
	ReadsAs     Kind // the kind its served text is: a derived document's is Markdown
}

var traits = [numKinds]Traits{
	Markdown:   {Frontmatter: true, ReadsAs: Markdown},
	Text:       {Text: true, ReadsAs: Text},
	YAML:       {Text: true, ReadsAs: YAML},
	Derived:    {ReadsAs: Markdown},
	Registered: {Text: true, ReadsAs: Registered},
}

// Traits are the kind's.
func (k Kind) Traits() Traits { return traits[k] }

// Table is a package's own behaviour per kind: how the indexer reads one, how the outline finds its
// places, the editor's syntax for one. It holds one entry for every kind, checked when it is built, so a kind added here cannot
// be forgotten by a package that reads kinds.
type Table[T any] struct{ by [numKinds]T }

// NewTable is the table of entries. It panics, at the package's init where tables are built, when
// a kind lacks an entry or one names no kind.
func NewTable[T any](entries map[Kind]T) Table[T] {
	var t Table[T]
	for k := Kind(0); k < numKinds; k++ {
		v, ok := entries[k]
		if !ok {
			panic(fmt.Sprintf("kind: a table lacks the kind %s", k))
		}
		t.by[k] = v
	}
	if len(entries) != int(numKinds) {
		panic(fmt.Sprintf("kind: a table names %d kinds; there are %d", len(entries), numKinds))
	}
	return t
}

// Of is k's entry.
func (t Table[T]) Of(k Kind) T { return t.by[k] }

// ParseMarkdown reads a workspace's Markdown: GFM with Obsidian's extensions (wikilinks, #tags,
// frontmatter).
func ParseMarkdown(src []byte) *markdown.Document {
	return markdown.Parse(src, markdown.WithGFM(), markdown.WithObsidian())
}

// ParseDerived reads a derived document's text: GFM alone. The text is a document's, not a vault's:
// no wikilinks or #tags, no frontmatter to check.
func ParseDerived(src []byte) *markdown.Document { return markdown.Parse(src, markdown.WithGFM()) }
