package tui

import (
	"github.com/yongjohnlee80/golib/tui/widget"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/parse/markdown"
)

// THE HIGHLIGHT MARKS — words marked over Markdown's highlighting: the search's in its preview,
// the page find's on the page.

// marks is the search's words, as the preview's highlighter marks them.
type marks struct {
	terms atomic.Pointer[[]string]
	// whole marks only whole words (Vim's * and #): a term beginning or ending with a keyword
	// character (widget.IsKeyword) is marked only where no keyword character touches it there.
	// The editor's own matching (FindAll) also ends a word where two scripts' keywords meet
	// (漢字abc); these marks don't, a difference only there.
	whole atomic.Bool
}

func (m *marks) Store(terms []string) { m.terms.Store(&terms) }

func (m *marks) load() []string {
	if p := m.terms.Load(); p != nil {
		return *p
	}
	return nil
}

// searchHighlighter is Markdown's, with the search's words marked over it (the Alert style).
func (h *Host) searchHighlighter() highlight.Highlighter { return markedHighlighter(&h.marks) }

func (h *Host) searchDefinition() highlight.Definition {
	d := markdown.Definition()
	d.Name = "Markdown (search)"
	d.Extensions = nil
	d.Aliases = nil
	factory := d.SourceFactory
	d.SourceFactory = func(catalog *highlight.Catalog) highlight.Source {
		source := factory(catalog)
		source.Highlighter = markedHighlighterWith(&h.marks, source.Highlighter)
		return source
	}
	return d
}

// markedHighlighter is Markdown's, with the words in m marked over it (the Alert style), read
// afresh on every line: the preview's for the search's words, the page's for the find's.
func markedHighlighter(m *marks) highlight.Highlighter {
	return markedHighlighterWith(m, markdown.Highlighter())
}

func markedHighlighterWith(m *marks, base highlight.Highlighter) highlight.Highlighter {
	return highlight.HighlighterFunc(func(line string, prev highlight.State) ([]highlight.Span, highlight.State) {
		spans, next := base.HighlightBlock(line, prev)
		return markedSpans(m, line, spans), next
	})
}

func markedSpans(m *marks, line string, spans []highlight.Span) []highlight.Span {
	terms := m.load()
	if len(terms) == 0 || line == "" {
		return spans
	}
	styles := make([]highlight.Style, len(line))
	for _, s := range spans {
		for i := s.Start; i < s.End && i < len(styles); i++ {
			styles[i] = s.Style
		}
	}
	marked := false
	for i := 0; i < len(line); {
		for _, t := range terms {
			if end := foldAt(line, i, t); end > i && (!m.whole.Load() || wholeAt(line, i, end)) {
				for j := i; j < end; j++ {
					styles[j] = highlight.Alert
				}
				marked = true
			}
		}
		_, n := utf8.DecodeRuneInString(line[i:])
		i += n
	}
	if !marked {
		return spans
	}
	var out []highlight.Span
	for i := 0; i < len(styles); {
		j := i
		for j < len(styles) && styles[j] == styles[i] {
			j++
		}
		if styles[i] != highlight.Normal {
			out = append(out, highlight.Span{Start: i, End: j, Style: styles[i]})
		}
		i = j
	}
	return out
}

// wholeAt reports line[i:end] standing as a whole word: a keyword character at either end of it
// has no keyword character beside it outside.
func wholeAt(line string, i, end int) bool {
	first, _ := utf8.DecodeRuneInString(line[i:])
	last, _ := utf8.DecodeLastRuneInString(line[:end])
	if widget.IsKeyword(string(first)) && i > 0 {
		if prev, _ := utf8.DecodeLastRuneInString(line[:i]); widget.IsKeyword(string(prev)) {
			return false
		}
	}
	if widget.IsKeyword(string(last)) && end < len(line) {
		if next, _ := utf8.DecodeRuneInString(line[end:]); widget.IsKeyword(string(next)) {
			return false
		}
	}
	return true
}

// foldAt is where term t (lower case) ends when it matches s from byte i, case aside; -1 when it
// does not. The offsets are s's own: lower-casing can change a rune's length, so a match found in
// strings.ToLower(s) would mark the wrong bytes of s.
func foldAt(s string, i int, t string) int {
	for _, tr := range t {
		if i >= len(s) {
			return -1
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if unicode.ToLower(r) != tr {
			return -1
		}
		i += n
	}
	return i
}
