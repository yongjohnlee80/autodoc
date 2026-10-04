package index

import (
	"bytes"
	"crypto/sha256"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/parse/markdown"
)

// The chunk sizes, in estimated tokens (words × 1.3: a stated estimate, not a count per model).
const (
	targetTokens = 350
	maxTokens    = 512
)

// chunkT is one chunk of a document: a run of blocks of one section, with the headings above it.
type chunkT struct {
	ord                int
	breadcrumb, body   string
	byteStart, byteEnd int
	hash, textHash     []byte
}

// unit is a block the packer places whole: a code block, a table and a list item are never split.
// holes are the link reference definitions inside it, which are not text.
type unit struct {
	start, end int
	holes      []markdown.Span
	tokens     int
	body       string // nonempty for a split block with synthetic table header or code fence
	kind       markdown.Kind
}

// chunkDoc splits a parsed document into heading-aware chunks (ADR 0204 §4.3). Sections split at
// every top-level heading; a section's blocks are packed in order into chunks of about
// targetTokens, maxTokens at most, and a block larger than that is a chunk of its own. The
// breadcrumb names the document's title and the headings above; chunks do not overlap, since the
// breadcrumb carries the context. Definitions and frontmatter are not text.
func chunkDoc(doc *markdown.Document, title string) []chunkT {
	return chunkDocWithLimit(doc, title, maxTokens)
}

func chunkPlainText(src []byte, title string, limit int) []chunkT {
	if limit <= 0 {
		limit = maxTokens
	}
	crumb := boundedCrumb(title, limit)
	budget := max(1, limit-tokensOf([]byte(crumb+"\n"), 0, len(crumb)+1))
	var units []unit
	start := -1
	for offset := 0; offset < len(src); {
		end := offset
		for end < len(src) && src[end] != '\n' {
			end++
		}
		if end < len(src) {
			end++
		}
		if len(bytes.TrimSpace(src[offset:end])) == 0 {
			if start >= 0 {
				piece := unit{start: start, end: offset, tokens: tokensOf(src, start, offset), kind: markdown.KindParagraph}
				units = append(units, splitOversized(src, piece, piece.kind, budget)...)
				start = -1
			}
		} else if start < 0 {
			start = offset
		}
		offset = end
	}
	if start >= 0 {
		piece := unit{start: start, end: len(src), tokens: tokensOf(src, start, len(src)), kind: markdown.KindParagraph}
		units = append(units, splitOversized(src, piece, piece.kind, budget)...)
	}
	out := packLimited(src, units, crumb, 0, limit)
	if len(out) == 0 {
		out = append(out, newChunk(0, crumb, "", 0, 0))
	}
	return out
}

