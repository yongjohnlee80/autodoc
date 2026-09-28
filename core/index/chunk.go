package index

import (
	"crypto/sha256"
	"strconv"
	"strings"

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
type unit struct {
	start, end int
	tokens     int
}

// chunkDoc splits a parsed document into heading-aware chunks (ADR 0204 §4.3). Sections split at
// every top-level heading; a section's blocks are packed in order into chunks of about
// targetTokens, maxTokens at most, and a block larger than that is a chunk of its own. The
// breadcrumb names the document's title and the headings above; chunks do not overlap, since the
// breadcrumb carries the context. Definitions and frontmatter are not text.
func chunkDoc(doc *markdown.Document, title string) []chunkT {
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
		out = append(out, pack(src, units, crumb, len(out))...)
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
			if tokensOf(src, n.Span.Start, n.Span.End) > maxTokens {
				for it := n.FirstChild; it != nil; it = it.Next {
					units = append(units, unitOf(src, it.Span.Start, it.Span.End))
				}
				continue
			}
		}
		units = append(units, unitOf(src, n.Span.Start, n.Span.End))
	}
	flush()
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

func unitOf(src []byte, start, end int) unit {
	return unit{start: start, end: end, tokens: tokensOf(src, start, end)}
}

func tokensOf(src []byte, start, end int) int {
	return len(strings.Fields(string(src[start:end]))) * 13 / 10
}

// pack places units in order: a chunk closes once it reaches targetTokens, or before a unit that
// would take it past maxTokens.
func pack(src []byte, units []unit, crumb string, base int) []chunkT {
	var out []chunkT
	var cur []unit
	tokens := 0
	emit := func() {
		if len(cur) == 0 {
			return
		}
		start, end := cur[0].start, cur[len(cur)-1].end
		body := strings.TrimSpace(string(src[start:end]))
		if body != "" {
			out = append(out, newChunk(base+len(out), crumb, body, start, end))
		}
		cur, tokens = cur[:0], 0
	}
	for _, u := range units {
		if len(cur) > 0 && tokens+u.tokens > maxTokens {
			emit()
		}
		cur = append(cur, u)
		tokens += u.tokens
		if tokens >= targetTokens {
			emit()
		}
	}
	emit()
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
