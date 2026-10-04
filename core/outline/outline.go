// Package outline is a document's structure for navigation (ADR 0212 §6): a Markdown file's
// headings, and the breadcrumb at a place in any document — the headings above it in Markdown,
// the key path in YAML, the title in plain text. The daemon's doc.outline and the editor's live
// outline both read it, so a saved file and an unsaved buffer are outlined alike.
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

// Read reads src as kind k; title is what a plain-text breadcrumb shows (the file's name).
func Read(src []byte, k kind.Kind, title string) *Doc {
	d := &Doc{kind: k, title: title, src: src, lines: lineStarts(src)}
	switch k {
	case kind.Markdown:
		d.headings = headings(src, d.lines)
	case kind.YAML:
		if st, err := pyaml.Parse(src); err == nil {
			d.yaml = st
		}
	}
	return d
}

// Headings are the file's headings in source order; none outside Markdown, which is the only kind
// with headings to navigate.
func (d *Doc) Headings() []Heading { return d.headings }

// Crumbs is the breadcrumb at a place: line is 1-based, col counts bytes into the line. Markdown:
// the heading the place is under and the headings above it, outermost first. YAML: the key path
// to the value there. Plain text, and a registered chunker's file: the title.
func (d *Doc) Crumbs(line, col int) []string {
	switch d.kind {
	case kind.Markdown:
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
	case kind.YAML:
		if d.yaml == nil {
			return nil
		}
		return yamlPath(d.yaml, d.offset(line, col))
	case kind.Text, kind.Registered:
		return []string{d.title} // a code outline is not AutoDoc's yet (ADR 0216 §3)
	}
	return nil
}

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
	doc := markdown.Parse(src, markdown.GFM(), markdown.Obsidian())
	var out []Heading
	seen := map[string]int{}
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Kind == markdown.KindFrontmatter {
				continue
			}
			if c.Kind == markdown.KindHeading {
				text := strings.TrimSpace(plain(c, src))
				id := slug(text)
				if count := seen[id]; count > 0 {
					seen[id]++
					id = fmt.Sprintf("%s-%d", id, count)
				} else {
					seen[id] = 1
				}
				out = append(out, Heading{ID: id, Level: c.Level, Text: text, Line: lineOf(lines, c.Span.Start), Byte: c.Span.Start})
				continue
			}
			walk(c)
		}
	}
	walk(doc.Root)
	return out
}

// plain is a node's text, its inline markup dropped.
func plain(n *markdown.Node, src []byte) string {
	var b bytes.Buffer
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
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

// slug is a heading's anchor as GitHub writes it: lowercase, letters, digits, '-' and '_' kept,
// spaces as '-', the rest dropped.
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "section"
	}
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