func chunkDocWithLimit(doc *markdown.Document, title string, limit int) []chunkT {
	if limit <= 0 {
		limit = maxTokens
	}
	src := doc.Source
	var out []chunkT
	var stack []string // the heading texts above, by level
	var units []unit
	flush := func() {
		heads := nonEmpty(stack)
		if len(heads) > 0 && heads[0] == title {
			heads = heads[1:] // the title came from this heading: say it once
		}
		crumb := strings.Join(append([]string{title}, heads...), " > ")
		crumb = boundedCrumb(crumb, limit)
		budget := max(1, limit-tokensOf([]byte(crumb+"\n"), 0, len(crumb)+1)-2)
		var parts []unit
		for _, u := range units {
			parts = append(parts, splitOversized(src, u, u.kind, budget)...)
		}
		out = append(out, packLimited(src, parts, crumb, len(out), limit)...)
		units = units[:0]
	}
	for n := doc.Root.FirstChild; n != nil; n = n.Next {
		switch n.Kind {
		case markdown.KindLinkRefDef, markdown.KindFrontmatter:
			continue
		case markdown.KindHeading:
			flush()
			for len(stack) < n.Level {
				stack = append(stack, "")
			}
			stack = append(stack[:n.Level-1], plainText(n, src))
			continue
		case markdown.KindList:
			if tokensOf(src, n.Span.Start, n.Span.End) > limit {
				for it := n.FirstChild; it != nil; it = it.Next {
					units = append(units, unitOf(src, it))
				}
				continue
			}
		}
		units = append(units, unitOf(src, n))
	}
	flush()
	if len(out) == 0 {
		// a note with no text (a title, frontmatter) is still a note: one chunk of no body carries its
		// title, so it can be found
		out = append(out, newChunk(0, boundedCrumb(title, limit), "", 0, 0))
	}
	for i := range out {
		out[i].ord = i
	}
	return out
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// unitOf is block n as a unit. A definition is a child of the block holding its paragraph (a list
// item, a block quote), so one can sit anywhere below n.
func unitOf(src []byte, n *markdown.Node) unit {
	u := unit{start: n.Span.Start, end: n.Span.End, tokens: tokensOf(src, n.Span.Start, n.Span.End), kind: n.Kind}
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Kind == markdown.KindLinkRefDef {
				u.holes = append(u.holes, c.Span)
				u.tokens -= tokensOf(src, c.Span.Start, c.Span.End)
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return u
}

func tokensOf(src []byte, start, end int) int {
	b := src[start:end]
	return max(len(strings.Fields(string(b)))*13/10, (len(b)+3)/4)
}

// pack places units in order: a chunk closes once it reaches targetTokens, or before a unit that
// would take it past maxTokens.
func pack(src []byte, units []unit, crumb string, base int) []chunkT {
	return packLimited(src, units, crumb, base, maxTokens)
}

func packLimited(src []byte, units []unit, crumb string, base, limit int) []chunkT {
	crumb = boundedCrumb(crumb, limit)
	budget := max(1, limit-tokensOf([]byte(crumb+"\n"), 0, len(crumb)+1))
	var out []chunkT
	var cur []unit
	tokens := 0
	emit := func() {
		if len(cur) == 0 {
			return
		}
		start, end := cur[0].start, cur[len(cur)-1].end
		body := strings.TrimSpace(text(src, cur))
		if body != "" {
			out = append(out, newChunk(base+len(out), crumb, body, start, end))
		}
		cur, tokens = cur[:0], 0
	}
	for _, u := range units {
		if len(cur) > 0 && tokens+u.tokens > budget {
			emit()
		}
		cur = append(cur, u)
		tokens += u.tokens
		if tokens >= min(limit*targetTokens/maxTokens, budget) {
			emit()
		}
	}
	emit()
	return out
}

func boundedCrumb(crumb string, limit int) string {
	for tokensOf([]byte(crumb), 0, len(crumb)) >= limit && len(crumb) > 0 {
		_, size := utf8.DecodeLastRuneInString(crumb)
		crumb = crumb[:len(crumb)-size]
	}
	return crumb
}

// text is the units' source with their holes cut out. Whitespace between two units is kept; anything
// else there is a definition (a block of its own), which becomes a blank line.
func text(src []byte, units []unit) string {
	var b strings.Builder
	for i, u := range units {
		if i > 0 {
			if gap := src[units[i-1].end:u.start]; len(bytes.TrimSpace(gap)) == 0 {
				b.Write(gap)
			} else {
				b.WriteString("\n\n")
			}
		}
		at := u.start
		if u.body != "" {
			b.WriteString(u.body)
			continue
		}
		for _, h := range u.holes {
			b.Write(src[at:h.Start])
			at = h.End
		}
		b.Write(src[at:u.end])
	}
	return b.String()
}

// splitOversized retains source offsets for each fragment. Synthetic markdown syntax
// belongs in the embedded body, never in the fragment's source span.
func splitOversized(src []byte, u unit, kind markdown.Kind, limit int) []unit {
	if u.tokens <= limit {
		return []unit{u}
	}
	bodyText := text(src, []unit{u})
	prefix, suffix := "", ""
	if kind == markdown.KindTable {
		lines := strings.SplitAfter(bodyText, "\n")
		if len(lines) >= 3 {
			prefix = lines[0] + lines[1]
			if tokensOf([]byte(prefix), 0, len(prefix)) > limit/3 {
				// A header larger than the budget cannot be repeated in full.
				// Retain a short column-name excerpt; the original remains in its source span.
				header := strings.TrimSpace(lines[0])
				for len(header) > 0 && tokensOf([]byte(header), 0, len(header)) > limit/4 {
					_, n := utf8.DecodeLastRuneInString(header)
					header = header[:len(header)-n]
				}
				prefix = header + "… |\n|---|\n"
			}
		}
	} else if kind == markdown.KindCodeBlock {
		line, _, ok := strings.Cut(bodyText, "\n")
		if ok && (strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~")) {
			prefix, suffix = line+"\n", "\n"+strings.TrimSpace(line)[:3]
		}
	}
	// A table's first fragment already contains its header; subsequent fragments repeat it.
	var out []unit
	at := u.start
	for at < u.end {
		p := ""
		if len(out) > 0 {
			p = prefix
		}
		remaining := limit - tokensOf([]byte(p+suffix), 0, len(p+suffix))
		if remaining < 1 {
			remaining = 1
		}
		end := at
		for end < u.end {
			next := end
			if i := bytes.IndexByte(src[next:u.end], '\n'); i >= 0 {
				next += i + 1
			} else {
				next = u.end
			}
			if tokensOf(src, at, next) > remaining {
				break
			}
			end = next
		}
		if end == at { // An indivisible row/line: split at a UTF-8 boundary.
			for end < u.end {
				_, n := utf8.DecodeRune(src[end:u.end])
				if tokensOf(src, at, end+n) > remaining && end > at {
					break
				}
				end += n
				if tokensOf(src, at, end) >= remaining {
					break
				}
			}
		}
		if end <= at {
			end = min(at+1, u.end)
		}
		part := text(src, []unit{{start: at, end: end, holes: clippedHoles(u.holes, at, end)}})
		if kind == markdown.KindCodeBlock && prefix != "" {
			if len(out) > 0 {
				part = prefix + part
			}
			if !strings.HasSuffix(strings.TrimSpace(part), strings.TrimSpace(suffix)) {
				part += suffix
			}
		} else {
			part = p + part
		}
		out = append(out, unit{start: at, end: end, body: part, tokens: tokensOf([]byte(part), 0, len(part))})
		at = end
	}
	return out
}

func clippedHoles(holes []markdown.Span, start, end int) []markdown.Span {
	var out []markdown.Span
	for _, h := range holes {
		if h.End <= start || h.Start >= end {
			continue
		}
		out = append(out, markdown.Span{Start: max(h.Start, start), End: min(h.End, end)})
	}
	return out
}

// newChunk hashes a chunk: hash is its identity under this chunker (a chunker bump changes every
// hash, so every chunk is rewritten once); textHash is exactly the text an embedding is made of, so
// a chunker change that keeps the text keeps its vector.
func newChunk(ord int, crumb, body string, start, end int) chunkT {
	h := sha256.New()
	h.Write([]byte(strconv.Itoa(ChunkerVersion)))
	h.Write([]byte{0})
	h.Write([]byte(crumb))
	h.Write([]byte{0})
	h.Write([]byte(body))
	t := sha256.Sum256([]byte(crumb + "\n" + body))
	return chunkT{ord: ord, breadcrumb: crumb, body: body, byteStart: start, byteEnd: end, hash: h.Sum(nil), textHash: t[:]}
}

// plainText is a node's text without markup: what a heading says.
func plainText(n *markdown.Node, src []byte) string {
	var b strings.Builder
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			switch c.Kind {
			case markdown.KindText, markdown.KindCodeSpan:
				b.Write(c.Text(src))
			case markdown.KindSoftBreak, markdown.KindHardBreak:
				b.WriteByte(' ')
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
