package outline

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/yongjohnlee80/golib/parse/markdown"
	pyaml "github.com/yongjohnlee80/golib/parse/yaml"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// Heading is one heading of a Markdown file: an ID stable within one version of the file (its
// slug, numbered when another heading has it), its level (1–6), its text, and where it starts:
// the 1-based line and the byte offset.
type Heading struct {
	ID    string
	Level int
	Text  string
	Line  int
	Byte  int
}

// Doc is a document read for navigation. Build one per version of its text.
type Doc struct {
	kind     kind.Kind
	title    string
	src      []byte
	lines    []int // each line's starting byte offset
	headings []Heading
	yaml     *pyaml.Stream // a YAML document that parsed; nil otherwise
}

// Read reads src as kind k (a derived document's text as the Markdown it is); title is what a
// plain-text breadcrumb shows (the file's name).
func Read(src []byte, k kind.Kind, title string) *Doc {
	d := &Doc{kind: k, title: title, src: src, lines: lineStarts(src)}
	if r := readings.Of(k).read; r != nil {
		r(d)
	}
	return d
}

// reading is how a kind is navigated: what is read of it once, and the breadcrumb at a place.
type reading struct {
	read   func(d *Doc)
	crumbs func(d *Doc, line, col int) []string
}

// readings are each kind's (kind.Table: a kind added is a kind navigated). Markdown has headings;
// YAML has key paths; plain text and a registered chunker's file only the title, since a code
// outline is not AutoDoc's yet (docs/decisions/search.md#file-kinds-and-their-precedence).
var readings = kind.NewTable(map[kind.Kind]reading{
	kind.Markdown:   {readHeadings, headingCrumbs},
	kind.Derived:    {readHeadings, headingCrumbs},
	kind.YAML:       {readYAML, yamlCrumbs},
	kind.Text:       {nil, titleCrumbs},
	kind.Registered: {nil, titleCrumbs},
})

func readHeadings(d *Doc) { d.headings = headings(d.src, d.lines) }

func readYAML(d *Doc) {
	if st, err := pyaml.Parse(d.src); err == nil {
		d.yaml = st
	}
}

// headingCrumbs is the heading the place is under and the headings above it, outermost first.
func headingCrumbs(d *Doc, line, _ int) []string {
	var stack []Heading
	for _, h := range d.headings {
		if h.Line > line {
			break
		}
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, h)
	}
	out := make([]string, len(stack))
	for i, h := range stack {
		out[i] = h.Text
	}
	return out
}

// yamlCrumbs is the key path to the value at the place.
func yamlCrumbs(d *Doc, line, col int) []string {
	if d.yaml == nil {
		return nil
	}
	return yamlPath(d.yaml, d.offset(line, col))
}

func titleCrumbs(d *Doc, _, _ int) []string { return []string{d.title} }

// Headings are the file's headings in source order; none outside Markdown (and a derived
// document's text, which is Markdown), the only kind with headings to navigate.
func (d *Doc) Headings() []Heading { return d.headings }

// Crumbs is the breadcrumb at a place: line is 1-based, col counts bytes into the line. Markdown:
// the heading the place is under and the headings above it, outermost first. YAML: the key path
// to the value there. Plain text, and a registered chunker's file: the title.
func (d *Doc) Crumbs(line, col int) []string { return readings.Of(d.kind).crumbs(d, line, col) }

// LineStart is the byte offset where line (1-based) starts, clamped to the text's lines.
func (d *Doc) LineStart(line int) int { return d.offset(line, 0) }

// offset is the byte offset of line and col, clamped to the text.
func (d *Doc) offset(line, col int) int {
	if len(d.lines) == 0 {
		return 0
	}
	i := min(max(line, 1), len(d.lines)) - 1
	return min(d.lines[i]+max(col, 0), len(d.src))
}

func lineStarts(src []byte) []int {
	out := []int{0}
	for i, b := range src {
		if b == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

func lineOf(lines []int, off int) int {
	return sort.Search(len(lines), func(i int) bool { return lines[i] > off })
}

// headings walks the file's heading nodes; frontmatter is not the file's text, and is skipped.
func headings(src []byte, lines []int) []Heading {
	var out []Heading
	eachHeading(kind.ParseMarkdown(src).Root, src, func(n *markdown.Node, id, text string) {
		out = append(out, Heading{ID: id, Level: n.Level(), Text: text, Line: lineOf(lines, n.Span.Start), Byte: n.Span.Start})
	})
	return out
}

// HeadingIDs is the ID of each heading under root, a document parsed from src, in document order:
// the slug GitHub gives the heading, numbered when an earlier heading has it. They are the IDs a
// Doc's Headings carry, so a page drawn from the document can give its headings the anchors a
// link written for GitHub expects.
func HeadingIDs(root *markdown.Node, src []byte) []string {
	var out []string
	eachHeading(root, src, func(_ *markdown.Node, id, _ string) { out = append(out, id) })
	return out
}

// eachHeading calls visit with each heading under root in document order, its ID and its text.
// Frontmatter is not the file's text, and is skipped.
func eachHeading(root *markdown.Node, src []byte, visit func(n *markdown.Node, id, text string)) {
	// occurrences is GitHub's slugger's: each ID given out, and for a repeated slug how many times
	// it has been numbered. A repeat takes the next free "-N"; checking every ID given out, not
	// only the slugs, keeps "Foo", "Foo", "Foo-1" from giving foo-1 twice (foo, foo-1, foo-1-1).
	occurrences := map[string]int{}
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Kind == markdown.KindFrontmatter {
				continue
			}
			if c.Kind == markdown.KindHeading {
				text := strings.TrimSpace(plain(c, src))
				base := slug(text)
				id := base
				for {
					if _, taken := occurrences[id]; !taken {
						break
					}
					occurrences[base]++
					id = fmt.Sprintf("%s-%d", base, occurrences[base])
				}
				occurrences[id] = 0
				visit(c, id, text)
				continue
			}
			walk(c)
		}
	}
	walk(root)
}

// plain is a node's text, its inline markup dropped. An image's alt text is not shown text, so
// it is left out, as GitHub leaves it out of a heading's anchor.
func plain(n *markdown.Node, src []byte) string {
	var b bytes.Buffer
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		if n.Kind == markdown.KindImage {
			return
		}
		if n.FirstChild == nil {
			switch n.Kind {
			case markdown.KindText, markdown.KindCodeSpan:
				b.Write(n.Text(src))
			case markdown.KindSoftBreak, markdown.KindHardBreak:
				b.WriteByte(' ')
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.Next {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// slug is a heading's anchor as GitHub writes it (github-slugger): lowercase; letters, combining marks, numbers,
// '-' and connector punctuation such as '_' kept; spaces as '-'; the rest dropped. Marks matter for
// scripts written with them (नमस्ते keeps its virama) and for decomposed accents (e + U+0301).
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.Is(unicode.M, r) || unicode.IsNumber(r) || r == '-' || unicode.Is(unicode.Pc, r):
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	// a heading with nothing a slug keeps (only emoji or punctuation) has an empty slug, as on
	// GitHub, and its repeats are -1, -2
	return b.String()
}

// yamlPath is the key path to the deepest value whose entry holds off.
func yamlPath(st *pyaml.Stream, off int) []string {
	var out []string
	var walk func(*pyaml.Node) bool
	walk = func(n *pyaml.Node) bool {
		if n == nil {
			return false
		}
		switch n.Kind {
		case pyaml.KindMapping:
			for _, p := range n.Pairs {
				if off >= p.Key.Span.Start && off <= max(p.Key.Span.End, p.Value.Span.End) {
					out = append(out, strings.TrimSpace(string(p.Key.Value)))
					walk(p.Value)
					return true
				}
			}
		case pyaml.KindSequence:
			for i, it := range n.Items {
				if off >= it.Span.Start && off <= it.Span.End {
					out = append(out, fmt.Sprintf("[%d]", i))
					walk(it)
					return true
				}
			}
		}
		return false
	}
	for i, doc := range st.Docs {
		if off >= doc.Span.Start && off <= doc.Span.End {
			if len(st.Docs) > 1 {
				out = append(out, fmt.Sprintf("document %d", i+1))
			}
			walk(doc.Root)
			break
		}
	}
	return out
}
